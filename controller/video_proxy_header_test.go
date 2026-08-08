package controller

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCopyVideoResponseHeaders verifies media headers survive while provider metadata is blocked.
func TestCopyVideoResponseHeaders(t *testing.T) {
	src := http.Header{
		"Content-Type":      []string{"video/mp4"},
		"Content-Range":     []string{"bytes 0-99/100"},
		"Accept-Ranges":     []string{"bytes"},
		"Etag":              []string{"video-etag"},
		"Set-Cookie":        []string{"provider_session=secret"},
		"Server":            []string{"supplier-edge"},
		"Via":               []string{"supplier-proxy"},
		"X-Request-Id":      []string{"upstream-request"},
		"X-Ratelimit-Limit": []string{"123"},
		"X-Project-Id":      []string{"project-secret"},
	}
	dst := http.Header{}

	copyVideoResponseHeaders(dst, src)

	assert.Equal(t, "video/mp4", dst.Get("Content-Type"))
	assert.Equal(t, "bytes 0-99/100", dst.Get("Content-Range"))
	assert.Equal(t, "bytes", dst.Get("Accept-Ranges"))
	assert.Equal(t, "video-etag", dst.Get("ETag"))
	assert.Empty(t, dst.Get("Set-Cookie"))
	assert.Empty(t, dst.Get("Server"))
	assert.Empty(t, dst.Get("Via"))
	assert.Empty(t, dst.Get("X-Request-Id"))
	assert.Empty(t, dst.Get("X-RateLimit-Limit"))
	assert.Empty(t, dst.Get("X-Project-Id"))
}
