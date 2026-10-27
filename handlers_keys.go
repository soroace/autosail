package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"autosail/internal/session"
	"github.com/gin-gonic/gin"
)

func handleKeySave(c *gin.Context) {
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
}

func handleKeySelect(c *gin.Context) {
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
}

func handleKeyActivate(c *gin.Context) {
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
}

func handleKeyDelete(c *gin.Context) {
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
}

func handleKeyReorder(c *gin.Context) {
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
}
