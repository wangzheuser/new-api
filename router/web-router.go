package router

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// WebAssets holds the embedded dashboard frontend assets.
type WebAssets struct {
	BuildFS   embed.FS
	IndexPage []byte
}

type ThemeAssets struct {
	DefaultBuildFS   fs.FS
	DefaultIndexPage []byte
	ClassicBuildFS   fs.FS
	ClassicIndexPage []byte
}

func SetWebRouter(router *gin.Engine, rawAssets any, pluginDispatcher ...gin.HandlerFunc) {
	if assets, ok := rawAssets.(ThemeAssets); ok {
		router.NoRoute(func(c *gin.Context) {
			if isFrontendAssetPath(c.Request.URL.Path) {
				if _, err := fs.Stat(assets.DefaultBuildFS, strings.TrimPrefix(c.Request.URL.Path, "/")); err != nil {
					c.Header("Cache-Control", "no-store")
					c.Status(http.StatusNotFound)
					return
				}
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.DefaultIndexPage)
		})
		return
	}
	assets, ok := rawAssets.(WebAssets)
	if !ok {
		return
	}
	var pluginDispatcherHandler gin.HandlerFunc
	if len(pluginDispatcher) > 0 {
		pluginDispatcherHandler = pluginDispatcher[0]
	} else {
		pluginDispatcherHandler = func(c *gin.Context) {}
	}
	frontendFS := common.EmbedFolder(assets.BuildFS, "web/dist")
	compress := gzip.Gzip(gzip.DefaultCompression)

	router.NoRoute(
		pluginDispatcherHandler,
		middleware.RouteTag("web"),
		func(c *gin.Context) {
			// ServeFrontendFiles sends build files already compressed.
			if !frontendFS.Exists("/", c.Request.URL.Path) {
				compress(c)
			}
		},
		middleware.AccessTokenAudit(),
		middleware.GlobalWebRateLimit(frontendFS),
		middleware.Cache(),
		middleware.ServeFrontendFiles(frontendFS),
		func(c *gin.Context) {
			if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
				controller.RelayNotFound(c)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", assets.IndexPage)
		},
	)
}

func isFrontendAssetPath(path string) bool {
	return strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/assets/")
}
