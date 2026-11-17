//go:build platform

package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
)

// RegisterPlatformOpenAPIRoutes 必须在平台受保护路由装配最后调用，以实际路由图冻结公开合同。
func RegisterPlatformOpenAPIRoutes(protected *gin.RouterGroup, router *gin.Engine, sessionCookieName string) error {
	if protected == nil || router == nil || protected.BasePath() != "/api" || strings.TrimSpace(sessionCookieName) == "" {
		return errors.New("平台 OpenAPI 装配不完整")
	}
	var document []byte
	protected.GET("/openapi.yaml", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		if principal.Mode != identity.ModePlatform {
			failService(c, kernel.Unauthorized("请使用平台会话登录"))
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", document)
	})
	var err error
	document, err = yaml.Marshal(platformOpenAPIDocument(router.Routes(), sessionCookieName))
	return err
}

func platformOpenAPIDocument(routes gin.RoutesInfo, sessionCookieName string) gin.H {
	paths := gin.H{}
	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api/") {
			continue
		}
		path, parameters := platformOpenAPIPath(strings.TrimPrefix(route.Path, "/api"))
		item, _ := paths[path].(gin.H)
		if item == nil {
			item = gin.H{}
			paths[path] = item
		}
		item[strings.ToLower(route.Method)] = platformOpenAPIOperation(route.Method, path, parameters)
	}
	return gin.H{
		"openapi": "3.0.3",
		"info": gin.H{
			"title": "BeefTV 平台 API", "version": "1",
			"description": "本文档的路径来自当前平台进程实际注册的路由；未装配的能力不会出现在文档中。" +
				"当前合同描述路由、认证、路径参数和通用响应，尚未逐项枚举业务请求体、查询参数及数据字段。" +
				"除精确匿名入口外，所有访问均使用有效平台会话 Cookie，业务对象限定到绑定工作区；管理员访问业务内容也不例外。" +
				"所有 POST、PUT、PATCH、DELETE 必须同时携带精确匹配部署来源的 Origin 和 X-CSRF-Token。" +
				"登录前先 GET /auth/csrf 并保留引导 Cookie；登录后使用新会话绑定的 CSRF 令牌。" +
				"会话失效后停止读写；需要强制改密的账号仅能使用账号恢复接口。" +
				"当前真实生成关闭，已注册的任务生成与工作流执行入口仍返回 generation_unavailable，接口存在不代表生成可用。" +
				"生成是否可用以 /features 的 generation 为准。",
		},
		"servers":                     []gin.H{{"url": "/api"}},
		"security":                    []gin.H{{"PlatformSession": []string{}}},
		"x-beeftv-generation-enabled": false,
		"paths":                       paths,
		"components": gin.H{
			"securitySchemes": gin.H{
				"PlatformSession": gin.H{
					"type": "apiKey", "in": "cookie", "name": sessionCookieName,
					"description": "登录成功后由服务端签发。正式环境使用 HttpOnly、Secure、SameSite=Lax、Path=/，不设置 Domain；仅显式本机开发允许非 Secure Cookie。",
				},
			},
			"schemas": gin.H{
				"PlatformSuccess": gin.H{
					"type": "object", "required": []string{"code", "data", "msg"},
					"properties": gin.H{
						"code": gin.H{"type": "integer", "enum": []int{0}},
						"data": gin.H{}, "msg": gin.H{"type": "string"},
					},
				},
				"PlatformError": gin.H{
					"type": "object", "required": []string{"code", "data", "msg"},
					"properties": gin.H{
						"code": gin.H{"type": "integer"}, "data": gin.H{"type": "object", "nullable": true, "description": "失败时为 null。"},
						"msg": gin.H{"type": "string"}, "reason": gin.H{"type": "string"},
						"details": gin.H{"type": "object", "additionalProperties": true},
					},
				},
			},
			"responses": gin.H{
				"PlatformError": gin.H{
					"description": "HTTP 状态与 code 表示真实失败；客户端按 reason 判定，不解析 msg。跨工作区对象返回 404，权限不足返回 403。",
					"content":     gin.H{"application/json": gin.H{"schema": gin.H{"$ref": "#/components/schemas/PlatformError"}}},
				},
			},
		},
	}
}

