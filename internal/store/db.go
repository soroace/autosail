package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"golang.org/x/crypto/bcrypt"
)

type Store struct {
	path string
	db   *sql.DB
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	IsAdmin      bool
}

type Key struct {
	ID          int64
	UserID      int64
	Name        string
	AccessKey   string
	SecretKey   string
	Proxy       string
	QuotaRegion string
	QuotaOn     string
	QuotaSpot   string
	QuotaOnName string
	QuotaSpName string
	CreatedAt   time.Time
}

func NewSQLiteStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{path: path, db: db}
	if err := s.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层数据库连接（优雅关闭时调用）
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// DB 暴露底层连接，供其它模块（如会话持久化）复用同一个连接池
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) initSchema(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			access_key TEXT NOT NULL,
			secret_key TEXT NOT NULL,
			proxy TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id);`,
	}
	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if err := s.ensureAPIKeyColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureUserColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureSettingsDefaults(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureUserColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(users);`)
	if err != nil {
		return err
	}
	defer rows.Close()
	existing := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal *string
			pk         int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, ok := existing["is_admin"]; ok {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0;")
	return err
}

func (s *Store) ensureSettingsDefaults(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES ('registration_open', '1');`)
	return err
}

func (s *Store) ensureAPIKeyColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(api_keys);`)
	if err != nil {
		return err
	}
	defer rows.Close()
	existing := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal *string
			pk         int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	columns := map[string]string{
		"quota_region":  "TEXT NOT NULL DEFAULT ''",
		"quota_on":      "TEXT NOT NULL DEFAULT ''",
		"quota_spot":    "TEXT NOT NULL DEFAULT ''",
		"quota_on_name": "TEXT NOT NULL DEFAULT ''",
		"quota_sp_name": "TEXT NOT NULL DEFAULT ''",
		"sort_order":    "INTEGER NOT NULL DEFAULT 0",
	}
	for name, def := range columns {
		if _, ok := existing[name]; ok {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE api_keys ADD COLUMN %s %s;", name, def)
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) EnsureUser(username, password string) (bool, error) {
	ctx := context.Background()
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return false, errors.New("username/password required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	selectStmt, err := tx.PrepareContext(ctx, `SELECT id FROM users WHERE username = ? LIMIT 1;`)
	if err != nil {
		return false, err
	}
	defer selectStmt.Close()
	var existingID int64
	err = selectStmt.QueryRowContext(ctx, username).Scan(&existingID)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	var userCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM users;`).Scan(&userCount); err != nil {
		return false, err
	}
	isAdmin := 0
	if userCount == 0 {
		isAdmin = 1
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	insertStmt, err := tx.PrepareContext(ctx, `INSERT INTO users (username, password_hash, is_admin) VALUES (?, ?, ?);`)
	if err != nil {
		return false, err
	}
	defer insertStmt.Close()
	_, err = insertStmt.ExecContext(ctx, username, string(hash), isAdmin)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) CreateUser(ctx context.Context, username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return nil, errors.New("username/password required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var existingID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ? LIMIT 1;`, username).Scan(&existingID); err == nil {
		return nil, errors.New("user exists")
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	var userCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM users;`).Scan(&userCount); err != nil {
		return nil, err
	}
	isAdmin := 0
	if userCount == 0 {
		isAdmin = 1
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users (username, password_hash, is_admin) VALUES (?, ?, ?);`, username, string(hash), isAdmin)
	if err != nil {
		return nil, err
	}
	insertID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &User{ID: insertID, Username: username, PasswordHash: string(hash), IsAdmin: isAdmin == 1}, nil
}

func (s *Store) AuthenticateUser(ctx context.Context, username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return nil, errors.New("missing credentials")
	}
	stmt, err := s.db.PrepareContext(ctx, `SELECT id, username, password_hash, is_admin FROM users WHERE username = ? LIMIT 1;`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	u := &User{}
	var isAdmin int
	if err := stmt.QueryRowContext(ctx, username).Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin); err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	u.IsAdmin = isAdmin == 1
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, errors.New("invalid password")
	}
	return u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	stmt, err := s.db.PrepareContext(ctx, `SELECT id, username, password_hash, is_admin FROM users ORDER BY id ASC;`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var (
			u       User
			isAdmin int
		)
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin); err != nil {
			return nil, err
		}
		u.IsAdmin = isAdmin == 1
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	if userID == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id = ?;`, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?;`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM users WHERE is_admin = 1;`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM users;`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) RegistrationOpen(ctx context.Context) (bool, error) {
	var val string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'registration_open' LIMIT 1;`).Scan(&val); err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}
	return strings.TrimSpace(val) != "0", nil
}

func (s *Store) SetRegistrationOpen(ctx context.Context, open bool) error {
	value := "0"
	if open {
		value = "1"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('registration_open', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value;`, value)
	return err
}

