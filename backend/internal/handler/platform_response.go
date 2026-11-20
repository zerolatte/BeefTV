package handler

import "github.com/gin-gonic/gin"

// 平台私有响应在实际发送前禁止缓存，避免素材处理器覆盖认证层的响应头。
type platformResponseWriter struct{ gin.ResponseWriter }

func (w platformResponseWriter) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(code)
}

func (w platformResponseWriter) WriteHeaderNow() {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeaderNow()
}

func (w platformResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	return w.ResponseWriter.Write(data)
}

func (w platformResponseWriter) WriteString(data string) (int, error) {
	w.WriteHeaderNow()
	return w.ResponseWriter.WriteString(data)
}

func (w platformResponseWriter) Flush() {
	w.WriteHeaderNow()
	w.ResponseWriter.Flush()
}
