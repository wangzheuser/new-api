package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheHeaderByRequestPath(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "root", url: "/", want: "no-cache"},
		{name: "root with query", url: "/?release=current", want: "no-cache"},
		{name: "explicit index with query", url: "/index.html?release=current", want: "no-cache"},
		{name: "hashed static asset", url: "/static/js/index.1234.js", want: "max-age=604800"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(Cache())
			router.NoRoute(func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			request := httptest.NewRequest(http.MethodGet, test.url, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, test.want, response.Header().Get("Cache-Control"))
		})
	}
}
