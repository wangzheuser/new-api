package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeRegistrationCodeIds verifies batch request boundary validation.
func TestNormalizeRegistrationCodeIds(t *testing.T) {
	tooMany := make([]int, 101)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	tests := []struct {
		name    string
		ids     []int
		expects []int
		hasErr  bool
	}{
		{name: "empty", hasErr: true},
		{name: "invalid", ids: []int{1, 0}, hasErr: true},
		{name: "deduplicated", ids: []int{2, 1, 2}, expects: []int{2, 1}},
		{name: "too many", ids: tooMany, hasErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := normalizeRegistrationCodeIds(test.ids)
			if test.hasErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expects, actual)
		})
	}
}

// TestBatchUpdateRegistrationCodeStatusRejectsInvalidRequests verifies API request boundaries.
func TestBatchUpdateRegistrationCodeStatusRejectsInvalidRequests(t *testing.T) {
	tooMany := make([]byte, 0, 512)
	tooMany = append(tooMany, `{"ids":[`...)
	for id := 1; id <= 101; id++ {
		if id > 1 {
			tooMany = append(tooMany, ',')
		}
		tooMany = append(tooMany, []byte(strconv.Itoa(id))...)
	}
	tooMany = append(tooMany, `],"status":1}`...)

	tests := []struct {
		name string
		body []byte
	}{
		{name: "empty ids", body: []byte(`{"ids":[],"status":1}`)},
		{name: "invalid id", body: []byte(`{"ids":[0],"status":1}`)},
		{name: "too many ids", body: tooMany},
		{name: "invalid status", body: []byte(`{"ids":[1],"status":3}`)},
	}
	gin.SetMode(gin.TestMode)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/registration_code/status/batch", bytes.NewReader(test.body))
			ctx.Request.Header.Set("Content-Type", "application/json")

			BatchUpdateRegistrationCodeStatus(ctx)

			var payload struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			assert.False(t, payload.Success)
		})
	}
}