func (s *Store) ListKeys(ctx context.Context, userID int64) ([]Key, error) {
	stmt, err := s.db.PrepareContext(ctx, `SELECT id, user_id, name, access_key, secret_key, proxy, quota_region, quota_on, quota_spot, quota_on_name, quota_sp_name, created_at FROM api_keys WHERE user_id = ? ORDER BY sort_order ASC, id DESC;`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.QueryContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		var (
			key          Key
			createdAtRaw string
		)
		if err := rows.Scan(&key.ID, &key.UserID, &key.Name, &key.AccessKey, &key.SecretKey, &key.Proxy, &key.QuotaRegion, &key.QuotaOn, &key.QuotaSpot, &key.QuotaOnName, &key.QuotaSpName, &createdAtRaw); err != nil {
			return nil, err
		}
		key.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtRaw)
		out = append(out, Key{
			ID:          key.ID,
			UserID:      key.UserID,
			Name:        key.Name,
			AccessKey:   key.AccessKey,
			SecretKey:   key.SecretKey,
			Proxy:       key.Proxy,
			QuotaRegion: key.QuotaRegion,
			QuotaOn:     key.QuotaOn,
			QuotaSpot:   key.QuotaSpot,
			QuotaOnName: key.QuotaOnName,
			QuotaSpName: key.QuotaSpName,
			CreatedAt:   key.CreatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) CreateKey(ctx context.Context, userID int64, name, accessKey, secretKey, proxy string) (int64, error) {
	if strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" {
		return 0, errors.New("missing key values")
	}
	if strings.TrimSpace(name) == "" {
		name = time.Now().Format("2006-01-02 15:04")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	insertStmt, err := tx.PrepareContext(ctx, `INSERT INTO api_keys (user_id, name, access_key, secret_key, proxy) VALUES (?, ?, ?, ?, ?);`)
	if err != nil {
		return 0, err
	}
	defer insertStmt.Close()
	if _, err = insertStmt.ExecContext(ctx, userID, name, accessKey, secretKey, proxy); err != nil {
		return 0, err
	}
	var insertID int64
	if err = tx.QueryRowContext(ctx, `SELECT last_insert_rowid();`).Scan(&insertID); err != nil {
		return 0, err
	}
	// 新账号排在列表末尾
	if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET sort_order = (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM api_keys WHERE user_id = ?) WHERE id = ?;`, userID, insertID); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return insertID, nil
}

func (s *Store) DeleteKey(ctx context.Context, userID, keyID int64) error {
	if keyID == 0 {
		return nil
	}
	stmt, err := s.db.PrepareContext(ctx, `DELETE FROM api_keys WHERE id = ? AND user_id = ?;`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx, keyID, userID)
	return err
}

func (s *Store) UpdateKey(ctx context.Context, userID, keyID int64, name, accessKey, secretKey, proxy string) error {
	if keyID == 0 {
		return errors.New("missing key id")
	}
	if strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" {
		return errors.New("missing key values")
	}
	if strings.TrimSpace(name) == "" {
		name = time.Now().Format("2006-01-02 15:04")
	}
	stmt, err := s.db.PrepareContext(ctx, `UPDATE api_keys SET name = ?, access_key = ?, secret_key = ?, proxy = ? WHERE id = ? AND user_id = ?;`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx, name, accessKey, secretKey, proxy, keyID, userID)
	return err
}

// UpdateKeysOrder 按传入的 id 顺序保存账号排序。
func (s *Store) UpdateKeysOrder(ctx context.Context, userID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.PrepareContext(ctx, `UPDATE api_keys SET sort_order = ? WHERE id = ? AND user_id = ?;`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, id := range ids {
		if _, err = stmt.ExecContext(ctx, i+1, id, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateKeyQuota(ctx context.Context, userID, keyID int64, region, onVal, spotVal, onName, spotName string) error {
	if keyID == 0 {
		return errors.New("missing key id")
	}
	stmt, err := s.db.PrepareContext(ctx, `UPDATE api_keys SET quota_region = ?, quota_on = ?, quota_spot = ?, quota_on_name = ?, quota_sp_name = ? WHERE id = ? AND user_id = ?;`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx, region, onVal, spotVal, onName, spotName, keyID, userID)
	return err
}
