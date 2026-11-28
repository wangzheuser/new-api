package helper

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
)

const (
	InitialScannerBufferSize    = 64 << 10  // 64KB (64*1024)
	DefaultMaxScannerBufferSize = 128 << 20 // 64MB (64*1024*1024) default SSE buffer size
	DefaultPingInterval         = 10 * time.Second
	// streamWriteTimeout bounds a single blocked write to a slow client so the
	// unconditional wg.Wait() in cleanup can always finish. Without it, a slow
	// but connected client (full TCP buffer, no server WriteTimeout) could hang
	// the handler forever.
	streamWriteTimeout = 30 * time.Second
)

// StreamScannerOptions controls how a stream without an explicit terminal marker is classified.
type StreamScannerOptions struct {
	RequireExplicitTerminal bool
	RequiredTerminalEvent   string
}

type boundedStreamDataQueue struct {
	items       chan string
	maxBytes    int
	queuedBytes int
	closed      bool
	mu          sync.Mutex
	cond        *sync.Cond
}

func newBoundedStreamDataQueue(maxEvents int, maxBytes int) *boundedStreamDataQueue {
	queue := &boundedStreamDataQueue{
		items:    make(chan string, maxEvents),
		maxBytes: maxBytes,
	}
	queue.cond = sync.NewCond(&queue.mu)
	return queue
}

func (q *boundedStreamDataQueue) put(ctx context.Context, stop <-chan bool, data string) bool {
	size := len(data)
	if q.maxBytes > 0 && size > q.maxBytes {
		return false
	}
	q.mu.Lock()
	for !q.closed && q.maxBytes > 0 && q.queuedBytes+size > q.maxBytes {
		q.cond.Wait()
	}
	if q.closed {
		q.mu.Unlock()
		return false
	}
	q.queuedBytes += size
	q.mu.Unlock()
	select {
	case q.items <- data:
		return true
	case <-ctx.Done():
	case <-stop:
	}
	q.release(size)
	return false
}

func (q *boundedStreamDataQueue) release(size int) {
	q.mu.Lock()
	q.queuedBytes -= size
	if q.queuedBytes < 0 {
		q.queuedBytes = 0
	}
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *boundedStreamDataQueue) cancel() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

func getScannerBufferSize() int {
	if constant.StreamScannerMaxBufferMB > 0 {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return DefaultMaxScannerBufferSize
}

func NewStreamScanner(reader io.Reader, limits ...any) *bufio.Scanner {
	maxBytes := getScannerBufferSize()
	if len(limits) > 0 {
		if limit, ok := limits[0].(int); ok && limit > 0 {
			maxBytes = limit
		}
	}
	return newStreamScannerWithLimit(reader, maxBytes)
}

func newStreamScannerWithLimit(reader io.Reader, maxBytes int) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	initialSize := InitialScannerBufferSize
	if maxBytes > 0 && initialSize > maxBytes {
		initialSize = maxBytes
	}
	scanner.Buffer(make([]byte, initialSize), maxBytes)
	return scanner
}

func copyCodexSSEHeaders(c *gin.Context, resp *http.Response) {
	if c == nil || c.Writer == nil || resp == nil {
		return
	}
	// codex
	for _, name := range []string{"X-Reasoning-Included", "X-Codex-Turn-State"} {
		values := resp.Header.Values(name)
		if !service.ShouldCopyUpstreamHeader(c, name, values) {
			continue
		}
		for _, value := range values {
			if value != "" {
				c.Writer.Header().Add(name, value)
			}
		}
	}
}

// ExtendWriteDeadline pushes the connection write deadline forward before each
// stream write. Best-effort: writers that don't support deadlines (e.g.
// httptest recorders) are silently ignored.
func ExtendWriteDeadline(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(streamWriteTimeout))
}

// ScanTextStream returns an explicit transient rejection before forwarding the first data event.
func ScanTextStream(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, options StreamScannerOptions, handle func(string, *StreamResult)) *types.NewAPIError {
	first := true
	StreamScannerHandlerWithOptions(c, resp, info, options, func(data string, sr *StreamResult) {
		if first {
			first = false
			if err := initialStreamRejection(data, resp, info); err != nil {
				// The controller records health even when this is the last or a pinned attempt.
				info.InitialStreamRejection = err
				sr.StopWithTerminal("error", "failed", err)
				return
			}
		}
		handle(data, sr)
	})
	return info.InitialStreamRejection
}

