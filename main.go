package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/patrickmn/go-cache"

	"autosail/internal/aws"
	"autosail/internal/session"
	"autosail/internal/store"
)

const maxFlashErrorLen = 200

type Flash struct {
	Success string
	Error   string
	Warn    string
	Info    string
}

// PageCommon 每个页面都需要的字段
type PageCommon struct {
	Title     string
	CSRFToken string

	Username string
	IsAdmin  bool

	RegistrationOpen    bool
	RegistrationCaptcha string
	Users               []store.User
	CurrentUserID       int64

	// 凭证侧栏（创建页/管理页共用）
	HasCreds    bool
	KeyName     string
	AK          string
	Proxy       string
	Keys        []store.Key
	ActiveKeyID int64
	ActiveKey   string
	PendingKey  int64

	Region string
	AZ     string

	Tab string // create / manage / quota

	Flash Flash

	// ToastInfo: 右下角全局提示弹窗（如已退出登录）
	ToastInfo string

	// Proxy check
	ProxyExitIP  string
	ProxyExitASN string
}

// PageCreate 创建实例页专属字段
type PageCreate struct {
	CreateEnableFW   bool
	CreateIPType     string
	CreateBlueprint  string
	CreateBundle     string
	CreateRootPwd    string
	CreateService    string
	CreateEC2AMI     string
	CreateEC2Type    string
	CreateEC2IPv6    bool
	CreateBundleName string

	Blueprints []Option
	Bundles    []Option
	IPTypes    []Option
	EC2AMIs    []Option
	EC2Types   []Option

	CreateRegions []RegionOption
}

// PageManage 管理页专属字段（实例列表见 InstanceSectionData）
type PageManage struct {
	ManageService string
	ManageRegions []RegionOption
}

// PageQuota 配额页专属字段
type PageQuota struct {
	QuotaRegions []RegionOption
	QuotaRegion  string
	QuotaOn      string
	QuotaSpot    string
	QuotaOnName  string
	QuotaSpName  string
}

// PageData 页面数据：按 tab 分组，经匿名嵌入保持字段可直接访问（模板无需改动）
type PageData struct {
	PageCommon
	PageCreate
	PageManage
	PageQuota
}

// InstanceSectionData 实例列表片段的数据：由 /aws/instances 异步渲染，
// 避免管理页在 HTTP 请求里同步等待 AWS 调用（慢时会让页面卡几十秒）。
type InstanceSectionData struct {
	CSRFToken     string
	Region        string
	ManageService string
	Instances     []aws.InstanceView
	EC2Instances  []aws.EC2InstanceView
	Error         string
}

func formatFlashError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	if len(msg) > maxFlashErrorLen {
		msg = msg[:maxFlashErrorLen] + "..."
	}
	return msg
}

const (
	instCacheTTL   = 10 * time.Second // 实例列表缓存时长
	instPollMargin = 2 * time.Second  // 前端轮询需略大于缓存时长
)

var (
	instCache = cache.New(instCacheTTL, 30*time.Second)
	appStore  *store.Store
)

// instPollIntervalMS 前端静默刷新间隔，由缓存时长推导，避免前后端各自硬编码而脱节
func instPollIntervalMS() int {
	return int((instCacheTTL + instPollMargin) / time.Millisecond)
}

//go:embed templates/*.html
var templateFS embed.FS


func mustEnvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}


func regionLabel(id string) string {
	for _, r := range allRegionOptions() {
		if r.ID == id {
			return r.Name
		}
	}
	return id
}

// 防止 region 被存坏（a/b/c 或 us-east-1a）导致 ResolveEndpointV2 not found
func normalizeRegion(r string) string {
	r = strings.TrimSpace(r)
	if r == "a" || r == "b" || r == "c" {
		return "us-east-1"
	}
	if len(r) >= 2 {
		last := r[len(r)-1]
		if (last == 'a' || last == 'b' || last == 'c') && strings.Contains(r, "-") {
			p := r[len(r)-2]
			if p >= '0' && p <= '9' { // us-east-1a
				return r[:len(r)-1]
			}
		}
	}
	return r
}

func genSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func genCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randInt(min, max int) int {
	if max <= min {
		return min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		return min
	}
	return int(n.Int64()) + min
}

func genRegistrationCaptcha() (string, string) {
	left := randInt(2, 9)
	right := randInt(2, 9)
	return strconv.Itoa(left) + " + " + strconv.Itoa(right), strconv.Itoa(left + right)
}

func main() {
	var err error
	port := mustEnvInt("PORT", 9000)
	dbPath := strings.TrimSpace(os.Getenv("DB_PATH"))
	if dbPath == "" {
		dbPath = filepath.Join("data", "app.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		panic(err)
	}

	appStore, err = store.NewSQLiteStore(dbPath)
	if err != nil {
		panic(err)
	}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	// 静态资源强制重验证：改 JS/CSS 后浏览器不会继续用旧缓存（304 开销极小）
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/static/") {
			c.Header("Cache-Control", "no-cache")
		}
		c.Next()
	})
	r.Static("/static", "./static")

	// templates
	tmpl := template.Must(template.New("").Funcs(template.FuncMap{
		"regionLabel": regionLabel,
		"instPollMS":  instPollIntervalMS,
	}).ParseFS(templateFS, "templates/*.html"))
	r.SetHTMLTemplate(tmpl)

	// session store
	store := session.NewStore()

	// Middleware: get/create session
	r.Use(func(c *gin.Context) {
		sid, err := c.Cookie("sid")
		if err != nil || sid == "" {
			sid = genSessionID()
			c.SetCookie("sid", sid, 3600*24*7, "/", "", false, true)
		}
		s := store.GetOrCreate(sid)
		c.Set("sess", s)
		c.Set("sid", sid)
		c.Next()
	})

	r.Use(func(c *gin.Context) {
		s := session.Must(c)
		token := strings.TrimSpace(s.GetString("csrf_token", ""))
		if token == "" {
			token = genCSRFToken()
			s.SetString("csrf_token", token)
		}
		c.Set("csrf_token", token)
		if c.Request.Method == http.MethodPost {
			formToken := strings.TrimSpace(c.PostForm("csrf_token"))
			if formToken == "" || formToken != token {
				if c.Request.URL.Path == "/login" {
					c.Redirect(http.StatusFound, "/login?msg=csrf")
				} else {
					c.Redirect(http.StatusFound, "/?msg=csrf")
				}
				c.Abort()
				return
			}
		}
		c.Next()
	})

	r.GET("/login", handleLoginPage)

	r.POST("/login", handleLoginPost)

	r.GET("/register", handleRegisterPage)

	r.POST("/register", handleRegisterPost)

	r.POST("/logout", handleLogout)

	r.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodPost && c.Request.URL.Path == "/login" {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodPost && c.Request.URL.Path == "/register" {
			c.Next()
			return
		}
		if c.Request.URL.Path == "/login" {
			c.Next()
			return
		}
		if c.Request.URL.Path == "/register" {
			c.Next()
			return
		}
		if !isLoggedIn(session.Must(c)) {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}
		c.Next()
	})

	r.GET("/", handleIndex)

	r.POST("/admin/registration", handleAdminRegistration)

	r.POST("/admin/users/delete", handleAdminUserDelete)

	// Save creds (留空不覆盖)
	r.POST("/auth/save", handleKeySave)

	// Select key
	r.POST("/auth/select", handleKeySelect)

	// Activate selected key
	r.POST("/auth/activate", handleKeyActivate)

	// Delete current key
	r.POST("/auth/delete", handleKeyDelete)

	// Reorder keys (drag & drop)
	r.POST("/auth/reorder", handleKeyReorder)

	// Proxy exit IP check (uses current session proxy)
	r.GET("/proxy/check", handleProxyCheck)

	// 拉取当前区域真实可用套餐（避免硬编码代次过期导致 NotFoundException）
	r.GET("/aws/bundles", handleBundles)

	// 实例列表片段（异步加载，避免管理页渲染时同步阻塞 AWS 调用）
	r.GET("/aws/instances", handleInstances)

	// Create instance
	r.POST("/aws/create", handleCreate)

	// Manage actions
	r.POST("/aws/reboot", handleReboot)

	r.POST("/aws/openall", handleOpenAll)

	// Clear manage list cache (per region+ak+proxy)
	r.POST("/aws/refresh", handleRefresh)

	r.POST("/aws/delete", handleDelete)

	r.POST("/aws/swapip", handleSwapIP)

	r.POST("/aws/ec2/start", handleEC2Start)

	r.POST("/aws/ec2/stop", handleEC2Stop)

	r.POST("/aws/ec2/reboot", handleEC2Reboot)

	r.POST("/aws/ec2/terminate", handleEC2Terminate)

	r.POST("/aws/ec2/openall", handleEC2OpenAll)

	// Quota test
	r.POST("/aws/quota", handleQuota)

	addr := ":" + strconv.Itoa(port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// 优雅关闭：监听 Ctrl+C / SIGTERM，等待进行中的请求处理完再退出
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("AutoSail listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("failed to start server on %s: %v", addr, err)
		}
	}()

	<-ctx.Done()
	log.Println("收到退出信号，正在优雅关闭...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}
	if err := appStore.Close(); err != nil {
		log.Printf("failed to close database: %v", err)
	}
	log.Println("server exited gracefully")
}

