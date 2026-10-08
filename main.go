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
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
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

type PageData struct {
	Title     string
	CSRFToken string

	Username string
	IsAdmin  bool

	RegistrationOpen    bool
	RegistrationCaptcha string
	Users               []store.User
	CurrentUserID       int64

	HasCreds    bool
	KeyName     string
	AK          string
	Proxy       string
	Keys        []store.Key
	ActiveKeyID int64
	ActiveKey   string
	PendingKey  int64

	Region        string
	CreateRegions []RegionOption
	ManageRegions []RegionOption
	QuotaRegions  []RegionOption
	AZ            string

	// Tabs: create/manage/quota
	Tab string

	Flash Flash

	// ToastInfo: 右下角全局提示弹窗（如已退出登录）
	ToastInfo string

	// Create form
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

	// Proxy check
	ProxyExitIP  string
	ProxyExitASN string

	// Manage
	Instances     []aws.InstanceView
	EC2Instances  []aws.EC2InstanceView
	ManageService string

	// Quota
	QuotaRegion string
	QuotaOn     string
	QuotaSpot   string
	QuotaOnName string
	QuotaSpName string
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

var (
	// cache instances list for 10s
	instCache = cache.New(10*time.Second, 30*time.Second)
	appStore  *store.Store
)

//go:embed templates/*.html
var templateFS embed.FS

type RegionOption struct {
	ID   string
	Name string
}

type Option struct {
	ID   string
	Name string
}

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

var lightsailRegionOptions = []RegionOption{
	{ID: "ap-northeast-1", Name: "东京 Tokyo"},
	{ID: "ap-northeast-2", Name: "首尔 Seoul"},
	{ID: "ap-southeast-1", Name: "新加坡 Singapore"},
	{ID: "ap-southeast-2", Name: "悉尼 Sydney"},
	{ID: "ap-south-1", Name: "孟买 Mumbai"},
	{ID: "us-east-1", Name: "弗吉尼亚 N. Virginia"},
	{ID: "us-east-2", Name: "俄亥俄 Ohio"},
	{ID: "us-west-2", Name: "俄勒冈 Oregon"},
	{ID: "ca-central-1", Name: "加拿大（中部） Canada Central"},
	{ID: "eu-central-1", Name: "法兰克福 Frankfurt"},
	{ID: "eu-west-1", Name: "爱尔兰 Ireland"},
	{ID: "eu-west-2", Name: "伦敦 London"},
	{ID: "eu-west-3", Name: "巴黎 Paris"},
	{ID: "eu-north-1", Name: "斯德哥尔摩 Stockholm"},
}

var ec2RegionOptions = []RegionOption{
	{ID: "af-south-1", Name: "开普敦 Cape Town"},
	{ID: "ap-east-1", Name: "香港 Hong Kong"},
	{ID: "ap-northeast-1", Name: "东京 Tokyo"},
	{ID: "ap-northeast-2", Name: "首尔 Seoul"},
	{ID: "ap-northeast-3", Name: "大阪 Osaka"},
	{ID: "ap-south-1", Name: "孟买 Mumbai"},
	{ID: "ap-south-2", Name: "海得拉巴 Hyderabad"},
	{ID: "ap-southeast-1", Name: "新加坡 Singapore"},
	{ID: "ap-southeast-2", Name: "悉尼 Sydney"},
	{ID: "ap-southeast-3", Name: "雅加达 Jakarta"},
	{ID: "ap-southeast-4", Name: "墨尔本 Melbourne"},
	{ID: "ca-central-1", Name: "加拿大（中部） Canada Central"},
	{ID: "ca-west-1", Name: "加拿大（西部） Canada West"},
	{ID: "eu-central-1", Name: "法兰克福 Frankfurt"},
	{ID: "eu-central-2", Name: "苏黎世 Zurich"},
	{ID: "eu-north-1", Name: "斯德哥尔摩 Stockholm"},
	{ID: "eu-south-1", Name: "米兰 Milan"},
	{ID: "eu-south-2", Name: "西班牙 Spain"},
	{ID: "eu-west-1", Name: "爱尔兰 Ireland"},
	{ID: "eu-west-2", Name: "伦敦 London"},
	{ID: "eu-west-3", Name: "巴黎 Paris"},
	{ID: "il-central-1", Name: "以色列（中部） Israel"},
	{ID: "me-central-1", Name: "阿联酋 UAE"},
	{ID: "me-south-1", Name: "巴林 Bahrain"},
	{ID: "sa-east-1", Name: "圣保罗 São Paulo"},
	{ID: "us-east-1", Name: "弗吉尼亚 N. Virginia"},
	{ID: "us-east-2", Name: "俄亥俄 Ohio"},
	{ID: "us-west-1", Name: "北加州 N. California"},
	{ID: "us-west-2", Name: "俄勒冈 Oregon"},
}

func regionOptionsForService(service string) []RegionOption {
	if service == "ec2" {
		return ec2RegionOptions
	}
	return lightsailRegionOptions
}

func allRegionOptions() []RegionOption {
	all := make([]RegionOption, 0, len(ec2RegionOptions)+len(lightsailRegionOptions))
	all = append(all, ec2RegionOptions...)
	for _, r := range lightsailRegionOptions {
		if !containsRegion(all, r.ID) {
			all = append(all, r)
		}
	}
	return all
}

func containsRegion(list []RegionOption, id string) bool {
	for _, r := range list {
		if r.ID == id {
			return true
		}
	}
	return false
}

var blueprintOptions = []Option{
	{ID: "ubuntu_24_04", Name: "Ubuntu 24.04 LTS"},
	{ID: "ubuntu_22_04", Name: "Ubuntu 22.04 LTS"},
	{ID: "debian_12", Name: "Debian 12"},
	{ID: "debian_11", Name: "Debian 11"},
	{ID: "centos_7", Name: "CentOS 7"},
}

var bundleOptions = []Option{
	{ID: "nano_3_0", Name: "nano (2 vCPUs, 内存 0.5 GB, 硬盘 20 GB, 流量 1024 GB / 月)"},
	{ID: "micro_3_0", Name: "micro (2 vCPUs, 内存 1 GB, 硬盘 40 GB, 流量 2048 GB / 月)"},
	{ID: "small_3_0", Name: "small (2 vCPUs, 内存 2 GB, 硬盘 60 GB, 流量 3072 GB / 月)"},
	{ID: "medium_3_0", Name: "medium (2 vCPUs, 内存 4 GB, 硬盘 80 GB, 流量 4096 GB / 月)"},
	{ID: "large_3_0", Name: "large (2 vCPUs, 内存 8 GB, 硬盘 160 GB, 流量 5120 GB / 月)"},
}

var ipTypeOptions = []Option{
	{ID: "dualstack", Name: "双栈（IPv4+IPv6）"},
	{ID: "ipv6", Name: "仅 IPv6（IPv6-only）"},
	{ID: "ipv4", Name: "仅 IPv4"},
}

var ec2AMIOptions = []Option{
	{ID: "ubuntu-24.04", Name: "Ubuntu 24.04 LTS"},
	{ID: "ubuntu-22.04", Name: "Ubuntu 22.04 LTS"},
	{ID: "debian-12", Name: "Debian 12"},
	{ID: "amzn-2023", Name: "Amazon Linux 2023"},
	{ID: "custom", Name: "自定义 AMI ID"},
}

var ec2InstanceTypes = []Option{
	{ID: "t3.micro", Name: "t3.micro (2 vCPU, 1 GB)"},
	{ID: "t3.small", Name: "t3.small (2 vCPU, 2 GB)"},
	{ID: "t3.medium", Name: "t3.medium (2 vCPU, 4 GB)"},
	{ID: "t3.large", Name: "t3.large (2 vCPU, 8 GB)"},
	{ID: "t3.xlarge", Name: "t3.xlarge (4 vCPU, 16 GB)"},
	{ID: "t3a.micro", Name: "t3a.micro (2 vCPU, 1 GB)"},
	{ID: "t3a.small", Name: "t3a.small (2 vCPU, 2 GB)"},
	{ID: "t3a.medium", Name: "t3a.medium (2 vCPU, 4 GB)"},
	{ID: "t3a.large", Name: "t3a.large (2 vCPU, 8 GB)"},
	{ID: "t4g.micro", Name: "t4g.micro (2 vCPU, 1 GB, ARM)"},
	{ID: "t4g.small", Name: "t4g.small (2 vCPU, 2 GB, ARM)"},
	{ID: "t4g.medium", Name: "t4g.medium (2 vCPU, 4 GB, ARM)"},
	{ID: "t4g.large", Name: "t4g.large (2 vCPU, 8 GB, ARM)"},
	{ID: "m6i.large", Name: "m6i.large (2 vCPU, 8 GB)"},
	{ID: "m6i.xlarge", Name: "m6i.xlarge (4 vCPU, 16 GB)"},
	{ID: "m6g.large", Name: "m6g.large (2 vCPU, 8 GB, ARM)"},
	{ID: "m6g.xlarge", Name: "m6g.xlarge (4 vCPU, 16 GB, ARM)"},
	{ID: "c6i.large", Name: "c6i.large (2 vCPU, 4 GB)"},
	{ID: "c6i.xlarge", Name: "c6i.xlarge (4 vCPU, 8 GB)"},
	{ID: "c6g.large", Name: "c6g.large (2 vCPU, 4 GB, ARM)"},
	{ID: "c6g.xlarge", Name: "c6g.xlarge (4 vCPU, 8 GB, ARM)"},
	{ID: "r6i.large", Name: "r6i.large (2 vCPU, 16 GB)"},
	{ID: "r6i.xlarge", Name: "r6i.xlarge (4 vCPU, 32 GB)"},
	{ID: "r6g.large", Name: "r6g.large (2 vCPU, 16 GB, ARM)"},
	{ID: "r6g.xlarge", Name: "r6g.xlarge (4 vCPU, 32 GB, ARM)"},
	{ID: "custom", Name: "自定义实例类型"},
}

var ipv6BundleMap = map[string]string{
	"nano_3_0":   "nano_ipv6_3_0",
	"micro_3_0":  "micro_ipv6_3_0",
	"small_3_0":  "small_ipv6_3_0",
	"medium_3_0": "medium_ipv6_3_0",
	"large_3_0":  "large_ipv6_3_0",
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
	r.Static("/static", "./static")

	// templates
	tmpl := template.Must(template.New("").Funcs(template.FuncMap{
		"regionLabel": regionLabel,
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

	r.GET("/login", func(c *gin.Context) {
		if isLoggedIn(session.Must(c)) {
			c.Redirect(http.StatusFound, "/")
			return
		}
		s := session.Must(c)
		userCount, _ := appStore.CountUsers(c.Request.Context())
		regOpen, _ := appStore.RegistrationOpen(c.Request.Context())
		data := PageData{
			Title:            "AutoSail",
			CSRFToken:        s.GetString("csrf_token", ""),
			RegistrationOpen: regOpen,
		}
		if userCount == 0 {
			data.Flash.Info = "首次使用：请直接输入要创建的管理员账号和密码登录。"
		}
		switch c.Query("msg") {
		case "bad":
			data.Flash.Error = "用户名或密码错误"
		case "init_invalid":
			data.Flash.Error = "首次初始化失败：用户名和密码不能为空"
		case "init_failed":
			data.Flash.Error = "首次初始化失败，请重试"
		case "init_ok":
			data.Flash.Success = "已创建管理员账号并登录"
		case "csrf":
			data.Flash.Error = "请求已失效，请重试"
		case "logout":
			data.ToastInfo = "已退出登录"
		case "regclosed":
			data.Flash.Warn = "注册已关闭，请联系管理员"
		case "registered":
			data.Flash.Success = "注册成功，请登录"
		}
		c.HTML(http.StatusOK, "login", data)
	})

	r.POST("/login", func(c *gin.Context) {
		username := strings.TrimSpace(c.PostForm("username"))
		password := strings.TrimSpace(c.PostForm("password"))

		userCount, err := appStore.CountUsers(c.Request.Context())
		if err != nil {
			c.Redirect(http.StatusFound, "/login?msg=bad")
			return
		}
		if userCount == 0 {
			if username == "" || password == "" {
				c.Redirect(http.StatusFound, "/login?msg=init_invalid")
				return
			}
			if _, err := appStore.EnsureUser(username, password); err != nil {
				c.Redirect(http.StatusFound, "/login?msg=init_failed")
				return
			}
		}

		user, err := appStore.AuthenticateUser(c.Request.Context(), username, password)
		if err != nil {
			c.Redirect(http.StatusFound, "/login?msg=bad")
			return
		}
		s := session.Must(c)
		s.SetString("user_id", strconv.FormatInt(user.ID, 10))
		s.SetString("username", user.Username)
		if user.IsAdmin {
			s.SetString("is_admin", "1")
		} else {
			s.SetString("is_admin", "0")
		}
		if userCount == 0 {
			c.Redirect(http.StatusFound, "/?msg=init_ok")
			return
		}
		c.Redirect(http.StatusFound, "/")
	})

	r.GET("/register", func(c *gin.Context) {
		if isLoggedIn(session.Must(c)) {
			c.Redirect(http.StatusFound, "/")
			return
		}
		regOpen, _ := appStore.RegistrationOpen(c.Request.Context())
		if !regOpen {
			c.Redirect(http.StatusFound, "/login?msg=regclosed")
			return
		}
		s := session.Must(c)
		captchaQuestion, captchaAnswer := genRegistrationCaptcha()
		s.SetString("reg_captcha_answer", captchaAnswer)
		data := PageData{
			Title:               "AutoSail",
			CSRFToken:           s.GetString("csrf_token", ""),
			RegistrationOpen:    regOpen,
			RegistrationCaptcha: captchaQuestion,
		}
		switch c.Query("msg") {
		case "exists":
			data.Flash.Error = "用户名已存在，请更换"
		case "invalid":
			data.Flash.Error = "用户名和密码不能为空"
		case "captcha":
			data.Flash.Error = "验证码错误，请重试"
		case "failed":
			data.Flash.Error = "注册失败，请稍后重试"
		}
		c.HTML(http.StatusOK, "register", data)
	})

	r.POST("/register", func(c *gin.Context) {
		regOpen, _ := appStore.RegistrationOpen(c.Request.Context())
		if !regOpen {
			c.Redirect(http.StatusFound, "/login?msg=regclosed")
			return
		}
		s := session.Must(c)
		captchaAnswer := strings.TrimSpace(c.PostForm("captcha"))
		expectedCaptcha := strings.TrimSpace(s.GetString("reg_captcha_answer", ""))
		if expectedCaptcha == "" || captchaAnswer != expectedCaptcha {
			c.Redirect(http.StatusFound, "/register?msg=captcha")
			return
		}
		username := strings.TrimSpace(c.PostForm("username"))
		password := strings.TrimSpace(c.PostForm("password"))
		_, err := appStore.CreateUser(c.Request.Context(), username, password)
		if err != nil {
			if strings.Contains(err.Error(), "required") {
				c.Redirect(http.StatusFound, "/register?msg=invalid")
				return
			}
			if strings.Contains(err.Error(), "exists") {
				c.Redirect(http.StatusFound, "/register?msg=exists")
				return
			}
			c.Redirect(http.StatusFound, "/register?msg=failed")
			return
		}
		s.SetString("reg_captcha_answer", "")
		c.Redirect(http.StatusFound, "/login?msg=registered")
	})

	r.POST("/logout", func(c *gin.Context) {
		s := session.Must(c)
		s.SetString("user_id", "")
		s.SetString("username", "")
		s.SetString("is_admin", "")
		s.SetString("key_id", "")
		s.SetString("pending_key_id", "")
		c.Redirect(http.StatusFound, "/login?msg=logout")
	})

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

	r.GET("/", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)
		isAdmin := isAdminSession(s)
		regOpen, _ := appStore.RegistrationOpen(c.Request.Context())

		tab := c.Query("tab")
		if tab == "" {
			tab = s.GetString("tab", "create")
		} else {
			s.SetString("tab", tab)
		}
		s.SetString("last_tab", tab)

		serviceQuery := strings.TrimSpace(c.Query("service"))
		switch tab {
		case "create":
			if serviceQuery != "" {
				s.SetString("create_service", serviceQuery)
			}
		case "manage":
			if serviceQuery != "" {
				s.SetString("manage_service", serviceQuery)
			}
		}

		region := normalizeRegion(c.Query("region"))
		if region == "" {
			region = normalizeRegion(s.GetString("region", "us-east-1"))
		}
		// 确保 session 一直是规范 region
		s.SetString("region", region)

		az := c.Query("az")
		if az == "" {
			az = s.GetString("az", "a")
		} else {
			s.SetString("az", az)
		}

		username := s.GetString("username", "")
		keys, _ := appStore.ListKeys(c.Request.Context(), userID)
		activeKey, _ := resolveActiveKey(s, keys)
		pendingKeyID := resolvePendingKeyID(s, keys, activeKey)
		pendingKey := findKeyByID(keys, pendingKeyID)
		formKey := pendingKey
		if formKey == nil {
			formKey = activeKey
		}

		// Create UI defaults (persist in session)
		createIPType := s.GetString("create_ip_type", "dualstack")
		createBlueprint := s.GetString("create_blueprint", "ubuntu_24_04")
		createBundle := s.GetString("create_bundle", "nano_3_0")
		createFW := s.GetString("create_fw_all", "1") == "1"
		createService := s.GetString("create_service", "lightsail")
		manageService := s.GetString("manage_service", "lightsail")
		createEC2AMI := s.GetString("create_ec2_ami", "ubuntu-22.04")
		createEC2Type := s.GetString("create_ec2_type", "t3.micro")
		createEC2IPv6 := s.GetString("create_ec2_ipv6", "0") == "1"
		createBundleName := findOptionName(bundleOptions, createBundle)
		createRegions := regionOptionsForService(createService)
		manageRegions := regionOptionsForService(manageService)

		activeAK := strings.TrimSpace(keyAccessKey(activeKey))
		activeProxy := strings.TrimSpace(keyProxy(activeKey))
		activeHasCreds := activeKey != nil && activeAK != "" && strings.TrimSpace(activeKey.SecretKey) != ""

		data := PageData{
			Title:            "AutoSail",
			CSRFToken:        s.GetString("csrf_token", ""),
			Username:         username,
			IsAdmin:          isAdmin,
			RegistrationOpen: regOpen,
			CurrentUserID:    userID,
			HasCreds:         activeHasCreds,
			KeyName:          keyName(formKey),
			AK:               keyAccessKey(formKey),
			Proxy:            keyProxy(formKey),
			Keys:             keys,
			ActiveKeyID:      keyID(activeKey),
			ActiveKey:        keyName(activeKey),
			PendingKey:       pendingKeyID,
			Region:           region,
			CreateRegions:    createRegions,
			ManageRegions:    manageRegions,
			QuotaRegions:     ec2RegionOptions,
			AZ:               az,
			Tab:              tab,
			CreateIPType:     createIPType,
			CreateBlueprint:  createBlueprint,
			CreateBundle:     createBundle,
			CreateEnableFW:   createFW,
			CreateService:    createService,
			CreateEC2AMI:     createEC2AMI,
			CreateEC2Type:    createEC2Type,
			CreateEC2IPv6:    createEC2IPv6,
			CreateBundleName: createBundleName,
			Blueprints:       blueprintOptions,
			Bundles:          bundleOptions,
			IPTypes:          ipTypeOptions,
			EC2AMIs:          ec2AMIOptions,
			EC2Types:         ec2InstanceTypes,
			ManageService:    manageService,
		}
		if isAdmin {
			users, err := appStore.ListUsers(c.Request.Context())
			if err == nil {
				data.Users = users
			}
		}

		data.QuotaRegion = "us-east-1"
		if activeKey != nil {
			if strings.TrimSpace(activeKey.QuotaRegion) != "" {
				data.QuotaRegion = activeKey.QuotaRegion
			}
			data.QuotaOn = activeKey.QuotaOn
			data.QuotaSpot = activeKey.QuotaSpot
			data.QuotaOnName = activeKey.QuotaOnName
			data.QuotaSpName = activeKey.QuotaSpName
		} else {
			data.QuotaRegion = s.GetString("quota_region", "us-east-1")
		}

		switch c.Query("msg") {
		case "cleared":
			data.Flash.Success = "已删除当前密钥"
		case "saved":
			data.Flash.Success = "已保存（已新增密钥）"
		case "activated":
			data.Flash.Success = "已启用选中的密钥"
		case "updated":
			data.Flash.Success = "已更新选中的密钥"
		case "csrf":
			data.Flash.Error = "请求已失效，请刷新页面后重试"
		case "needuse":
			data.Flash.Warn = "请先选择密钥并点击“使用此密钥”"
		case "needkey":
			data.Flash.Warn = "Access Key / Secret Key 不能为空"
		case "needids":
			data.Flash.Warn = "Blueprint / Bundle 不能为空"
		case "err_client":
			data.Flash.Error = "AWS 客户端初始化失败"
		case "created":
			data.Flash.Success = "✅ 创建请求已提交（稍等 1-2 分钟后去『管理』查看）"
		case "create_failed":
			errMsg := strings.TrimSpace(c.Query("err"))
			if errMsg != "" {
				if strings.Contains(errMsg, "已创建") {
					data.Flash.Warn = errMsg
				} else {
					data.Flash.Error = "创建失败：" + errMsg
				}
			} else {
				data.Flash.Error = "创建失败：请查看服务器日志/检查权限/区域是否可用"
			}
		case "quota_ok":
			data.Flash.Success = "✅ 配额测试完成"
		case "quota_err":
			data.Flash.Error = "配额测试失败：未找到配额项或没有 Service Quotas 权限"
		case "reboot_ok":
			data.Flash.Success = "已提交重启"
		case "reboot_failed":
			data.Flash.Error = "重启失败（详情看日志）"
		case "openall_ok":
			data.Flash.Success = "已提交全端口开放"
		case "openall_failed":
			data.Flash.Error = "全端口开放失败（详情看日志）"
		case "start_ok":
			data.Flash.Success = "已提交启动"
		case "start_failed":
			data.Flash.Error = "启动失败（详情看日志）"
		case "stop_ok":
			data.Flash.Success = "已提交停止"
		case "stop_failed":
			data.Flash.Error = "停止失败（详情看日志）"
		case "terminate_ok":
			data.Flash.Success = "已提交终止"
		case "terminate_failed":
			data.Flash.Error = "终止失败（详情看日志）"
		case "delete_ok":
			data.Flash.Success = "已提交删除（如有静态 IP 已尝试释放）"
		case "delete_failed":
			errMsg := strings.TrimSpace(c.Query("err"))
			if errMsg != "" {
				data.Flash.Error = "删除失败：" + errMsg
			} else {
				data.Flash.Error = "删除失败（详情看日志）"
			}
		case "swapip_ok":
			data.Flash.Success = "✅ 换 IP 请求已提交"
		case "swapip_failed":
			errMsg := strings.TrimSpace(c.Query("err"))
			switch {
			case errMsg == "":
				data.Flash.Error = "换 IP 失败（详情看日志）"
			case strings.Contains(errMsg, "可能已成功"):
				data.Flash.Warn = errMsg
			default:
				data.Flash.Error = "换 IP 失败：" + errMsg
			}
		}

		// manage list
		if tab == "manage" && activeHasCreds {
			if manageService == "ec2" {
				key := strings.Join([]string{"ec2inst", region, activeAK, activeProxy}, "|")
				if v, ok := instCache.Get(key); ok {
					data.EC2Instances = v.([]aws.EC2InstanceView)
				} else {
					cli, err := aws.NewEC2Client(c.Request.Context(), region, activeAK, activeKey.SecretKey, activeProxy)
					if err != nil {
						data.Flash.Error = "创建 EC2 client 失败：" + err.Error()
					} else {
						list, err := aws.ListEC2Instances(c.Request.Context(), cli)
						if err != nil {
							data.Flash.Error = "拉取 EC2 实例失败：" + err.Error()
						} else {
							data.EC2Instances = list
							instCache.Set(key, list, cache.DefaultExpiration)
						}
					}
				}
			} else {
				key := strings.Join([]string{"inst", region, activeAK, activeProxy}, "|")
				if v, ok := instCache.Get(key); ok {
					data.Instances = v.([]aws.InstanceView)
				} else {
					cli, err := aws.NewLightsailClient(c.Request.Context(), region, activeAK, activeKey.SecretKey, activeProxy)
					if err != nil {
						data.Flash.Error = "创建 Lightsail client 失败：" + err.Error()
					} else {
						list, err := aws.ListInstances(c.Request.Context(), cli)
						if err != nil {
							data.Flash.Error = "拉取实例失败：" + err.Error()
						} else {
							data.Instances = list
							instCache.Set(key, list, cache.DefaultExpiration)
						}
					}
				}
			}
		} else if tab == "manage" && !activeHasCreds {
			data.Flash.Warn = "请先启用一个有效密钥再查看实例列表"
		}

		c.HTML(http.StatusOK, "layout", data)
	})

	r.POST("/admin/registration", func(c *gin.Context) {
		s := session.Must(c)
		if !isAdminSession(s) {
			c.Redirect(http.StatusFound, "/")
			return
		}
		open := strings.TrimSpace(c.PostForm("open")) == "1"
		_ = appStore.SetRegistrationOpen(c.Request.Context(), open)
		c.Redirect(http.StatusFound, "/")
	})

	r.POST("/admin/users/delete", func(c *gin.Context) {
		s := session.Must(c)
		if !isAdminSession(s) {
			c.Redirect(http.StatusFound, "/")
			return
		}
		currentUserID, _ := userIDFromSession(s)
		idStr := strings.TrimSpace(c.PostForm("user_id"))
		if idStr == "" {
			c.Redirect(http.StatusFound, "/")
			return
		}
		userID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || userID == 0 || userID == currentUserID {
			c.Redirect(http.StatusFound, "/")
			return
		}
		users, err := appStore.ListUsers(c.Request.Context())
		if err == nil {
			for _, u := range users {
				if u.ID == userID && u.IsAdmin {
					adminCount, err := appStore.CountAdmins(c.Request.Context())
					if err != nil || adminCount <= 1 {
						c.Redirect(http.StatusFound, "/")
						return
					}
					break
				}
			}
		}
		_ = appStore.DeleteUser(c.Request.Context(), userID)
		c.Redirect(http.StatusFound, "/")
	})

	// Save creds (留空不覆盖)
	r.POST("/auth/save", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)

		mode := strings.TrimSpace(c.PostForm("mode"))
		keyIDStr := strings.TrimSpace(c.PostForm("key_id"))
		ak := strings.TrimSpace(c.PostForm("ak"))
		sk := strings.TrimSpace(c.PostForm("sk"))
		proxy := strings.TrimSpace(c.PostForm("proxy"))

		if mode == "update" && keyIDStr != "" {
			if keyID, err := strconv.ParseInt(keyIDStr, 10, 64); err == nil && keyID > 0 {
				keys, _ := appStore.ListKeys(c.Request.Context(), userID)
				if existing := findKeyByID(keys, keyID); existing != nil {
					keyName := strings.TrimSpace(c.PostForm("key_name"))
					if keyName == "" {
						keyName = existing.Name
					}
					if ak == "" {
						ak = existing.AccessKey
					}
					if sk == "" {
						sk = existing.SecretKey
					}
					if ak == "" || sk == "" {
						c.Redirect(http.StatusFound, "/?msg=needkey")
						return
					}
					if err := appStore.UpdateKey(c.Request.Context(), userID, keyID, keyName, ak, sk, proxy); err == nil {
						s.SetString("pending_key_id", strconv.FormatInt(keyID, 10))
						c.Redirect(http.StatusFound, "/?msg=updated")
						return
					}
				}
			}
		}

		keyName := strings.TrimSpace(c.PostForm("key_name"))
		if keyName == "" {
			keyName = time.Now().Format("2006-01-02 15:04")
		}
		if ak == "" || sk == "" {
			c.Redirect(http.StatusFound, "/?msg=needkey")
			return
		}
		keyID, err := appStore.CreateKey(c.Request.Context(), userID, keyName, ak, sk, proxy)
		if err == nil {
			s.SetString("pending_key_id", strconv.FormatInt(keyID, 10))
		}
		c.Redirect(http.StatusFound, "/?msg=saved")
	})

	// Select key
	r.POST("/auth/select", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)
		keyID := strings.TrimSpace(c.PostForm("key_id"))
		if keyID != "" {
			keys, _ := appStore.ListKeys(c.Request.Context(), userID)
			if parsedID, err := strconv.ParseInt(keyID, 10, 64); err == nil {
				if keyExists(keys, parsedID) {
					s.SetString("pending_key_id", keyID)
					s.SetString("key_id", keyID)
					c.Redirect(http.StatusFound, "/?msg=activated")
					return
				}
			}
		}
		c.Redirect(http.StatusFound, "/")
	})

	// Activate selected key
	r.POST("/auth/activate", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)
		keys, _ := appStore.ListKeys(c.Request.Context(), userID)
		keyID := strings.TrimSpace(c.PostForm("key_id"))
		if keyID == "" {
			keyID = strings.TrimSpace(s.GetString("pending_key_id", ""))
		}
		if keyID != "" {
			if parsedID, err := strconv.ParseInt(keyID, 10, 64); err == nil {
				if keyExists(keys, parsedID) {
					s.SetString("key_id", keyID)
					s.SetString("pending_key_id", keyID)
				}
			}
		}
		c.Redirect(http.StatusFound, "/?msg=activated")
	})

	// Delete current key
	r.POST("/auth/delete", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)
		keyIDStr := strings.TrimSpace(c.PostForm("key_id"))
		if keyIDStr == "" {
			keyIDStr = strings.TrimSpace(s.GetString("key_id", ""))
		}
		var deletedKeyID int64
		if keyIDStr != "" {
			if keyID, err := strconv.ParseInt(keyIDStr, 10, 64); err == nil && keyID > 0 {
				_ = appStore.DeleteKey(c.Request.Context(), userID, keyID)
				deletedKeyID = keyID
			}
		}
		keys, _ := appStore.ListKeys(c.Request.Context(), userID)
		activeKeyID, _ := strconv.ParseInt(strings.TrimSpace(s.GetString("key_id", "")), 10, 64)
		if activeKeyID == deletedKeyID || !keyExists(keys, activeKeyID) {
			activeKeyID = 0
		}
		if activeKeyID == 0 && len(keys) > 0 {
			activeKeyID = keys[0].ID
		}
		if activeKeyID > 0 {
			activeKeyIDStr := strconv.FormatInt(activeKeyID, 10)
			s.SetString("key_id", activeKeyIDStr)
			s.SetString("pending_key_id", activeKeyIDStr)
		} else {
			s.SetString("key_id", "")
			s.SetString("pending_key_id", "")
		}
		c.Redirect(http.StatusFound, "/?msg=cleared")
	})

	// Reorder keys (drag & drop)
	r.POST("/auth/reorder", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)
		keys, _ := appStore.ListKeys(c.Request.Context(), userID)
		owned := make(map[int64]struct{}, len(keys))
		for _, k := range keys {
			owned[k.ID] = struct{}{}
		}
		var ids []int64
		seen := make(map[int64]struct{})
		for _, raw := range c.PostFormArray("order") {
			for _, part := range strings.Split(raw, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				id, err := strconv.ParseInt(part, 10, 64)
				if err != nil {
					continue
				}
				if _, ok := owned[id]; !ok {
					continue
				}
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			_ = appStore.UpdateKeysOrder(c.Request.Context(), userID, ids)
		}
		c.Redirect(http.StatusFound, "/")
	})

	// Proxy exit IP check (uses current session proxy)
	r.GET("/proxy/check", func(c *gin.Context) {
		s := session.Must(c)
		proxy := strings.TrimSpace(c.Query("proxy"))
		if proxy == "" {
			userID, _ := userIDFromSession(s)
			keys, _ := appStore.ListKeys(c.Request.Context(), userID)
			activeKey, _ := resolveActiveKey(s, keys)
			proxy = keyProxy(activeKey)
		}
		ip, asn, err := aws.CheckProxyExitIP(c.Request.Context(), proxy)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "ip": ip, "as": asn})
	})

	// Create instance
	r.POST("/aws/create", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)
		keys, _ := appStore.ListKeys(c.Request.Context(), userID)
		activeKey, _ := resolveActiveKey(s, keys)
		if activeKey == nil || strings.TrimSpace(activeKey.AccessKey) == "" || strings.TrimSpace(activeKey.SecretKey) == "" {
			c.Redirect(http.StatusFound, "/?tab=create&msg=needuse")
			return
		}
		ak := strings.TrimSpace(activeKey.AccessKey)
		sk := strings.TrimSpace(activeKey.SecretKey)
		proxy := strings.TrimSpace(activeKey.Proxy)

		service := strings.TrimSpace(c.PostForm("service"))
		if service == "" {
			service = "lightsail"
		}
		s.SetString("create_service", service)

		region := normalizeRegion(strings.TrimSpace(c.PostForm("region")))
		az := strings.TrimSpace(c.PostForm("az"))
		if region == "" {
			region = "us-east-1"
		}
		if az == "" {
			az = "a"
		}
		s.SetString("region", region)
		s.SetString("az", az)

		if service == "ec2" {
			amiChoice := strings.TrimSpace(c.PostForm("ec2_ami"))
			instanceType := strings.TrimSpace(c.PostForm("ec2_type"))
			instanceTypeCustom := strings.TrimSpace(c.PostForm("ec2_type_custom"))
			enableIPv6 := strings.TrimSpace(c.PostForm("ec2_ipv6")) == "1"
			countStr := strings.TrimSpace(c.PostForm("ec2_count"))
			rootPwd := strings.TrimSpace(c.PostForm("root_pwd"))
			if amiChoice != "" {
				s.SetString("create_ec2_ami", amiChoice)
			}
			if instanceType != "" {
				s.SetString("create_ec2_type", instanceType)
			}
			if enableIPv6 {
				s.SetString("create_ec2_ipv6", "1")
			} else {
				s.SetString("create_ec2_ipv6", "0")
			}

			count := int32(1)
			if countStr != "" {
				if parsed, err := strconv.Atoi(countStr); err == nil && parsed > 0 {
					count = int32(parsed)
				}
			}

			cli, err := aws.NewEC2Client(c.Request.Context(), region, ak, sk, proxy)
			if err != nil {
				c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=err_client&service=ec2")
				return
			}

			if instanceType == "custom" {
				instanceType = instanceTypeCustom
			}

			amiID, err := aws.ResolveEC2AMI(c.Request.Context(), cli, amiChoice)
			if err != nil {
				errMsg := formatFlashError(err)
				if errMsg != "" {
					c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=create_failed&service=ec2&err="+url.QueryEscape(errMsg))
				} else {
					c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=create_failed&service=ec2")
				}
				return
			}

			userData := ""
			if rootPwd != "" {
				userData = aws.BuildRootPasswordUserData(rootPwd)
			}

			ec2Name := strings.TrimSpace(c.PostForm("instance_name"))
			if ec2Name == "" {
				ec2Name = "ec2-" + strconv.FormatInt(time.Now().Unix(), 10)
			}
			err = aws.CreateEC2Instance(c.Request.Context(), cli, aws.CreateEC2InstanceInput{
				Name:         ec2Name,
				AMI:          amiID,
				InstanceType: instanceType,
				Count:        count,
				UserData:     userData,
				EnableIPv6:   enableIPv6,
			})
			if err != nil {
				errMsg := formatFlashError(err)
				if errMsg != "" {
					c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=create_failed&service=ec2&err="+url.QueryEscape(errMsg))
				} else {
					c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=create_failed&service=ec2")
				}
				return
			}

			key := strings.Join([]string{"ec2inst", region, ak, proxy}, "|")
			instCache.Delete(key)

			c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&msg=created&service=ec2")
			return
		}

		ipType := strings.TrimSpace(c.PostForm("ip_type"))
		if ipType == "" {
			ipType = "dualstack"
		}
		s.SetString("create_ip_type", ipType)
		enableFW := c.PostForm("enable_fw") == "1"
		if enableFW {
			s.SetString("create_fw_all", "1")
		} else {
			s.SetString("create_fw_all", "0")
		}

		blueprint := strings.TrimSpace(c.PostForm("blueprint_id"))
		bundle := strings.TrimSpace(c.PostForm("bundle_id"))
		if blueprint != "" {
			s.SetString("create_blueprint", blueprint)
		}
		if bundle != "" {
			s.SetString("create_bundle", bundle)
		}
		rootPwd := strings.TrimSpace(c.PostForm("root_pwd"))

		if rootPwd == "" {
			c.Redirect(http.StatusFound, "/?tab=create&region="+region)
			return
		}
		if blueprint == "" || bundle == "" {
			c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=needids")
			return
		}

		instanceName := strings.TrimSpace(c.PostForm("instance_name"))
		if instanceName == "" {
			instanceName = "vps-" + strconv.FormatInt(time.Now().Unix(), 10)
		}
		userData := aws.BuildRootPasswordUserData(rootPwd)

		// If ipv6-only, use ipv6 bundle encoding (Lightsail real bundle id)
		bundleToUse := bundle
		if ipType == "ipv6" {
			if v, ok := ipv6BundleMap[bundle]; ok {
				bundleToUse = v
			}
		}

		cli, err := aws.NewLightsailClient(c.Request.Context(), region, ak, sk, proxy)
		if err != nil {
			c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=err_client")
			return
		}

		availabilityZone := region + az

		err = aws.CreateInstance(c.Request.Context(), cli, aws.CreateInstanceInput{
			InstanceName:     instanceName,
			AvailabilityZone: availabilityZone,
			BlueprintID:      blueprint,
			BundleID:         bundleToUse,
			UserData:         userData,
			IPAddressType:    ipType,
			EnableFWAll:      enableFW,
		})
		if err != nil {
			errMsg := formatFlashError(err)
			if errMsg != "" {
				c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=create_failed&err="+url.QueryEscape(errMsg))
			} else {
				c.Redirect(http.StatusFound, "/?tab=create&region="+region+"&msg=create_failed")
			}
			return
		}

		// invalidate list cache
		key := strings.Join([]string{"inst", region, ak, proxy}, "|")
		instCache.Delete(key)

		c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&msg=created")
	})

	// Manage actions
	r.POST("/aws/reboot", func(c *gin.Context) {
		doManageAction(c, "reboot", func(ctx *gin.Context, cli aws.LightsailAPI, name string) error {
			return aws.RebootInstance(ctx.Request.Context(), cli, name)
		})
	})

	r.POST("/aws/openall", func(c *gin.Context) {
		doManageAction(c, "openall", func(ctx *gin.Context, cli aws.LightsailAPI, name string) error {
			return aws.OpenAllPorts(ctx.Request.Context(), cli, name)
		})
	})

	// Clear manage list cache (per region+ak+proxy)
	r.POST("/aws/refresh", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)
		keys, _ := appStore.ListKeys(c.Request.Context(), userID)
		activeKey, _ := resolveActiveKey(s, keys)
		if activeKey == nil {
			c.Redirect(http.StatusFound, "/?tab=manage&msg=needuse")
			return
		}
		ak := strings.TrimSpace(activeKey.AccessKey)
		proxy := strings.TrimSpace(activeKey.Proxy)
		region := normalizeRegion(strings.TrimSpace(c.PostForm("region")))
		if region == "" {
			region = normalizeRegion(s.GetString("region", "us-east-1"))
		}
		service := strings.TrimSpace(c.PostForm("service"))
		if service == "" {
			service = s.GetString("manage_service", "lightsail")
		}
		keyPrefix := "inst"
		if service == "ec2" {
			keyPrefix = "ec2inst"
		}
		key := strings.Join([]string{keyPrefix, region, ak, proxy}, "|")
		instCache.Delete(key)
		c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&service="+service)
	})

	r.POST("/aws/delete", func(c *gin.Context) {
		doManageAction(c, "delete", func(ctx *gin.Context, cli aws.LightsailAPI, name string) error {
			return aws.DeleteInstanceWithStaticIPCleanup(ctx.Request.Context(), cli, name)
		})
	})

	r.POST("/aws/swapip", func(c *gin.Context) {
		doManageAction(c, "swapip", func(ctx *gin.Context, cli aws.LightsailAPI, name string) error {
			return aws.SwapStaticIPForInstance(ctx.Request.Context(), cli, name)
		})
	})

	r.POST("/aws/ec2/start", func(c *gin.Context) {
		doManageActionEC2(c, "start", func(ctx *gin.Context, cli *ec2.Client, id string) error {
			return aws.StartEC2Instance(ctx.Request.Context(), cli, id)
		})
	})

	r.POST("/aws/ec2/stop", func(c *gin.Context) {
		doManageActionEC2(c, "stop", func(ctx *gin.Context, cli *ec2.Client, id string) error {
			return aws.StopEC2Instance(ctx.Request.Context(), cli, id)
		})
	})

	r.POST("/aws/ec2/reboot", func(c *gin.Context) {
		doManageActionEC2(c, "reboot", func(ctx *gin.Context, cli *ec2.Client, id string) error {
			return aws.RebootEC2Instance(ctx.Request.Context(), cli, id)
		})
	})

	r.POST("/aws/ec2/terminate", func(c *gin.Context) {
		doManageActionEC2(c, "terminate", func(ctx *gin.Context, cli *ec2.Client, id string) error {
			return aws.TerminateEC2Instance(ctx.Request.Context(), cli, id)
		})
	})

	r.POST("/aws/ec2/openall", func(c *gin.Context) {
		doManageActionEC2(c, "openall", func(ctx *gin.Context, cli *ec2.Client, id string) error {
			return aws.OpenAllEC2Ports(ctx.Request.Context(), cli, id)
		})
	})

	// Quota test
	r.POST("/aws/quota", func(c *gin.Context) {
		s := session.Must(c)
		userID, _ := userIDFromSession(s)
		keys, _ := appStore.ListKeys(c.Request.Context(), userID)
		activeKey, _ := resolveActiveKey(s, keys)
		if activeKey == nil || strings.TrimSpace(activeKey.AccessKey) == "" || strings.TrimSpace(activeKey.SecretKey) == "" {
			lastTab := s.GetString("last_tab", "create")
			c.Redirect(http.StatusFound, "/?tab="+lastTab+"&msg=needuse")
			return
		}
		ak := strings.TrimSpace(activeKey.AccessKey)
		sk := strings.TrimSpace(activeKey.SecretKey)
		proxy := strings.TrimSpace(activeKey.Proxy)

		region := normalizeRegion(strings.TrimSpace(c.PostForm("quota_region")))
		if region == "" {
			region = normalizeRegion(activeKey.QuotaRegion)
		}
		if region == "" {
			region = normalizeRegion(s.GetString("quota_region", "us-east-1"))
		}
		s.SetString("quota_region", region)

		sq, err := aws.NewServiceQuotasClient(c.Request.Context(), region, ak, sk, proxy)
		if err != nil {
			s.SetString("quota_on", "")
			s.SetString("quota_spot", "")
			lastTab := s.GetString("last_tab", "create")
			c.Redirect(http.StatusFound, "/?tab="+lastTab+"&msg=quota_err")
			return
		}

		onVal, spotVal, onName, spotName, err := aws.TestVCPUQuotas(c.Request.Context(), sq)
		if err != nil || (strings.TrimSpace(onVal) == "" && strings.TrimSpace(spotVal) == "") {
			s.SetString("quota_on", "")
			s.SetString("quota_spot", "")
			s.SetString("quota_on_name", "")
			s.SetString("quota_sp_name", "")
			lastTab := s.GetString("last_tab", "create")
			c.Redirect(http.StatusFound, "/?tab="+lastTab+"&msg=quota_err")
			return
		}

		s.SetString("quota_on", onVal)
		s.SetString("quota_spot", spotVal)
		s.SetString("quota_on_name", onName)
		s.SetString("quota_sp_name", spotName)
		if err := appStore.UpdateKeyQuota(c.Request.Context(), userID, activeKey.ID, region, onVal, spotVal, onName, spotName); err != nil {
			lastTab := s.GetString("last_tab", "create")
			c.Redirect(http.StatusFound, "/?tab="+lastTab+"&msg=quota_err")
			return
		}

		lastTab := s.GetString("last_tab", "create")
		c.Redirect(http.StatusFound, "/?tab="+lastTab+"&msg=quota_ok")
	})

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