func platformOpenAPIPath(path string) (string, []gin.H) {
	parts := strings.Split(path, "/")
	parameters := make([]gin.H, 0)
	for index, part := range parts {
		if !strings.HasPrefix(part, ":") && !strings.HasPrefix(part, "*") {
			continue
		}
		name := part[1:]
		parts[index] = "{" + name + "}"
		parameters = append(parameters, gin.H{"name": name, "in": "path", "required": true, "schema": gin.H{"type": "string"}})
	}
	return strings.Join(parts, "/"), parameters
}

func platformOpenAPIOperation(method, path string, parameters []gin.H) gin.H {
	operation := gin.H{"summary": method + " " + path}
	response := gin.H{
		"description": "成功；data 的业务结构由对应接口定义。",
		"content":     gin.H{"application/json": gin.H{"schema": gin.H{"$ref": "#/components/schemas/PlatformSuccess"}}},
	}
	responses := gin.H{"200": response, "default": gin.H{"$ref": "#/components/responses/PlatformError"}}
	operation["responses"] = responses
	if platformOpenAPIAnonymous(method, path) {
		operation["security"] = []gin.H{}
	}
	if strings.HasPrefix(path, "/admin/") {
		operation["x-required-role"] = "admin"
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		description := "与当前会话绑定的 CSRF 令牌，不能沿用其他会话的令牌。"
		if method == http.MethodPost && path == "/auth/login" {
			description = "先访问 GET /auth/csrf 获取短期登录令牌，并在此请求保留引导 Cookie。"
		}
		parameters = append(parameters,
			gin.H{"name": "Origin", "in": "header", "required": true, "description": "必须精确匹配平台配置的部署来源。", "schema": gin.H{"type": "string"}},
			gin.H{"name": "X-CSRF-Token", "in": "header", "required": true, "description": description, "schema": gin.H{"type": "string"}},
		)
	}
	if len(parameters) > 0 {
		operation["parameters"] = parameters
	}
	if method == http.MethodGet {
		switch {
		case path == "/openapi.yaml":
			response["description"] = "平台 OpenAPI 3.0 YAML；需要有效会话。"
			response["content"] = gin.H{"application/yaml": gin.H{"schema": gin.H{"type": "string"}}}
		case strings.HasSuffix(path, "/events") || strings.HasSuffix(path, "/text-events"):
			response["description"] = "使用 Cookie 鉴权的 SSE；会话撤销后最多 5 秒内结束。"
			response["content"] = gin.H{"text/event-stream": gin.H{"schema": gin.H{"type": "string"}}}
			if strings.HasSuffix(path, "/text-events") {
				operation["parameters"] = append(parameters,
					gin.H{"name": "Last-Event-ID", "in": "header", "description": "递增事件序号游标。", "schema": gin.H{"type": "integer", "format": "int64", "minimum": 0}},
					gin.H{"name": "after", "in": "query", "description": "递增事件序号游标。", "schema": gin.H{"type": "integer", "format": "int64", "minimum": 0}},
				)
			}
		case strings.HasSuffix(path, "/file"):
			response["description"] = "当前工作区拥有的资源文件；Range 请求同样校验归属。"
			response["content"] = gin.H{"application/octet-stream": gin.H{"schema": gin.H{"type": "string", "format": "binary"}}}
			responses["206"] = response
			operation["parameters"] = append(parameters, gin.H{"name": "Range", "in": "header", "schema": gin.H{"type": "string"}})
		}
	}
	return operation
}

func platformOpenAPIAnonymous(method, path string) bool {
	return method == http.MethodGet && (path == "/runtime" || path == "/healthz" || path == "/auth/csrf") || method == http.MethodPost && path == "/auth/login"
}
