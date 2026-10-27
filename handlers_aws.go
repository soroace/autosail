package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"autosail/internal/aws"
	"autosail/internal/session"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/gin-gonic/gin"
	"github.com/patrickmn/go-cache"
)

func handleProxyCheck(c *gin.Context) {
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
}

func handleBundles(c *gin.Context) {
	s := session.Must(c)
	userID, _ := userIDFromSession(s)
	keys, _ := appStore.ListKeys(c.Request.Context(), userID)
	activeKey, _ := resolveActiveKey(s, keys)
	if activeKey == nil || strings.TrimSpace(activeKey.AccessKey) == "" || strings.TrimSpace(activeKey.SecretKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "请先启用一个有效密钥"})
		return
	}
	region := normalizeRegion(strings.TrimSpace(c.Query("region")))
	if region == "" {
		region = "us-east-1"
	}
	cli, err := aws.NewLightsailClient(c.Request.Context(), region, activeKey.AccessKey, activeKey.SecretKey, activeKey.Proxy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": formatFlashError(err)})
		return
	}
	list, err := aws.ListBundles(c.Request.Context(), cli)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": formatFlashError(err)})
		return
	}
	// 过滤 Windows 套餐（本服务只建 Linux 实例）
	out := make([]aws.BundleView, 0, len(list))
	for _, b := range list {
		if b.Windows {
			continue
		}
		out = append(out, b)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "bundles": out})
}

func handleInstances(c *gin.Context) {
	s := session.Must(c)
	userID, _ := userIDFromSession(s)
	keys, _ := appStore.ListKeys(c.Request.Context(), userID)
	activeKey, _ := resolveActiveKey(s, keys)

	service := strings.TrimSpace(c.Query("service"))
	if service != "ec2" {
		service = "lightsail"
	}
	region := normalizeRegion(strings.TrimSpace(c.Query("region")))
	if region == "" {
		region = "us-east-1"
	}
	data := InstanceSectionData{
		CSRFToken:     s.GetString("csrf_token", ""),
		Region:        region,
		ManageService: service,
	}

	ak := strings.TrimSpace(keyAccessKey(activeKey))
	if activeKey == nil || ak == "" || strings.TrimSpace(activeKey.SecretKey) == "" {
		data.Error = "请先启用一个有效密钥再查看实例列表"
		c.HTML(http.StatusOK, "instanceSection", data)
		return
	}
	sk := strings.TrimSpace(activeKey.SecretKey)
	proxy := strings.TrimSpace(activeKey.Proxy)

	if service == "ec2" {
		key := strings.Join([]string{"ec2inst", region, ak, proxy}, "|")
		if v, ok := instCache.Get(key); ok {
			data.EC2Instances = v.([]aws.EC2InstanceView)
		} else {
			cli, err := aws.NewEC2Client(c.Request.Context(), region, ak, sk, proxy)
			if err != nil {
				data.Error = "创建 EC2 client 失败：" + formatFlashError(err)
			} else if list, err := aws.ListEC2Instances(c.Request.Context(), cli); err != nil {
				data.Error = "拉取 EC2 实例失败：" + formatFlashError(err)
			} else {
				data.EC2Instances = list
				instCache.Set(key, list, cache.DefaultExpiration)
			}
		}
	} else {
		key := strings.Join([]string{"inst", region, ak, proxy}, "|")
		if v, ok := instCache.Get(key); ok {
			data.Instances = v.([]aws.InstanceView)
		} else {
			cli, err := aws.NewLightsailClient(c.Request.Context(), region, ak, sk, proxy)
			if err != nil {
				data.Error = "创建 Lightsail client 失败：" + formatFlashError(err)
			} else if list, err := aws.ListInstances(c.Request.Context(), cli); err != nil {
				data.Error = "拉取实例失败：" + formatFlashError(err)
			} else {
				data.Instances = list
				instCache.Set(key, list, cache.DefaultExpiration)
			}
		}
	}
	c.HTML(http.StatusOK, "instanceSection", data)
}

func handleCreate(c *gin.Context) {
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
}

func handleReboot(c *gin.Context) {
	doManageAction(c, "reboot", func(ctx *gin.Context, cli aws.LightsailAPI, name string) error {
		return aws.RebootInstance(ctx.Request.Context(), cli, name)
	})
}

func handleOpenAll(c *gin.Context) {
	doManageAction(c, "openall", func(ctx *gin.Context, cli aws.LightsailAPI, name string) error {
		return aws.OpenAllPorts(ctx.Request.Context(), cli, name)
	})
}

func handleRefresh(c *gin.Context) {
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
}

func handleDelete(c *gin.Context) {
	doManageAction(c, "delete", func(ctx *gin.Context, cli aws.LightsailAPI, name string) error {
		return aws.DeleteInstanceWithStaticIPCleanup(ctx.Request.Context(), cli, name)
	})
}

func handleSwapIP(c *gin.Context) {
	doManageAction(c, "swapip", func(ctx *gin.Context, cli aws.LightsailAPI, name string) error {
		return aws.SwapStaticIPForInstance(ctx.Request.Context(), cli, name)
	})
}

func handleEC2Start(c *gin.Context) {
	doManageActionEC2(c, "start", func(ctx *gin.Context, cli *ec2.Client, id string) error {
		return aws.StartEC2Instance(ctx.Request.Context(), cli, id)
	})
}

func handleEC2Stop(c *gin.Context) {
	doManageActionEC2(c, "stop", func(ctx *gin.Context, cli *ec2.Client, id string) error {
		return aws.StopEC2Instance(ctx.Request.Context(), cli, id)
	})
}

func handleEC2Reboot(c *gin.Context) {
	doManageActionEC2(c, "reboot", func(ctx *gin.Context, cli *ec2.Client, id string) error {
		return aws.RebootEC2Instance(ctx.Request.Context(), cli, id)
	})
}

func handleEC2Terminate(c *gin.Context) {
	doManageActionEC2(c, "terminate", func(ctx *gin.Context, cli *ec2.Client, id string) error {
		return aws.TerminateEC2Instance(ctx.Request.Context(), cli, id)
	})
}

func handleEC2OpenAll(c *gin.Context) {
	doManageActionEC2(c, "openall", func(ctx *gin.Context, cli *ec2.Client, id string) error {
		return aws.OpenAllEC2Ports(ctx.Request.Context(), cli, id)
	})
}

func handleQuota(c *gin.Context) {
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

	cacheKey := strings.Join([]string{"inst", region, ak, proxy}, "|")

	if err := fn(c, cli, name); err != nil {
		log.Printf("[manage] action=%s region=%q instance=%q 失败: %v", action, region, name, err)
		// 换 IP 实际已成功（响应丢失）：按提示处理，不当作失败
		if errors.Is(err, aws.ErrNewIPAlreadyAttached) {
			instCache.Delete(cacheKey)
			c.Redirect(http.StatusFound, "/?tab=manage&region="+region+"&msg=swapip_attached")
			return
		}
		reason := formatFlashError(err)
		target := "/?tab=manage&region=" + region + "&msg=" + action + "_failed"
		if reason != "" {
			target += "&err=" + url.QueryEscape(reason)
		}
		c.Redirect(http.StatusFound, target)
		return
	}

	// invalidate cache
	instCache.Delete(cacheKey)

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

