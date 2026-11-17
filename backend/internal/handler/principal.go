package handler

import (
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"

	"github.com/gin-gonic/gin"
)

const principalContextKey = "canvas.principal"

// SetPrincipal 只供验证身份的中间件调用，工作区标识不能来自请求正文。
func SetPrincipal(c *gin.Context, principal identity.Principal) error {
	if err := principal.Validate(); err != nil {
		return kernel.Unauthorized("身份无效，请重新登录")
	}
	c.Set(principalContextKey, principal)
	return nil
}

func CurrentPrincipal(c *gin.Context) (identity.Principal, error) {
	value, exists := c.Get(principalContextKey)
	principal, valid := value.(identity.Principal)
	if !exists || !valid || principal.Validate() != nil {
		return identity.Principal{}, kernel.Unauthorized("请先登录")
	}
	return principal, nil
}
