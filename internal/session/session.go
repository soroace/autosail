package session

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// DefaultTTL 会话空闲超时：有请求就自动续期，超过这个时长没操作才失效
const DefaultTTL = 12 * time.Hour

const (
	defaultCleanupInterval = 5 * time.Minute
	// touchPersistInterval 活跃时间落库节流，避免每个请求都写库
	touchPersistInterval = time.Minute
)

type Session struct {
	mu         sync.RWMutex
	m          map[string]string
	lastAccess time.Time
	lastSaved  time.Time // 上次把 last_access 写入库的时间
	persisted  bool      // 是否已写入过库（空会话不落库）
	sid        string
	st         *Store
}

func (s *Session) GetString(key, def string) string {
	s.mu.RLock()
	v, ok := s.m[key]
	s.mu.RUnlock()
	s.touch()
	if ok {
		return v
	}
	return def
}

func (s *Session) SetString(key, val string) {
	now := time.Now()
	s.mu.Lock()
	s.m[key] = val
	s.lastAccess = now
	s.lastSaved = now
	data, _ := json.Marshal(s.m)
	s.mu.Unlock()
	if s.st != nil {
		s.st.save(s.sid, string(data), now)
		s.mu.Lock()
		s.persisted = true
		s.mu.Unlock()
	}
}

func (s *Session) touch() {
	now := time.Now()
	s.mu.Lock()
	s.lastAccess = now
	due := s.st != nil && s.persisted && now.Sub(s.lastSaved) >= touchPersistInterval
	if due {
		s.lastSaved = now
	}
	s.mu.Unlock()
	if due {
		s.st.touch(s.sid, now)
	}
}

func (s *Session) LastAccess() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastAccess
}

type Store struct {
	mu      sync.RWMutex
	m       map[string]*Session
	ttl     time.Duration
	cleanup time.Duration
	db      *sql.DB

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewStore 创建纯内存会话存储（进程重启后登录状态丢失）
func NewStore() *Store {
	return newStore(nil, DefaultTTL)
}

// NewStoreWithDB 创建带持久化的会话存储：会话写入 SQLite，进程重启后仍保持登录。
// db 为 nil 时退化为纯内存模式。
func NewStoreWithDB(db *sql.DB, ttl time.Duration) (*Store, error) {
	st := newStore(db, ttl)
	if db != nil {
		if err := st.initSchema(); err != nil {
			st.Close()
			return nil, err
		}
	}
	return st, nil
}

func newStore(db *sql.DB, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	st := &Store{
		m:       map[string]*Session{},
		ttl:     ttl,
		cleanup: defaultCleanupInterval,
		db:      db,
		stopCh:  make(chan struct{}),
	}
	go st.cleanupLoop()
	return st
}

func (st *Store) initSchema() error {
	if _, err := st.db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		sid TEXT PRIMARY KEY,
		data TEXT NOT NULL,
		last_access INTEGER NOT NULL
	);`); err != nil {
		return err
	}
	_, err := st.db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_last_access ON sessions(last_access);`)
	return err
}

func (st *Store) GetOrCreate(id string) *Session {
	st.mu.RLock()
	s, ok := st.m[id]
	st.mu.RUnlock()
	if ok {
		s.touch()
		return s
	}

	if s = st.load(id); s != nil {
		return s
	}

	s = &Session{m: map[string]string{}, lastAccess: time.Now(), sid: id, st: st}
	st.mu.Lock()
	if existing, ok := st.m[id]; ok {
		st.mu.Unlock()
		existing.touch()
		return existing
	}
	st.m[id] = s
	st.mu.Unlock()
	return s
}

// load 从数据库恢复未过期的会话；不存在或已过期返回 nil
func (st *Store) load(id string) *Session {
	if st.db == nil {
		return nil
	}
	var (
		data       string
		lastAccess int64
	)
	if err := st.db.QueryRow(`SELECT data, last_access FROM sessions WHERE sid = ?`, id).Scan(&data, &lastAccess); err != nil {
		return nil
	}
	la := time.Unix(lastAccess, 0)
	if time.Since(la) > st.ttl {
		_, _ = st.db.Exec(`DELETE FROM sessions WHERE sid = ?`, id)
		return nil
	}

	m := map[string]string{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		m = map[string]string{}
	}
	s := &Session{m: m, lastAccess: la, lastSaved: la, persisted: true, sid: id, st: st}

	st.mu.Lock()
	if existing, ok := st.m[id]; ok {
		st.mu.Unlock()
		existing.touch()
		return existing
	}
	st.m[id] = s
	st.mu.Unlock()
	return s
}

func (st *Store) save(sid, data string, now time.Time) {
	if st.db == nil {
		return
	}
	_, _ = st.db.Exec(`INSERT INTO sessions (sid, data, last_access) VALUES (?, ?, ?)
		ON CONFLICT(sid) DO UPDATE SET data = excluded.data, last_access = excluded.last_access;`,
		sid, data, now.Unix())
}

func (st *Store) touch(sid string, now time.Time) {
	if st.db == nil {
		return
	}
	_, _ = st.db.Exec(`UPDATE sessions SET last_access = ? WHERE sid = ?;`, now.Unix(), sid)
}

func (st *Store) cleanupLoop() {
	ticker := time.NewTicker(st.cleanup)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			st.cleanupExpired()
		case <-st.stopCh:
			return
		}
	}
}

func (st *Store) cleanupExpired() {
	expiredBefore := time.Now().Add(-st.ttl)

	st.mu.Lock()
	for id, sess := range st.m {
		if sess.LastAccess().Before(expiredBefore) {
			delete(st.m, id)
		}
	}
	st.mu.Unlock()

	if st.db != nil {
		_, _ = st.db.Exec(`DELETE FROM sessions WHERE last_access < ?;`, expiredBefore.Unix())
	}
}

// Close 停止清理协程（数据库连接由调用方负责关闭）
func (st *Store) Close() {
	st.stopOnce.Do(func() { close(st.stopCh) })
}

func Must(c *gin.Context) *Session {
	v, ok := c.Get("sess")
	if !ok {
		panic("session missing")
	}
	return v.(*Session)
}
