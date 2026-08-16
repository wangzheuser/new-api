package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIOCopyBytesGracefullyDefersResponseEvaluationUntilSettlement verifies
// provider parsing and usage classification finish before rules are evaluated.
func TestIOCopyBytesGracefullyDefersResponseEvaluationUntilSettlement(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: responseOverrideForHTTPTest()},
	}
	relaycommon.StartResponseOverrideBuffer(c, info)
	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	body := []byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}],"usage":{"total_tokens":12}}`)
	info.MergeResponseSemantics(types.RelayFormatOpenAI, body)

	IOCopyBytesGracefully(c, upstream, body)

	require.NotNil(t, info.ResponseOverride)
	assert.False(t, info.ResponseOverride.Evaluated)
	assert.False(t, info.ResponseOverride.Applied)
	assert.Equal(t, relaycommon.ResponseUsageUpstream, info.ResponseSemantics.Response.UsageState)
	assert.Equal(t, 0, recorder.Body.Len())
	assert.False(t, recorder.Flushed)

	decision := EvaluateResponseOverrideBeforeSettlement(c, info, &dto.Usage{TotalTokens: 12}, upstream.StatusCode)
	require.NotNil(t, decision)
	assert.True(t, info.ResponseOverride.Evaluated)
	assert.True(t, info.ResponseOverride.Applied)
	assert.Equal(t, relaycommon.ResponseOutcomeRejected, info.ResponseOverride.Semantics.Response.PrimaryOutcome)
	assert.Equal(t, relaycommon.ResponseUsageUpstream, info.ResponseOverride.Semantics.Response.UsageState)
	assert.Equal(t, 0, recorder.Body.Len())
	assert.False(t, recorder.Flushed)
}

// TestIOCopyBytesGracefullyFailsOpenOnInvalidRuntimeOverride verifies configuration drift preserves the upstream success.
func TestIOCopyBytesGracefullyFailsOpenOnInvalidRuntimeOverride(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: map[string]interface{}{
			"operations": []interface{}{
				map[string]interface{}{
					"phase": "response",
					"mode":  "return_error",
					"value": map[string]interface{}{"message": "invalid", "skip_retry": false},
				},
			},
		}},
	}
	relaycommon.StartResponseOverrideBuffer(c, info)
	body := []byte(`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`)

	IOCopyBytesGracefully(c, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, body)

	require.NotNil(t, info.ResponseOverride)
	assert.False(t, info.ResponseOverride.Evaluated)
	decision := EvaluateResponseOverrideBeforeSettlement(c, info, nil, http.StatusOK)
	require.NotNil(t, decision)
	assert.False(t, info.ResponseOverride.Applied)
	assert.Equal(t, relaycommon.ResponseOverrideNotAppliedConfigError, info.ResponseOverride.NotAppliedReason)
	assert.NotEmpty(t, info.ResponseOverride.ConfigError)
	buffer := relaycommon.CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	require.NoError(t, buffer.Commit(c))
	assert.JSONEq(t, string(body), recorder.Body.String())
}

func responseOverrideForHTTPTest() map[string]interface{} {
	return map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "response",
				"mode":  "return_error",
				"value": map[string]interface{}{
					"message":     "模型拒绝执行该指令",
					"status_code": http.StatusInternalServerError,
				},
				"conditions": []interface{}{
					map[string]interface{}{
						"source": "semantic",
						"path":   "response.primary_outcome",
						"mode":   "full",
						"value":  relaycommon.ResponseOutcomeRejected,
					},
				},
			},
		},
	}
}