func isLoggedIn(s *session.Session) bool {
	return strings.TrimSpace(s.GetString("user_id", "")) != ""
}

func isAdminSession(s *session.Session) bool {
	return strings.TrimSpace(s.GetString("is_admin", "")) == "1"
}

func userIDFromSession(s *session.Session) (int64, bool) {
	idStr := strings.TrimSpace(s.GetString("user_id", ""))
	if idStr == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func resolveActiveKey(s *session.Session, keys []store.Key) (*store.Key, bool) {
	if len(keys) == 0 {
		return nil, false
	}
	keyIDStr := strings.TrimSpace(s.GetString("key_id", ""))
	if keyIDStr != "" {
		if keyID, err := strconv.ParseInt(keyIDStr, 10, 64); err == nil {
			for i := range keys {
				if keys[i].ID == keyID {
					return &keys[i], true
				}
			}
		}
	}
	return nil, false
}

func resolvePendingKeyID(s *session.Session, keys []store.Key, activeKey *store.Key) int64 {
	keyIDStr := strings.TrimSpace(s.GetString("pending_key_id", ""))
	if keyIDStr != "" {
		if keyID, err := strconv.ParseInt(keyIDStr, 10, 64); err == nil {
			if keyExists(keys, keyID) {
				return keyID
			}
		}
	}
	if activeKey != nil {
		return activeKey.ID
	}
	return 0
}

func findOptionName(options []Option, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	for _, opt := range options {
		if opt.ID == id {
			return opt.Name
		}
	}
	return ""
}

func keyExists(keys []store.Key, keyID int64) bool {
	for _, key := range keys {
		if key.ID == keyID {
			return true
		}
	}
	return false
}

func findKeyByID(keys []store.Key, keyID int64) *store.Key {
	if keyID == 0 {
		return nil
	}
	for i := range keys {
		if keys[i].ID == keyID {
			return &keys[i]
		}
	}
	return nil
}

func keyName(k *store.Key) string {
	if k == nil {
		return ""
	}
	return k.Name
}

func keyAccessKey(k *store.Key) string {
	if k == nil {
		return ""
	}
	return k.AccessKey
}

func keyProxy(k *store.Key) string {
	if k == nil {
		return ""
	}
	return k.Proxy
}

func keyID(k *store.Key) int64 {
	if k == nil {
		return 0
	}
	return k.ID
}
