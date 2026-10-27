package main

import (
	"net/http"
	"strconv"
	"strings"

	"autosail/internal/session"
	"github.com/gin-gonic/gin"
)

func handleLoginPage(c *gin.Context) {
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
}

func handleLoginPost(c *gin.Context) {
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
}

func handleRegisterPage(c *gin.Context) {
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
}

func handleRegisterPost(c *gin.Context) {
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
}

func handleLogout(c *gin.Context) {
	s := session.Must(c)
	s.SetString("user_id", "")
	s.SetString("username", "")
	s.SetString("is_admin", "")
	s.SetString("key_id", "")
	s.SetString("pending_key_id", "")
	c.Redirect(http.StatusFound, "/login?msg=logout")
}

func handleAdminRegistration(c *gin.Context) {
	s := session.Must(c)
	if !isAdminSession(s) {
		c.Redirect(http.StatusFound, "/")
		return
	}
	open := strings.TrimSpace(c.PostForm("open")) == "1"
	_ = appStore.SetRegistrationOpen(c.Request.Context(), open)
	c.Redirect(http.StatusFound, "/")
}

func handleAdminUserDelete(c *gin.Context) {
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
}
