package channel

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func applyAgentHeaderPreset(info *common.RelayInfo, c *gin.Context, headers map[string]string) {
	if info.ChannelMeta == nil || info.ChannelSetting.AgentHeaderPreset == nil {
		return
	}
	preset := info.ChannelSetting.AgentHeaderPreset
	identityNames := preset.IdentityHeaderNames()
	if len(identityNames) == 0 {
		return
	}
	// A wildcard may have copied curl's UA. Select the identity as a group before explicit overrides.
	for _, name := range identityNames {
		delete(headers, name)
	}
	if !info.IsChannelTest && c != nil && c.Request != nil && matchesAgentClient(preset.Client, c.Request.UserAgent(), c.Request.Header.Get("originator")) {
		for name := range c.Request.Header {
			lower := strings.ToLower(name)
			pass := slices.Contains(identityNames, lower)
			switch preset.Client {
			case "codex":
				pass = pass || slices.Contains([]string{"session_id", "thread_id", "session-id", "thread-id"}, lower) || strings.HasPrefix(lower, "x-codex-")
			case "claude":
				pass = pass || strings.HasPrefix(lower, "x-stainless-") || strings.HasPrefix(lower, "x-claude-code-")
			}
			if pass && !shouldSkipPassthroughHeader(name) {
				headers[lower] = c.Request.Header.Get(name)
			}
		}
		return
	}
	for name, value := range preset.Headers {
		headers[strings.ToLower(name)] = value
	}
	// An SDK can request capabilities without identifying itself as Claude Code.
	if preset.Client == "claude" && !info.IsChannelTest && c != nil && c.Request != nil {
		for _, name := range []string{"anthropic-beta", "anthropic-version"} {
			if value := c.Request.Header.Get(name); value != "" {
				headers[name] = value
			}
		}
	}
}

func matchesAgentClient(client, userAgent, originator string) bool {
	product, version, ok := strings.Cut(strings.TrimSpace(userAgent), "/")
	if !ok || strings.TrimSpace(version) == "" {
		return false
	}
	product = strings.ToLower(product)
	if client == "claude" {
		return product == "claude-cli"
	}
	if client != "codex" {
		return false
	}
	switch product {
	case "codex_cli_rs", "codex-tui", "codex_vscode", "codex_exec", "codex_sdk_ts", "codex_atlas", "codex_chatgpt_desktop":
	default:
		if !strings.HasPrefix(product, "codex ") {
			return false
		}
	}
	return originator == "" || strings.EqualFold(strings.TrimSpace(originator), product)
}
