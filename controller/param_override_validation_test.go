package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParamOverridePersistenceEndpointsRejectInvalidRules verifies every
// controller entry point rejects an invalid response operation before storage.
func TestParamOverridePersistenceEndpointsRejectInvalidRules(t *testing.T) {
	invalidOverride := `{"operations":[{"phase":"response","mode":"set","path":"x","value":1}]}`
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		handler func(*gin.Context)
	}{
		{
			name:   "channel add",
			method: http.MethodPost,
			path:   "/api/channel",
			body: `{"mode":"single","channel":{"type":1,"key":"test-key","name":"test","models":"MODEL","group":"default","param_override":` +
				quoteJSONForControllerTest(t, invalidOverride) + `}}`,
			handler: AddChannel,
		},
		{
			name:    "channel update",
			method:  http.MethodPut,
			path:    "/api/channel/",
			body:    `{"id":1,"param_override":` + quoteJSONForControllerTest(t, invalidOverride) + `}`,
			handler: UpdateChannel,
		},
		{
			name:    "tag batch edit",
			method:  http.MethodPost,
			path:    "/api/channel/tag",
			body:    `{"tag":"test","param_override":` + quoteJSONForControllerTest(t, invalidOverride) + `}`,
			handler: EditTagChannels,
		},
		{
			name:   "system final error",
			method: http.MethodPut,
			path:   "/api/option/",
			body: `{"key":"general_setting.default_final_error_override","value":` +
				quoteJSONForControllerTest(t, invalidOverride) + `}`,
			handler: UpdateOption,
		},
		{
			name:   "channel affinity template",
			method: http.MethodPut,
			path:   "/api/option/",
			body: `{"key":"channel_affinity_setting.rules","value":` +
				quoteJSONForControllerTest(t, `[{"name":"test","param_override_template":`+invalidOverride+`}]`) + `}`,
			handler: UpdateOption,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set("id", 1)
			ctx.Set("role", common.RoleRootUser)

			test.handler(ctx)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.NotEmpty(t, response.Message)
		})
	}
}

// quoteJSONForControllerTest encodes a JSON document as one JSON string value.
func quoteJSONForControllerTest(t *testing.T, value string) string {
	t.Helper()
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
