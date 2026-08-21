package controller

import (
	"bytes"
	"net/http"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// requestedModelResponseWriter exposes only the client-requested model in HTTP and SSE responses.
type requestedModelResponseWriter struct {
	gin.ResponseWriter
	info        *relaycommon.RelayInfo
	pendingSSE  []byte
	pendingJSON []byte
	statusCode  int
	headerSent  bool
}

// RebindResponseWriter preserves pending SSE state when an attempt buffer releases its downstream writer.
func (w *requestedModelResponseWriter) RebindResponseWriter(writer gin.ResponseWriter) {
	if w == nil || writer == nil {
		return
	}
	w.ResponseWriter = writer
}

// FinishResponseWriter commits or discards bytes held for an incomplete JSON or SSE frame.
func (w *requestedModelResponseWriter) FinishResponseWriter(commit bool) error {
	if w == nil {
		return nil
	}
	pendingSSE := w.pendingSSE
	pendingJSON := w.pendingJSON
	w.pendingSSE = nil
	w.pendingJSON = nil
	if !commit {
		relaycommon.FilterClientModelResponseHeaders(w.Header(), w.info)
		if !w.ResponseWriter.Written() {
			relaycommon.ClearTransformedEntityHeaders(w.Header())
			w.Header().Del("Content-Type")
			w.Header().Del("Transfer-Encoding")
			w.statusCode = 0
			w.headerSent = false
		}
		return nil
	}
	if len(pendingSSE) == 0 && len(pendingJSON) == 0 {
		w.commitHeader(false)
		return nil
	}
	if len(pendingSSE) > 0 {
		rewritten, changed := rewriteRequestedModelResponse(pendingSSE, w.info, w.responseStatusCode())
		w.commitHeader(changed || w.isSSE())
		_, err := w.ResponseWriter.Write(rewritten)
		return err
	}
	rewritten, changed := relaycommon.RewriteClientModelJSON(pendingJSON, w.info, w.responseStatusCode())
	w.commitHeader(changed)
	_, err := w.ResponseWriter.Write(rewritten)
	return err
}

// WriteHeader delays the status until the body determines whether entity headers became stale.
func (w *requestedModelResponseWriter) WriteHeader(code int) {
	if w == nil || w.ResponseWriter == nil || code <= 0 || w.headerSent || w.ResponseWriter.Written() {
		return
	}
	w.statusCode = code
}

// WriteHeaderNow commits filtered headers when a handler explicitly requests it.
func (w *requestedModelResponseWriter) WriteHeaderNow() {
	if w == nil {
		return
	}
	contentType := strings.ToLower(w.Header().Get("Content-Type"))
	mayTransform := strings.Contains(contentType, "json") || strings.Contains(contentType, "text/event-stream")
	w.commitHeader(mayTransform)
}

// Status returns the delayed status code before the underlying writer is committed.
func (w *requestedModelResponseWriter) Status() int {
	if w == nil {
		return 0
	}
	if w.statusCode != 0 {
		return w.statusCode
	}
	if w.ResponseWriter == nil {
		return http.StatusOK
	}
	return w.ResponseWriter.Status()
}

// Write rewrites complete JSON bodies and complete SSE lines while preserving arbitrary network chunks.
func (w *requestedModelResponseWriter) Write(data []byte) (int, error) {
	if w == nil || w.ResponseWriter == nil {
		return 0, http.ErrBodyNotAllowed
	}
	if w.isSSE() || len(w.pendingSSE) > 0 || looksLikeSSE(data) {
		return w.writeSSE(data)
	}
	if w.isJSON() || len(w.pendingJSON) > 0 || looksLikeJSON(data) {
		return w.writeJSON(data)
	}

	rewritten, changed := rewriteRequestedModelResponse(data, w.info, w.responseStatusCode())
	w.commitHeader(changed)
	if _, err := w.ResponseWriter.Write(rewritten); err != nil {
		return 0, err
	}
	return len(data), nil
}

// WriteString delegates to Write so string and byte responses share the same contract.
func (w *requestedModelResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// Flush emits a complete pending SSE data line and then flushes the downstream writer.
func (w *requestedModelResponseWriter) Flush() {
	if w == nil || w.ResponseWriter == nil {
		return
	}
	if len(w.pendingJSON) > 0 {
		return
	}
	if len(w.pendingSSE) > 0 {
		trimmed := bytes.TrimSpace(w.pendingSSE)
		payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
		if !bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Equal(payload, []byte("[DONE]")) || gjson.ValidBytes(payload) {
			rewritten, changed := rewriteRequestedModelResponse(w.pendingSSE, w.info, w.responseStatusCode())
			w.commitHeader(changed || w.isSSE())
			_, _ = w.ResponseWriter.Write(rewritten)
			w.pendingSSE = nil
		}
	}
	w.commitHeader(w.isSSE())
	w.ResponseWriter.Flush()
}

// writeJSON buffers a JSON response until a complete value is available across Write calls.
func (w *requestedModelResponseWriter) writeJSON(data []byte) (int, error) {
	w.pendingJSON = append(w.pendingJSON, data...)
	if !gjson.ValidBytes(bytes.TrimSpace(w.pendingJSON)) {
		return len(data), nil
	}
	rewritten, changed := relaycommon.RewriteClientModelJSON(w.pendingJSON, w.info, w.responseStatusCode())
	w.pendingJSON = nil
	w.commitHeader(changed)
	if _, err := w.ResponseWriter.Write(rewritten); err != nil {
		return 0, err
	}
	return len(data), nil
}

// writeSSE buffers only the final incomplete line and rewrites all complete SSE lines.
func (w *requestedModelResponseWriter) writeSSE(data []byte) (int, error) {
	w.pendingSSE = append(w.pendingSSE, data...)
	lineEnd := bytes.LastIndexByte(w.pendingSSE, '\n')
	if lineEnd < 0 {
		return len(data), nil
	}
	complete := w.pendingSSE[:lineEnd+1]
	rewritten, changed := rewriteRequestedModelResponse(complete, w.info, w.responseStatusCode())
	w.commitHeader(changed || w.isSSE())
	if _, err := w.ResponseWriter.Write(rewritten); err != nil {
		return 0, err
	}
	w.pendingSSE = append([]byte(nil), w.pendingSSE[lineEnd+1:]...)
	return len(data), nil
}

// responseStatusCode returns the candidate status used to recognize non-2xx error bodies.
func (w *requestedModelResponseWriter) responseStatusCode() int {
	statusCode := w.Status()
	if statusCode == 0 {
		return http.StatusOK
	}
	return statusCode
}

// isSSE reports whether the current response uses Server-Sent Events.
func (w *requestedModelResponseWriter) isSSE() bool {
	return w != nil && strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream")
}

// isJSON reports whether the current response declares a JSON media type.
func (w *requestedModelResponseWriter) isJSON() bool {
	return w != nil && strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "json")
}

// looksLikeJSON recognizes an object or array when an upstream omits its media type.
func looksLikeJSON(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

// looksLikeSSE recognizes an event data field when an upstream omits its media type.
func looksLikeSSE(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("data:"))
}

// commitHeader filters model disclosures and commits the delayed response status once.
func (w *requestedModelResponseWriter) commitHeader(transformed bool) {
	if w == nil || w.ResponseWriter == nil || w.headerSent {
		return
	}
	relaycommon.FilterClientModelResponseHeaders(w.Header(), w.info)
	if transformed {
		relaycommon.ClearTransformedEntityHeaders(w.Header())
	}
	statusCode := w.responseStatusCode()
	w.headerSent = true
	w.ResponseWriter.WriteHeader(statusCode)
}

// rewriteRequestedModelResponse rewrites a JSON body or JSON payloads on SSE data lines.
func rewriteRequestedModelResponse(data []byte, info *relaycommon.RelayInfo, statusCode int) ([]byte, bool) {
	if len(data) == 0 || info == nil || strings.TrimSpace(info.GetRequestedModelName()) == "" {
		return data, false
	}
	if gjson.ValidBytes(bytes.TrimSpace(data)) {
		return relaycommon.RewriteClientModelJSON(data, info, statusCode)
	}

	lines := bytes.SplitAfter(data, []byte("\n"))
	changed := false
	for index, line := range lines {
		content, ending := splitSSELineEnding(line)
		trimmedLeft := bytes.TrimLeft(content, " \t")
		if !bytes.HasPrefix(trimmedLeft, []byte("data:")) {
			continue
		}
		fieldOffset := len(content) - len(trimmedLeft) + len("data:")
		payloadStart := fieldOffset
		for payloadStart < len(content) && (content[payloadStart] == ' ' || content[payloadStart] == '\t') {
			payloadStart++
		}
		payloadEnd := len(content)
		for payloadEnd > payloadStart && (content[payloadEnd-1] == ' ' || content[payloadEnd-1] == '\t') {
			payloadEnd--
		}
		payload := content[payloadStart:payloadEnd]
		if !gjson.ValidBytes(payload) {
			continue
		}
		rewritten, payloadChanged := relaycommon.RewriteClientModelJSON(payload, info, statusCode)
		if !payloadChanged {
			continue
		}
		updated := make([]byte, 0, len(content)-len(payload)+len(rewritten)+len(ending))
		updated = append(updated, content[:payloadStart]...)
		updated = append(updated, rewritten...)
		updated = append(updated, content[payloadEnd:]...)
		updated = append(updated, ending...)
		lines[index] = updated
		changed = true
	}
	if !changed {
		return data, false
	}
	return bytes.Join(lines, nil), true
}

// splitSSELineEnding separates the payload from CRLF or LF without normalizing either.
func splitSSELineEnding(line []byte) ([]byte, []byte) {
	if bytes.HasSuffix(line, []byte("\r\n")) {
		return line[:len(line)-2], line[len(line)-2:]
	}
	if bytes.HasSuffix(line, []byte("\n")) {
		return line[:len(line)-1], line[len(line)-1:]
	}
	return line, nil
}

// installRequestedModelResponseWriter registers the per-attempt final client response writer.
func installRequestedModelResponseWriter(c *gin.Context, info *relaycommon.RelayInfo) {
	if c == nil || c.Writer == nil || info == nil || strings.TrimSpace(info.GetRequestedModelName()) == "" {
		return
	}
	wrap := func(writer gin.ResponseWriter) relaycommon.FinalResponseWriter {
		return &requestedModelResponseWriter{
			ResponseWriter: writer,
			info:           info,
		}
	}
	relaycommon.SetFinalResponseWriterFactory(c, wrap)
}
