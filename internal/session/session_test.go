package session

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// 基本读写 + 同一 sid 返回同一会话
func TestSessionSetGet(t *testing.T) {
	st := NewStore()
	defer st.Close()

	s := st.GetOrCreate("sid-1")
	s.SetString("user_id", "42")
	if got := s.GetString("user_id", ""); got != "42" {
		t.Fatalf("期望 42，实际 %q", got)
	}
	if got := s.GetString("missing", "def"); got != "def" {
		t.Fatalf("缺省值错误: %q", got)
	}
	if st.GetOrCreate("sid-1") != s {
		t.Fatal("同一 sid 应返回同一会话实例")
	}
}

// 超过 TTL 的会话应被清理
func TestCleanupExpired(t *testing.T) {
	st := newStore(nil, 50*time.Millisecond)
	defer st.Close()

	s := st.GetOrCreate("old")
	s.SetString("k", "v")
	time.Sleep(80 * time.Millisecond)
	st.cleanupExpired()

	if got := st.GetOrCreate("old").GetString("k", ""); got != "" {
		t.Fatalf("过期会话应被清理，实际 %q", got)
	}
}

// 持久化：进程重启（新的 Store 复用同一个库）后仍能恢复登录态
func TestPersistenceAcrossRestart(t *testing.T) {
	db := openTestDB(t)

	st1, err := NewStoreWithDB(db, time.Hour)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	st1.GetOrCreate("sid-1").SetString("user_id", "7")
	st1.Close()

	st2, err := NewStoreWithDB(db, time.Hour)
	if err != nil {
		t.Fatalf("重建 store 失败: %v", err)
	}
	defer st2.Close()

	if got := st2.GetOrCreate("sid-1").GetString("user_id", ""); got != "7" {
		t.Fatalf("重启后应恢复登录态，实际 %q", got)
	}
}

// 已过期的会话不应从库里恢复，且对应行应被删除
func TestExpiredSessionNotRestored(t *testing.T) {
	db := openTestDB(t)

	st1, err := NewStoreWithDB(db, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	st1.GetOrCreate("sid-old").SetString("user_id", "9")
	st1.Close()

	time.Sleep(60 * time.Millisecond)

	st2, err := NewStoreWithDB(db, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("重建 store 失败: %v", err)
	}
	defer st2.Close()

	if got := st2.GetOrCreate("sid-old").GetString("user_id", ""); got != "" {
		t.Fatalf("过期会话不应恢复，实际 %q", got)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE sid = ?`, "sid-old").Scan(&n); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("过期会话行应被删除，剩余 %d 行", n)
	}
}
