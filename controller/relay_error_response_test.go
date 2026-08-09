package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRelayErrorResponse_OpenAICommittedStreamUsesSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	helper.SetEventStreamHeaders(ctx)
	require.NoError(t, helper.FlushWriter(ctx))

	relayErr := types.NewErrorWithStatusCode(errors.New("upstream timeout"), types.ErrorCodeDoRequestFailed, http.StatusGatewayTimeout)
	writeRelayErrorResponse(ctx, types.RelayFormatOpenAI, nil, &relaycommon.RelayInfo{IsStream: true}, relayErr)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	assert.True(t, strings.HasPrefix(recorder.Body.String(), "data: "))
	assert.Contains(t, recorder.Body.String(), `"message":"upstream timeout"`)
}

func TestWriteRelayErrorResponse_ClaudeCommittedStreamUsesSSEEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	helper.SetEventStreamHeaders(ctx)
	require.NoError(t, helper.FlushWriter(ctx))

	relayErr := types.NewErrorWithStatusCode(errors.New("upstream timeout"), types.ErrorCodeDoRequestFailed, http.StatusGatewayTimeout)
	writeRelayErrorResponse(ctx, types.RelayFormatClaude, nil, &relaycommon.RelayInfo{IsStream: true}, relayErr)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	assert.Contains(t, recorder.Body.String(), "event: error\n")
	assert.Contains(t, recorder.Body.String(), `"type":"error"`)
	assert.Contains(t, recorder.Body.String(), `"message":"upstream timeout"`)
}

func TestWriteRelayErrorResponse_UncommittedResponseKeepsHTTPStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	relayErr := types.NewErrorWithStatusCode(errors.New("upstream timeout"), types.ErrorCodeDoRequestFailed, http.StatusGatewayTimeout)
	writeRelayErrorResponse(ctx, types.RelayFormatOpenAI, nil, &relaycommon.RelayInfo{IsStream: true}, relayErr)

	assert.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"message":"upstream timeout"`)
}

func TestWriteRelayErrorResponse_CommittedStreamSkipsWriteAfterClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	requestCtx, cancel := context.WithCancel(context.Background())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestCtx)
	helper.SetEventStreamHeaders(ctx)
	require.NoError(t, helper.FlushWriter(ctx))
	cancel()

	relayErr := types.NewErrorWithStatusCode(errors.New("upstream timeout"), types.ErrorCodeDoRequestFailed, http.StatusGatewayTimeout)
	writeRelayErrorResponse(ctx, types.RelayFormatOpenAI, nil, &relaycommon.RelayInfo{IsStream: true}, relayErr)

	assert.Empty(t, recorder.Body.String())
}
