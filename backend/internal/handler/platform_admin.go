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
		var req model.ModelCreditPrice
		if !platformBind(c, &req) {
			return
		}
		platformResult(c, gin.H{"saved": true}, billing.SetPrice(platformAccount(c).ID, req))
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
		p, n := platformPage(c)
		value, err := catalog.Channels(platformAccount(c).ID, admin.ListQuery{Page: p, Limit: n, Keyword: c.Query("q")})
		platformResult(c, value, err)
	})
	saveChannel := func(c *gin.Context) {
		var req modelcatalog.ChannelRequest
		if !platformBind(c, &req) {
			return
		}
		value, err := catalog.SaveChannel(platformAccount(c).ID, c.Param("channelId"), req)
		platformResult(c, value, err)
	}
	r.POST("/channels", saveChannel)
	r.PUT("/channels/:channelId", saveChannel)
	r.GET("/channels/:channelId/models", func(c *gin.Context) {
		value, err := catalog.Models(platformAccount(c).ID, c.Param("channelId"))
		platformResult(c, value, err)
	})
	saveModel := func(c *gin.Context) {
		var req modelcatalog.ChannelModelRequest
		if !platformBind(c, &req) {
			return
		}
		value, err := catalog.SaveModel(platformAccount(c).ID, c.Param("channelId"), c.Param("id"), req)
		platformResult(c, value, err)
	}
	r.POST("/channels/:channelId/models", saveModel)
	r.PUT("/channels/:channelId/models/:id", saveModel)
	r.POST("/channels/:channelId/import", func(c *gin.Context) {
		var req struct {
			Models []string `json:"models"`
		}
		if !platformBind(c, &req) {
			return
		}
		value, err := catalog.ImportModels(c.Request.Context(), platformAccount(c).ID, c.Param("channelId"), req.Models)
		platformResult(c, value, err)
	})
}