func doManageAction(c *gin.Context, action string, fn func(ctx *gin.Context, cli aws.LightsailAPI, name string) error) {
	s := session.Must(c)
	userID, _ := userIDFromSession(s)
	keys, _ := appStore.ListKeys(c.Request.Context(), userID)
	activeKey, _ := resolveActiveKey(s, keys)
	if activeKey == nil {
		c.Redirect(http.StatusFound, "/?tab=manage&msg=needuse")
		return
	}
	ak := strings.TrimSpace(activeKey.AccessKey)
	sk := strings.TrimSpace(activeKey.SecretKey)
	proxy := strings.TrimSpace(activeKey.Proxy)

	region := normalizeRegion(strings.TrimSpace(c.PostForm("region")))
	if region == "" {
		region = normalizeRegion(s.GetString("region", "us-east-1"))
	}
	name := strings.TrimSpace(c.PostForm("instance"))
	if name == "" {
		c.Redirect(http.StatusFound, "/?tab=manage&region="+region)
		return
	}
	if ak == "" || sk == "" {
		c.Redirect(http.StatusFound, "/?tab=manage&region="+region)
		return
	}

	cli, err := aws.NewLightsailClient(c.Request.Context(), region, ak, sk, proxy)
	if err != nil {
		c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&msg=err_client")
		return
	}

	if err := fn(c, cli, name); err != nil {
		reason := formatFlashError(err)
		log.Printf("[manage] action=%s region=%q instance=%q 失败: %v", action, region, name, err)
		target := "/?tab=manage&region=" + region + "&msg=" + action + "_failed"
		if reason != "" {
			target += "&err=" + url.QueryEscape(reason)
		}
		c.Redirect(http.StatusFound, target)
		return
	}

	// invalidate cache
	key := strings.Join([]string{"inst", region, ak, proxy}, "|")
	instCache.Delete(key)

	c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&msg="+action+"_ok")
}