func initialStreamRejection(data string, resp *http.Response, info *relaycommon.RelayInfo) *types.NewAPIError {
	if resp.StatusCode != http.StatusOK || info.ProtocolEventWritten ||
		info.SendResponseCount != 0 || info.StreamStatus.ClientPayloadIsCommitted() || info.StreamStatus.ErrorFrameIsWritten() {
		return nil
	}
	// Only a standalone provider rejection is replayable. Failure terminals can carry partial work.
	var frame struct {
		Type    string
		Code    string
		Message string
		Error   *struct {
			Type, Status, Message string
			Code                  any
		}
	}
	if !strings.Contains(data, `"error"`) || common.UnmarshalJsonStr(data, &frame) != nil {
		return nil
	}
	if frame.Type != "error" && (frame.Type != "" || frame.Error == nil) {
		return nil
	}
	var fields map[string]any
	if common.UnmarshalJsonStr(data, &fields) != nil {
		return nil
	}
	for key := range fields {
		switch key {
		case "type", "code", "message", "error", "param", "sequence_number", "event_id":
		default:
			return nil
		}
	}
	code, message := frame.Code, frame.Message
	if frame.Error != nil {
		code, message = frame.Error.Type, frame.Error.Message
		if explicit, ok := frame.Error.Code.(string); ok && explicit != "" {
			code = explicit
		}
		if code == "" {
			code = frame.Error.Status
		}
	}
	var status int
	switch code {
	case "rate_limit_error", "rate_limit_exceeded", "RESOURCE_EXHAUSTED":
		status = http.StatusTooManyRequests
	case "overloaded_error", "server_overloaded", "UNAVAILABLE":
		status = http.StatusServiceUnavailable
	default:
		return nil
	}
	return types.WithOpenAIError(types.OpenAIError{Code: code, Type: "upstream_error", Message: message}, status,
		types.ErrOptionWithUpstreamStatusCode(resp.StatusCode), types.ErrOptionWithSSEStatus(status),
		types.ErrOptionWithUpstreamRetryAfter(resp.Header.Get("Retry-After")))
}

// StreamScannerHandler scans an SSE response with legacy EOF-compatible semantics.
func StreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string, sr *StreamResult)) {
	StreamScannerHandlerWithOptions(c, resp, info, StreamScannerOptions{}, dataHandler)
}

