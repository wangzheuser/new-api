package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 30
	}
}

func newNativeResponsesStreamTest(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "responses-test"},
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAIResponses,
		DisablePing: true,
	}
	return c, recorder, resp, info
}

func TestOaiResponsesStreamHandlerCompletesOnTypedTerminal(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		``,
	}, "\n")
	c, recorder, resp, info := newNativeResponsesStreamTest(t, body)

	usage, newAPIError := OaiResponsesStreamHandler(c, info, resp)

	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	assert.Equal(t, 2, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Contains(t, recorder.Body.String(), "event: response.completed")
	reason, endErr := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonDone, reason)
	assert.NoError(t, endErr)
	event, status := info.StreamStatus.Terminal()
	assert.Equal(t, "response.completed", event)
	assert.Equal(t, "completed", status)
}

func TestOaiResponsesStreamHandlerAcceptsIncompleteAsBillableTerminal(t *testing.T) {
	body := `data: {"type":"response.incomplete","response":{"status":"incomplete","usage":{"input_tokens":4,"output_tokens":6,"total_tokens":10},"incomplete_details":{"reason":"max_output_tokens"}}}` + "\n"
	c, _, resp, info := newNativeResponsesStreamTest(t, body)

	usage, newAPIError := OaiResponsesStreamHandler(c, info, resp)

	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	assert.Equal(t, 10, usage.TotalTokens)
	event, status := info.StreamStatus.Terminal()
	assert.Equal(t, "response.incomplete", event)
	assert.Equal(t, "incomplete", status)
}

func TestOaiResponsesStreamHandlerReturnsForwardedFailure(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		`data: {"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"upstream_aborted","message":"mid stream aborted"}}}`,
		``,
	}, "\n")
	c, recorder, resp, info := newNativeResponsesStreamTest(t, body)

	usage, newAPIError := OaiResponsesStreamHandler(c, info, resp)

	assert.Nil(t, usage)
	require.NotNil(t, newAPIError)
	assert.Equal(t, types.ErrorCode("upstream_aborted"), newAPIError.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(newAPIError))
	assert.True(t, types.IsClientErrorWritten(newAPIError))
	assert.Contains(t, recorder.Body.String(), "event: response.failed")
	event, status := info.StreamStatus.Terminal()
	assert.Equal(t, "response.failed", event)
	assert.Equal(t, "failed", status)
}

func TestOaiResponsesStreamHandlerRejectsUnexpectedEOFAfterCommit(t *testing.T) {
	body := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n"
	c, recorder, resp, info := newNativeResponsesStreamTest(t, body)

	usage, newAPIError := OaiResponsesStreamHandler(c, info, resp)

	assert.Nil(t, usage)
	require.NotNil(t, newAPIError)
	assert.Equal(t, types.ErrorCodeBadResponse, newAPIError.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(newAPIError))
	assert.Contains(t, recorder.Body.String(), "partial")
	reason, endErr := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonUnexpectedEOF, reason)
	assert.EqualError(t, endErr, "stream ended before terminal event")
}

func TestOaiResponsesStreamHandlerAllowsRetryBeforeCommit(t *testing.T) {
	c, recorder, resp, info := newNativeResponsesStreamTest(t, "")

	usage, newAPIError := OaiResponsesStreamHandler(c, info, resp)

	assert.Nil(t, usage)
	require.NotNil(t, newAPIError)
	assert.False(t, types.IsSkipRetryError(newAPIError))
	assert.Empty(t, recorder.Body.String())
	reason, _ := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonUnexpectedEOF, reason)
}