func doManageActionEC2(c *gin.Context, action string, fn func(ctx *gin.Context, cli *ec2.Client, id string) error) {
	s := session.Must(c)
	userID, _ := userIDFromSession(s)
	keys, _ := appStore.ListKeys(c.Request.Context(), userID)
	activeKey, _ := resolveActiveKey(s, keys)
	if activeKey == nil {
		c.Redirect(http.StatusFound, "/?tab=manage&msg=needuse&service=ec2")
		return
	}
	ak := strings.TrimSpace(activeKey.AccessKey)
	sk := strings.TrimSpace(activeKey.SecretKey)
	proxy := strings.TrimSpace(activeKey.Proxy)

	region := normalizeRegion(strings.TrimSpace(c.PostForm("region")))
	if region == "" {
		region = normalizeRegion(s.GetString("region", "us-east-1"))
	}
	id := strings.TrimSpace(c.PostForm("instance"))
	if id == "" {
		c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&service=ec2")
		return
	}
	if ak == "" || sk == "" {
		c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&service=ec2")
		return
	}

	cli, err := aws.NewEC2Client(c.Request.Context(), region, ak, sk, proxy)
	if err != nil {
		c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&msg=err_client&service=ec2")
		return
	}

	if err := fn(c, cli, id); err != nil {
		reason := formatFlashError(err)
		log.Printf("[manage-ec2] action=%s region=%q instance=%q 失败: %v", action, region, id, err)
		target := "/?tab=manage&region=" + region + "&msg=" + action + "_failed&service=ec2"
		if reason != "" {
			target += "&err=" + url.QueryEscape(reason)
		}
		c.Redirect(http.StatusFound, target)
		return
	}

	key := strings.Join([]string{"ec2inst", region, ak, proxy}, "|")
	instCache.Delete(key)

	c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&msg="+action+"_ok&service=ec2")
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