// StreamScannerHandlerWithOptions scans an SSE response and applies the requested terminal policy.
func StreamScannerHandlerWithOptions(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, options StreamScannerOptions, dataHandler func(data string, sr *StreamResult)) {

	if resp == nil || dataHandler == nil {
		return
	}

	if info.StreamStatus == nil {
		info.StreamStatus = relaycommon.NewStreamStatus()
	}
	info.StreamStatus.SetStreamPolicyVersion(
		info.AcceptStreamPolicyVersion(resp.Header.Get("X-Stream-Policy")),
	)

	ctx, cancel := context.WithCancel(context.Background())

	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second

	var (
		stopChan    = make(chan bool, 3) // 增加缓冲区避免阻塞
		ticker      = time.NewTicker(streamingTimeout)
		writeMutex  = info.StreamWriterMutex() // 与请求级 pinger 共用写锁
		wg          sync.WaitGroup             // 用于等待所有 goroutine 退出
		cleanupOnce sync.Once
		stopOnce    sync.Once
		scanErr     error
	)
	progressiveTarget := info.StreamStatus.StreamPolicyVersion() == "progressive-v1"
	queueMaxEvents := 10
	queueMaxBytes := 0
	maxEventBytes := getScannerBufferSize()
	if progressiveTarget {
		options.RequireExplicitTerminal = true
		queueMaxEvents = 16
		queueMaxBytes = 1 << 20
		maxEventBytes = 1 << 20
	}
	if options.RequireExplicitTerminal {
		info.StreamStatus.RequireTerminal()
	}
	scanner := newStreamScannerWithLimit(resp.Body, maxEventBytes)
	dataQueue := newBoundedStreamDataQueue(queueMaxEvents, queueMaxBytes)

	stop := func() {
		stopOnce.Do(func() {
			dataQueue.cancel()
			close(stopChan)
		})
	}

	logger.LogDebug(c, "relay timeout seconds: %d", common.RelayTimeout)
	logger.LogDebug(c, "relay max idle conns: %d", common.RelayMaxIdleConns)
	logger.LogDebug(c, "relay max idle conns per host: %d", common.RelayMaxIdleConnsPerHost)
	logger.LogDebug(c, "streaming timeout seconds: %d", int64(streamingTimeout.Seconds()))
	EnsureConfiguredStreamPinger(c, info)

	cleanup := func() {
		cleanupOnce.Do(func() {
			info.StopStreamPinger()
			cancel()
			stop()
			if resp.Body != nil {
				_ = resp.Body.Close()
			}

			ticker.Stop()
			wg.Wait()
		})
	}
	// Ensure gin.Context is not returned to Gin's pool while any stream goroutine can still use it.
	defer cleanup()

	scanner.Split(bufio.ScanLines)
	copyCodexSSEHeaders(c, resp)
	SetEventStreamHeaders(c)

	ctx = context.WithValue(ctx, "stop_chan", stopChan)

	wg.Add(1)
	gopool.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.LogError(c, common.LogText("data handler goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("handler panic: %v", r))
			}
			stop()
			wg.Done()
		}()
		sr := newStreamResult(info.StreamStatus)
		for data := range dataQueue.items {
			dataQueue.release(len(data))
			sr.reset()
			func() {
				writeMutex.Lock()
				defer writeMutex.Unlock()
				if !info.FirstPayloadDeadline.IsZero() {
					if _, endErr := info.StreamStatus.End(); endErr == relaycommon.ErrFirstPayloadTimeout {
						sr.Stop(nil)
						return
					}
				}
				ExtendWriteDeadline(c)
				info.ObserveCacheUsage(info.GetFinalRequestRelayFormat(), []byte(data))
				dataHandler(data, sr)
			}()
			if sr.IsStopped() {
				return
			}
		}
	})

	// Scanner goroutine with improved error handling
	wg.Add(1)
	common.RelayCtxGo(ctx, func() {
		defer func() {
			close(dataQueue.items)
			if r := recover(); r != nil {
				logger.LogError(c, common.LogText("scanner goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("scanner panic: %v", r))
			}
			stop()
			logger.LogDebug(c, "scanner goroutine exited")
			wg.Done()
		}()

		for scanner.Scan() {
			// 检查是否需要停止
			select {
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			default:
			}

			ticker.Reset(streamingTimeout)
			data := scanner.Text()
			logger.LogDebug(c, "stream scanner data: %s", data)

			if len(data) < 6 {
				continue
			}
			if data[:5] != "data:" && data[:6] != "[DONE]" {
				continue
			}
			data = data[5:]
			data = strings.TrimSpace(data)
			if data == "" {
				continue
			}
			if !strings.HasPrefix(data, "[DONE]") {
				info.SetFirstResponseTime()
				info.ReceivedResponseCount++

				if !dataQueue.put(ctx, stopChan, data) {
					return
				}
			} else {
				// A transport trailer cannot replace a protocol-specific terminal queued before it.
				if options.RequiredTerminalEvent != "" && options.RequiredTerminalEvent != "[DONE]" {
					return
				}
				info.StreamStatus.SetTerminal("[DONE]", "completed")
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				logger.LogDebug(c, "received [DONE], stopping scanner")
				return
			}
		}

		if err := scanner.Err(); err != nil {
			if err != io.EOF {
				scanErr = err
			}
		}
	})

	// 主循环等待完成或超时
	select {
	case <-ticker.C:
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
	case <-stopChan:
		// EndReason already set by the goroutine that triggered stopChan
	case <-c.Request.Context().Done():
		// 客户端断开：立即 cleanup 关闭上游 resp.Body，解除 scanner 阻塞并让上游停止生成，
		// 避免为已放弃的请求继续消费上游 token。
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
	}

	cleanup()
	// Wait for the data handler to drain buffered events before classifying EOF.
	// A terminal event can be queued immediately before the upstream closes.
	if !info.StreamStatus.HasEnded() {
		switch {
		case scanErr != nil:
			logger.LogError(c, "scanner error: "+scanErr.Error())
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, scanErr)
		case options.RequireExplicitTerminal:
			terminalEvent, _ := info.StreamStatus.Terminal()
			expectedTerminal := strings.TrimSpace(options.RequiredTerminalEvent)
			if terminalEvent != "" && (expectedTerminal == "" || terminalEvent == expectedTerminal) {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
			} else {
				err := fmt.Errorf("stream ended before terminal event")
				if expectedTerminal != "" {
					err = fmt.Errorf("stream ended before terminal event %s", expectedTerminal)
				}
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonUnexpectedEOF, err)
			}
		default:
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
		}
	}
	if info.StreamStatus.IsNormalEnd() && !info.StreamStatus.HasErrors() {
		logger.LogInfo(c, common.LogText("stream ended: %s", info.StreamStatus.Summary()))
	} else {
		logger.LogError(c, common.LogText("stream ended: %s, received=%d", info.StreamStatus.Summary(), info.ReceivedResponseCount))
	}
}
