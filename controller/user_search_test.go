package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestSearchUsersRejectsInvalidSubscriptionFilters verifies malformed filters do not reach the database.
func TestSearchUsersRejectsInvalidSubscriptionFilters(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantMessage string
	}{
		{name: "invalid active flag", query: "active_subscription=1", wantMessage: "invalid active_subscription"},
		{name: "non numeric plan", query: "subscription_plan_id=abc", wantMessage: "invalid subscription_plan_id"},
		{name: "non positive plan", query: "subscription_plan_id=0", wantMessage: "invalid subscription_plan_id"},
		{name: "negative plan", query: "subscription_plan_id=-1", wantMessage: "invalid subscription_plan_id"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/search?"+test.query, nil)

			SearchUsers(ctx)

			assert.Contains(t, response.Body.String(), test.wantMessage)
		})
	}
}
