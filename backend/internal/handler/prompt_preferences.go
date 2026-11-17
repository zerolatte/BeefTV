package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/prompts"
	"net/http"
)

func registerPromptPreferenceRoutes(r *gin.RouterGroup, owners workspaceOwners, promptDomain *prompts.Service) {
	r.GET("/settings/prompt-templates", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		preferences, err := promptDomain.UserPromptPreferences(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"preferences": preferences})
	})
	r.PATCH("/settings/prompt-templates/:operation", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req prompts.UserPromptCustomizationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		customization, err := promptDomain.UpdateUserPromptCustomization(user, c.Param("operation"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"customization": customization})
	})
	r.DELETE("/settings/prompt-templates/:operation", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		if err := promptDomain.ResetUserPromptCustomization(user, c.Param("operation")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})
}
