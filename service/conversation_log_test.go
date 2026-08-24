package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordConversationLogIncludesResponseOverrideMetadata verifies upstream success and the replaced client response remain jointly auditable.
func TestRecordConversationLogIncludesResponseOverrideMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.ConversationLog{}))
	requestID := "response-override-conversation-test"
	require.NoError(t, model.LOG_DB.Where("request_id = ?", requestID).Delete(&model.ConversationLog{}).Error)
	t.Cleanup(func() {
		_ = model.LOG_DB.Where("request_id = ?", requestID).Delete(&model.ConversationLog{}).Error
	})

	previousCaptureEnabled := common.ConversationCaptureEnabled
	common.ConversationCaptureEnabled = true
	t.Cleanup(func() {
		common.ConversationCaptureEnabled = previousCaptureEnabled
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"MODEL"}`))
	ctx.Set(common.RequestIdKey, requestID)
	ctx.Set("username", "audit-user")
	ctx.Set("use_channel", []string{"17"})
	semantics := relaycommon.ResponseSemantics{
		Response: relaycommon.ResponseSemanticSummary{
			TransportStatus: relaycommon.ResponseTransportSuccess,
			PrimaryOutcome:  relaycommon.ResponseOutcomeRejected,
			RejectionState:  relaycommon.ResponseRejectionAll,
			OutputState:     relaycommon.ResponseOutputEmpty,
			UsageState:      relaycommon.ResponseUsageUpstream,
			StreamState:     relaycommon.ResponseStreamNotStreamed,
		},
		Upstream: relaycommon.ResponseEndpointSemantics{Format: types.RelayFormatOpenAI, HTTPStatus: http.StatusOK},
		Client:   relaycommon.ResponseEndpointSemantics{Format: types.RelayFormatOpenAI, HTTPStatus: http.StatusForbidden},
	}
	info := &relaycommon.RelayInfo{
		TokenId:            23,
		UserId:             42,
		UsingGroup:         "default",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RequestedModelName: "MODEL",
		RequestURLPath:     "/v1/chat/completions",
		RelayFormat:        types.RelayFormatOpenAI,
		RetryIndex:         1,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         17,
			UpstreamModelName: "UPSTREAM_MODEL",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				ConversationLogEnabled: true,
			},
		},
		ResponseOverride: &relaycommon.ResponseOverrideDecision{
			Configured:           true,
			Evaluated:            true,
			Applied:              true,
			RuleID:               "operation[2]",
			RuleIndex:            2,
			Description:          "Map an upstream business rejection to a client error",
			UpstreamStatusCode:   http.StatusOK,
			ClientStatusCode:     http.StatusForbidden,
			Semantics:            semantics,
			Billable:             true,
			Retryable:            false,
			AffectsChannelHealth: false,
		},
	}
	relaycommon.StartConversationCapture(ctx, info)
	require.NotNil(t, info.ConversationCapture)

	upstreamBody := `{"choices":[{"finish_reason":"content_filter","message":{"content":""}}],"usage":{"completion_tokens":17}}`
	upstreamResponse := &http.Response{Body: io.NopCloser(strings.NewReader(upstreamBody))}
	relaycommon.WrapConversationUpstreamResponse(info, upstreamResponse)
	_, err := io.ReadAll(upstreamResponse.Body)
	require.NoError(t, err)

	clientBody := `{"error":{"message":"model response rejected","type":"upstream_response_error","code":"response_rejected"}}`
	ctx.Header("Content-Type", "application/json")
	ctx.Status(http.StatusForbidden)
	_, err = ctx.Writer.Write([]byte(clientBody))
	require.NoError(t, err)

	RecordConversationLog(ctx, info, nil)
	require.NoError(t, WaitForConversationLogWrites(context.Background()))

	var persisted model.ConversationLog
	require.NoError(t, model.LOG_DB.Where("request_id = ?", requestID).First(&persisted).Error)
	assert.Equal(t, http.StatusForbidden, persisted.StatusCode)
	assert.Equal(t, upstreamBody, persisted.UpstreamResponseBody)
	assert.Equal(t, clientBody, persisted.ClientResponseBody)

	var metadata map[string]interface{}
	require.NoError(t, common.Unmarshal([]byte(persisted.Metadata), &metadata))
	assert.Equal(t, float64(1), metadata["retry_index"])
	assert.Equal(t, float64(http.StatusOK), metadata["upstream_status_code"])
	assert.Equal(t, float64(http.StatusForbidden), metadata["client_status_code"])
	override, ok := metadata["response_override"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, override["applied"])
	assert.Equal(t, "operation[2]", override["rule_id"])
	assert.Equal(t, float64(2), override["rule_index"])
	assert.Equal(t, "Map an upstream business rejection to a client error", override["description"])
	assert.Equal(t, true, override["billable"])
	assert.Equal(t, false, override["retryable"])
	assert.Equal(t, false, override["affects_channel_health"])
	serializedSemantics, ok := override["semantics"].(map[string]interface{})
	require.True(t, ok)
	response, ok := serializedSemantics["response"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponseOutcomeRejected, response["primary_outcome"])
	assert.Equal(t, relaycommon.ResponseRejectionAll, response["rejection_state"])
	upstream, ok := serializedSemantics["upstream"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(http.StatusOK), upstream["http_status"])
	client, ok := serializedSemantics["client"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(http.StatusForbidden), client["http_status"])
}

// TestSanitizeConversationBody verifies text is preserved while JSON and SSE media payloads are omitted.
func TestSanitizeConversationBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		changed     bool
		contains    string
		notContains string
	}{
		{name: "plain json", body: `{"messages":[{"content":"hello"}]}`, changed: false, contains: `"hello"`},
		{name: "data uri", body: `{"image_url":{"url":"data:image/png;base64,aGVsbG8="},"text":"hello"}`, changed: true, contains: conversationBinaryOmitted, notContains: "aGVsbG8="},
		{name: "truncated data uri", body: `{"url":"data:image/png;base64,aGVsbG8=`, changed: true, contains: conversationBinaryOmitted, notContains: "aGVsbG8="},
		{name: "claude base64", body: `{"source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}`, changed: true, contains: conversationBinaryOmitted, notContains: "aGVsbG8="},
		{name: "gemini sse", body: "data: {\"candidates\":[],\"inlineData\":{\"mimeType\":\"image/png\",\"data\":\"aGVsbG8=\"}}\n\n", changed: true, contains: conversationBinaryOmitted, notContains: "aGVsbG8="},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sanitized, changed := SanitizeConversationBody([]byte(test.body))
			assert.Equal(t, test.changed, changed)
			assert.Contains(t, string(sanitized), test.contains)
			if test.notContains != "" {
				assert.NotContains(t, string(sanitized), test.notContains)
			}
		})
	}
}

// TestSanitizeConversationBodyRepairsInvalidUTF8 protects conversation log persistence on PostgreSQL.
func TestSanitizeConversationBodyRepairsInvalidUTF8(t *testing.T) {
	sanitized, changed := SanitizeConversationBody([]byte{'h', 'i', 0xe4})

	assert.False(t, changed)
	assert.True(t, utf8.Valid(sanitized))
	assert.Equal(t, "hi\uFFFD", string(sanitized))
}
