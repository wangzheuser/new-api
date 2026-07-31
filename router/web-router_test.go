package router

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsFrontendAssetPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "compiled asset", path: "/static/js/async/old-build.js", want: true},
		{name: "application route", path: "/usage-logs/common", want: false},
		{name: "api route", path: "/api/log/", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, isFrontendAssetPath(test.path))
		})
	}
}

func TestMissingFrontendAssetIsNotCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buildFS := fstest.MapFS{
		"web/default/dist/index.html": &fstest.MapFile{Data: []byte("default")},
		"web/classic/dist/index.html": &fstest.MapFile{Data: []byte("classic")},
	}
	router := gin.New()
	SetWebRouter(router, ThemeAssets{
		DefaultBuildFS:   fs.FS(buildFS),
		DefaultIndexPage: []byte("default"),
		ClassicBuildFS:   fs.FS(buildFS),
		ClassicIndexPage: []byte("classic"),
	})

	request := httptest.NewRequest(http.MethodGet, "/static/js/old-build.js", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func TestApplicationRouteAlwaysRevalidatesIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buildFS := fstest.MapFS{
		"web/default/dist/index.html": &fstest.MapFile{Data: []byte("default")},
		"web/classic/dist/index.html": &fstest.MapFile{Data: []byte("classic")},
	}
	router := gin.New()
	SetWebRouter(router, ThemeAssets{
		DefaultBuildFS:   fs.FS(buildFS),
		DefaultIndexPage: []byte("default"),
		ClassicBuildFS:   fs.FS(buildFS),
		ClassicIndexPage: []byte("classic"),
	})

	request := httptest.NewRequest(http.MethodGet, "/usage-logs/common", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
}
