package aws

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cleanupTransportCache 清理本用例写入的全局缓存，避免用例间互相影响
func cleanupTransportCache(t *testing.T, proxies ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, p := range proxies {
			if v, ok := transportCache.LoadAndDelete(p); ok {
				if tr, ok := v.(*http.Transport); ok {
					tr.CloseIdleConnections()
				}
			}
		}
	})
}

// 同一 proxy 复用同一个 Transport；不同 proxy（含直连）互相隔离
func TestBaseHTTPClientReusesTransportByProxy(t *testing.T) {
	const proxy = "http://127.0.0.1:7890"
	const otherProxy = "http://127.0.0.1:1080"
	cleanupTransportCache(t, proxy, otherProxy, "")

	a, err := baseHTTPClient(proxy)
	if err != nil {
		t.Fatalf("baseHTTPClient(%q) 返回错误: %v", proxy, err)
	}
	b, err := baseHTTPClient(proxy)
	if err != nil {
		t.Fatalf("baseHTTPClient(%q) 返回错误: %v", proxy, err)
	}
	if a.Transport != b.Transport {
		t.Fatal("同一 proxy 应复用同一个 Transport")
	}
	if a == b {
		t.Fatal("每次应返回独立的 http.Client，便于各自设置 Timeout")
	}

	direct, err := baseHTTPClient("")
	if err != nil {
		t.Fatalf("baseHTTPClient(\"\") 返回错误: %v", err)
	}
	if direct.Transport == a.Transport {
		t.Fatal("直连与走代理不应共用 Transport")
	}

	otherClient, err := baseHTTPClient(otherProxy)
	if err != nil {
		t.Fatalf("baseHTTPClient 返回错误: %v", err)
	}
	if otherClient.Transport == a.Transport {
		t.Fatal("不同 proxy 不应共用 Transport")
	}
}

// netcheck 会把自己 Client 的 Timeout 改成 12s，不能影响其他调用的 25s。
func TestBaseHTTPClientTimeoutIsPerClient(t *testing.T) {
	cleanupTransportCache(t, "")
	c1, err := baseHTTPClient("")
	if err != nil {
		t.Fatalf("baseHTTPClient 返回错误: %v", err)
	}
	c1.Timeout = 12 * time.Second

	c2, err := baseHTTPClient("")
	if err != nil {
		t.Fatalf("baseHTTPClient 返回错误: %v", err)
	}
	if c2.Timeout != 25*time.Second {
		t.Fatalf("Client 超时被串改：期望 25s，实际 %v", c2.Timeout)
	}
}

func TestBaseHTTPClientRejectsInvalidProxy(t *testing.T) {
	if _, err := baseHTTPClient("://bad"); err == nil {
		t.Fatal("非法 proxy 应返回错误")
	}
}

// 并发首访同一 proxy 必须收敛到同一个 Transport（LoadOrStore 竞争路径）
func TestBaseHTTPClientConcurrentSameProxy(t *testing.T) {
	const proxy = "http://127.0.0.1:7890"
	cleanupTransportCache(t, proxy)

	const n = 50
	got := make([]http.RoundTripper, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // 尽量同时进入，制造 LoadOrStore 竞争
			c, err := baseHTTPClient(proxy)
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				return
			}
			got[idx] = c.Transport
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if got[i] == nil {
			t.Fatalf("goroutine %d 未拿到 Transport", i)
		}
		if got[i] != got[0] {
			t.Fatalf("并发调用应复用同一个 Transport，goroutine %d 拿到了不同的", i)
		}
	}
}

// 并发使用不同 proxy 时，各 key 不能被串到一起。
func TestBaseHTTPClientConcurrentDistinctProxies(t *testing.T) {
	proxies := []string{"http://127.0.0.1:7891", "http://127.0.0.1:7892"}
	cleanupTransportCache(t, proxies...)

	const rounds = 20
	var wg sync.WaitGroup
	for _, p := range proxies {
		for r := 0; r < rounds; r++ {
			wg.Add(1)
			go func(proxy string) {
				defer wg.Done()
				if _, err := baseHTTPClient(proxy); err != nil {
					t.Errorf("baseHTTPClient(%q): %v", proxy, err)
				}
			}(p)
		}
	}
	wg.Wait()

	a, _ := baseHTTPClient(proxies[0])
	b, _ := baseHTTPClient(proxies[1])
	if a.Transport == b.Transport {
		t.Fatal("不同 proxy 的并发调用被串到了同一个 Transport")
	}
}

// 连续请求应复用同一条 TCP 连接
func TestBaseHTTPClientReusesConnection(t *testing.T) {
	if os.Getenv("HTTP_PROXY") != "" || os.Getenv("HTTPS_PROXY") != "" {
		t.Skip("环境设置了代理，本地直连复用断言不适用")
	}
	cleanupTransportCache(t, "")

	var newConns int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&newConns, 1)
		}
	}
	srv.Start()
	defer srv.Close()

	c, err := baseHTTPClient("")
	if err != nil {
		t.Fatalf("baseHTTPClient: %v", err)
	}
	for i := 0; i < 3; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("第 %d 次请求失败: %v", i+1, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	if n := atomic.LoadInt32(&newConns); n != 1 {
		t.Fatalf("3 次请求应复用 1 条 TCP 连接，实际建立了 %d 条", n)
	}
}
