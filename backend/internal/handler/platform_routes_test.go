package handler

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestPlatformCreativeBoundary(t *testing.T) {
	tests := []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/tasks", true}, {"POST", "/tasks/:id/cancel", true}, {"POST", "/tasks/:id/recover", true},
		{"POST", "/tasks/:id/retry", false}, {"POST", "/tasks/:id/text-deltas", false},
		{"POST", "/tasks/:id/text-replay-complete", false}, {"POST", "/timeline/renders", false},
		{"POST", "/media-upscale/tasks", false}, {"POST", "/video-subtitle-erase/tasks", false},
		{"POST", "/projects/:id/workflows", false}, {"PATCH", "/projects/:id/workflow-steps/:stepId", false},
		{"PUT", "/canvas-workflows/:id", false}, {"PATCH", "/resources/storage-usage", false},
		{"GET", "/settings/prompt-templates", false}, {"PATCH", "/settings/prompt-templates/:operation", false},
		{"POST", "/resources/uploads", true}, {"PUT", "/canvas-projects/:id", true},
	}
	for _, tc := range tests {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			router := gin.New()
			api := router.Group("/api", PlatformCreativeRoutes())
			api.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(204) })
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(tc.method, "/api"+tc.path, nil))
			want := 403
			if tc.allowed {
				want = 204
			}
			if response.Code != want {
				t.Fatalf("status=%d want=%d", response.Code, want)
			}
		})
	}
}
