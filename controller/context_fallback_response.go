package controller

import (
	"bytes"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// contextFallbackResponseWriter 将普通 JSON 和 SSE 中的模型字段恢复为客户端请求模型。
type contextFallbackResponseWriter struct {
	gin.ResponseWriter
	requestedModel string
	pendingSSE     []byte
}

// WriteHeader 移除重写后已失效的上游内容长度，交由 HTTP 层重新计算或分块发送。
func (w *contextFallbackResponseWriter) WriteHeader(code int) {
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(code)
}

// Write 重写普通 JSON，并仅缓冲 SSE 的未完成行以兼容任意网络分块。
func (w *contextFallbackResponseWriter) Write(data []byte) (int, error) {
	w.Header().Del("Content-Length")
	if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") || len(w.pendingSSE) > 0 {
		w.pendingSSE = append(w.pendingSSE, data...)
		lineEnd := bytes.LastIndexByte(w.pendingSSE, '\n')
		if lineEnd < 0 {
			return len(data), nil
		}
		complete := w.pendingSSE[:lineEnd+1]
		if _, err := w.ResponseWriter.Write(rewriteContextFallbackResponseModels(complete, w.requestedModel)); err != nil {
			return 0, err
		}
		w.pendingSSE = append([]byte(nil), w.pendingSSE[lineEnd+1:]...)
		return len(data), nil
	}
	rewritten := rewriteContextFallbackResponseModels(data, w.requestedModel)
	_, err := w.ResponseWriter.Write(rewritten)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// WriteString 与 Write 共用同一模型重写逻辑。
func (w *contextFallbackResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// Flush 在完整 SSE 事件没有换行时仍会重写可识别的数据行。
func (w *contextFallbackResponseWriter) Flush() {
	if len(w.pendingSSE) > 0 {
		trimmed := bytes.TrimSpace(w.pendingSSE)
		payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
		if !bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Equal(payload, []byte("[DONE]")) || gjson.ValidBytes(payload) {
			_, _ = w.ResponseWriter.Write(rewriteContextFallbackResponseModels(w.pendingSSE, w.requestedModel))
			w.pendingSSE = nil
		}
	}
	w.ResponseWriter.Flush()
}

// rewriteContextFallbackResponseModels 重写顶层 model 及 Responses 事件中的 response.model。
func rewriteContextFallbackResponseModels(data []byte, requestedModel string) []byte {
	if len(data) == 0 || requestedModel == "" {
		return data
	}
	if gjson.ValidBytes(bytes.TrimSpace(data)) {
		return rewriteContextFallbackJSON(data, requestedModel)
	}

	lines := bytes.SplitAfter(data, []byte("\n"))
	changed := false
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
		if !gjson.ValidBytes(payload) {
			continue
		}
		rewritten := rewriteContextFallbackJSON(payload, requestedModel)
		if bytes.Equal(payload, rewritten) {
			continue
		}
		lineEnding := ""
		if bytes.HasSuffix(line, []byte("\r\n")) {
			lineEnding = "\r\n"
		} else if bytes.HasSuffix(line, []byte("\n")) {
			lineEnding = "\n"
		}
		lines[i] = []byte("data: " + string(rewritten) + lineEnding)
		changed = true
	}
	if !changed {
		return data
	}
	return bytes.Join(lines, nil)
}

// rewriteContextFallbackJSON 仅修改协议模型字段，保留其他响应内容。
func rewriteContextFallbackJSON(data []byte, requestedModel string) []byte {
	result := data
	for _, path := range []string{"model", "response.model", "message.model"} {
		if !gjson.GetBytes(result, path).Exists() {
			continue
		}
		updated, err := sjson.SetBytes(result, path, requestedModel)
		if err == nil {
			result = updated
		}
	}
	return result
}

// installContextFallbackResponseWriter 只在实际发生兜底时安装透明响应重写。
func installContextFallbackResponseWriter(c *gin.Context, requestedModel string) {
	if c == nil || c.Writer == nil || strings.TrimSpace(requestedModel) == "" {
		return
	}
	if _, installed := c.Writer.(*contextFallbackResponseWriter); installed {
		return
	}
	c.Writer = &contextFallbackResponseWriter{
		ResponseWriter: c.Writer,
		requestedModel: requestedModel,
	}
}
