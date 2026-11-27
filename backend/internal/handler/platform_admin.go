package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/credits"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformadmin"
	"net/http"
	"strconv"
	"time"
)

func platformPage(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("pageSize", "30"))
	return page, limit
}
func platformBind(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := c.ShouldBindJSON(v); err != nil {
		failService(c, kernel.BadAuthRequest("请求参数无效"))
		return false
	}
	return true
}
func platformResult(c *gin.Context, value any, err error) {
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, value)
}

func platformCatalogBind(c *gin.Context, value any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := c.ShouldBindJSON(value); err != nil {
		failService(c, kernel.BadAuthRequest("模型配置字段或类型无效；请检查高级配置中的字段名称、规格字符串和积分整数"))
		return false
	}
	return true
}

func RegisterPlatformAdmin(api *gin.RouterGroup, auth *identity.Service, billing *credits.Service, catalog *platformadmin.Service) {
	r := api.Group("/admin")
	r.Use(func(c *gin.Context) {
		if _, err := auth.RequireAdmin(platformAccount(c).ID); err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		c.Next()
	})
	r.GET("/accounts", func(c *gin.Context) {
		p, n := platformPage(c)
		rows, total, err := auth.Accounts(platformAccount(c).ID, c.Query("q"), c.Query("status"), p, n)
		platformResult(c, gin.H{"items": rows, "total": total}, err)
	})
	r.POST("/accounts", func(c *gin.Context) {
		var req struct {
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
		}
		if !platformBind(c, &req) {
			return
		}
		u, password, err := auth.CreateAccount(platformAccount(c).ID, req.Username, req.DisplayName)
		platformResult(c, gin.H{"user": u, "temporaryPassword": password}, err)
	})
	r.PATCH("/accounts/:id", func(c *gin.Context) {
		var req struct {
			DisplayName string           `json:"displayName"`
			Status      model.UserStatus `json:"status"`
		}
		if !platformBind(c, &req) {
			return
		}
		platformResult(c, gin.H{"saved": true}, auth.UpdateAccount(platformAccount(c).ID, c.Param("id"), req.DisplayName, req.Status))
	})
	r.POST("/accounts/:id/reset-password", func(c *gin.Context) {
		password, err := auth.ResetPassword(platformAccount(c).ID, c.Param("id"))
		platformResult(c, gin.H{"temporaryPassword": password}, err)
	})
	r.POST("/accounts/:id/credits", func(c *gin.Context) {
		var req struct {
			Amount      int64  `json:"amount"`
			OperationID string `json:"operationId"`
			Reason      string `json:"reason"`
		}
		if !platformBind(c, &req) {
			return
		}
		platformResult(c, gin.H{"saved": true}, billing.Adjust(platformAccount(c).ID, c.Param("id"), req.OperationID, req.Reason, req.Amount))
	})
	r.GET("/prices", func(c *gin.Context) {
		value, err := billing.Prices(platformAccount(c).ID)
		platformResult(c, value, err)
	})
	r.PUT("/prices", func(c *gin.Context) {
		var req credits.PriceRequest
		if !platformBind(c, &req) {
			return
		}
		_, err := billing.SetPrices(platformAccount(c).ID, []credits.PriceRequest{req})
		platformResult(c, gin.H{"saved": true}, err)
	})
	r.POST("/prices/batch", func(c *gin.Context) {
		var req struct {
			Prices []credits.PriceRequest `json:"prices"`
		}
		if !platformBind(c, &req) {
			return
		}
		value, err := billing.SetPrices(platformAccount(c).ID, req.Prices)
		platformResult(c, gin.H{"items": value}, err)
	})
	r.GET("/usage", func(c *gin.Context) {
		p, n := platformPage(c)
		rows, total, err := billing.Usage(platformAccount(c).ID, c.Query("userId"), c.Query("state"), p, n)
		platformResult(c, gin.H{"items": rows, "total": total}, err)
	})
	r.POST("/usage/:id/resolve", func(c *gin.Context) {
		var req struct {
			Units  int64  `json:"units"`
			Reason string `json:"reason"`
		}
		if !platformBind(c, &req) {
			return
		}
		platformResult(c, gin.H{"saved": true}, billing.Resolve(platformAccount(c).ID, c.Param("id"), req.Reason, req.Units))
	})
	r.GET("/channels", func(c *gin.Context) {
		p, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		n, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
		value, err := catalog.Channels(platformAccount(c).ID, admin.ListQuery{Page: p, Limit: n, Keyword: c.Query("q"), Status: c.Query("status")})
		platformResult(c, value, err)
	})
	r.GET("/protocols", func(c *gin.Context) {
		value, err := catalog.Protocols(platformAccount(c).ID, c.Query("capability"))
		platformResult(c, gin.H{"providers": value}, err)
	})
	r.POST("/model-capabilities", func(c *gin.Context) {
		var req modelcatalog.ModelCapabilityDraft
		if !platformBind(c, &req) {
			return
		}
		value, err := catalog.Capability(platformAccount(c).ID, req)
		platformResult(c, value, err)
	})
	r.POST("/channels/probe", func(c *gin.Context) {
		if !enforceRateLimit(c, "platform-channel-probe:"+platformAccount(c).ID, 30, time.Minute) {
			return
		}
		var req platformadmin.ProbeRequest
		if !platformBind(c, &req) {
			return
		}
		value, err := catalog.Probe(c.Request.Context(), platformAccount(c).ID, req)
		platformResult(c, value, err)
	})
	saveChannel := func(c *gin.Context) {
		var req platformadmin.ChannelRequest
		if !platformCatalogBind(c, &req) {
			return
		}
		value, err := catalog.SaveChannel(platformAccount(c).ID, c.Param("channelId"), req)
		platformResult(c, value, err)
	}
	r.POST("/channels", saveChannel)
	r.PUT("/channels/:channelId", saveChannel)
	r.DELETE("/channels/:channelId", func(c *gin.Context) {
		platformResult(c, gin.H{"deleted": true}, catalog.DeleteChannel(platformAccount(c).ID, c.Param("channelId")))
	})
	r.GET("/channels/:channelId/models", func(c *gin.Context) {
		p, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		n, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
		status := c.Query("status")
		if status == "" {
			status = c.Query("readiness")
		}
		value, err := catalog.Models(platformAccount(c).ID, c.Param("channelId"), platformadmin.ModelQuery{Page: p, PageSize: n, Keyword: c.Query("q"), Capability: c.Query("capability"), Status: status})
		platformResult(c, value, err)
	})
	saveModel := func(c *gin.Context) {
		var req platformadmin.ModelRequest
		if !platformCatalogBind(c, &req) {
			return
		}
		value, err := catalog.SaveModel(platformAccount(c).ID, c.Param("channelId"), c.Param("id"), req)
		platformResult(c, value, err)
	}
	r.POST("/channels/:channelId/models", saveModel)
	r.PUT("/channels/:channelId/models/:id", saveModel)
	r.POST("/channels/:channelId/models/batch", func(c *gin.Context) {
		var req struct {
			Models []platformadmin.ModelRequest `json:"models"`
		}
		if !platformCatalogBind(c, &req) {
			return
		}
		value, err := catalog.SaveModels(platformAccount(c).ID, c.Param("channelId"), req.Models)
		platformResult(c, gin.H{"items": value}, err)
	})
	r.POST("/channels/:channelId/import", func(c *gin.Context) {
		var req struct {
			Models        []string `json:"models"`
			CatalogFormat string   `json:"catalogFormat"`
		}
		if !platformBind(c, &req) {
			return
		}
		value, err := catalog.ImportModels(c.Request.Context(), platformAccount(c).ID, c.Param("channelId"), req.Models, req.CatalogFormat)
		platformResult(c, value, err)
	})
}
