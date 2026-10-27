package main

import (
	"net/http"
	"strings"

	"autosail/internal/session"
	"github.com/gin-gonic/gin"
)

func handleIndex(c *gin.Context) {
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
	case "swapip_attached":
		data.Flash.Warn = "换 IP 可能已成功，请刷新确认"
	case "swapip_failed":
		errMsg := strings.TrimSpace(c.Query("err"))
		if errMsg != "" {
			data.Flash.Error = "换 IP 失败：" + errMsg
		} else {
			data.Flash.Error = "换 IP 失败（详情看日志）"
		}
	}


	c.HTML(http.StatusOK, "layout", data)
}
