package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

func shouldCloseActiveWebSocketsAfterDisable(channelId int) bool {
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.SysLog(common.LogText("failed to check channel status before closing active websockets: channel_id=%d, error=%v", channelId, err))
		return true
	}
	return channel.Status != common.ChannelStatusEnabled
}

// RootUserLanguage returns the saved language of the root user, who receives
// channel notices; it is empty when the root user saved none.
func RootUserLanguage() string {
	root := model.GetRootUser()
	if root == nil {
		return ""
	}
	return root.GetSetting().Language
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string) {
	common.SysLog(common.LogText("channel %q (#%d) failed, disabling it, reason: %s", channelError.ChannelName, channelError.ChannelId, common.LocalLogPreview(reason)))

	// 自动禁用必须同时受全局开关和渠道开关控制。
	if !common.AutomaticDisableChannelEnabled || !channelError.AutoBan {
		common.SysLog(fmt.Sprintf("通道「%s」（#%d）未启用自动禁用功能，跳过禁用操作", channelError.ChannelName, channelError.ChannelId))
		return
	}

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		if shouldCloseActiveWebSocketsAfterDisable(channelError.ChannelId) {
			CloseActiveWebSocketsForChannel(channelError.ChannelId, ChannelDisabledCloseReason)
		}
		lang := RootUserLanguage()
		params := map[string]any{"Name": channelError.ChannelName, "Id": channelError.ChannelId, "Reason": reason}
		subject := i18n.Translate(lang, i18n.MsgChannelNotifyDisabledSubject, params)
		content := i18n.Translate(lang, i18n.MsgChannelNotifyDisabledContent, params)
		NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
	}
}

// EnableChannel enables one channel or multi-key entry and reports whether its state changed.
func EnableChannel(channelId int, usingKey string, channelName string) bool {
	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		if usingKey == "" {
			ClearChannelTemporaryAutoDisable(channelId)
		} else {
			ClearMultiKeyTemporaryDisable(channelId, usingKey)
		}
		subject := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled), subject, content)
	}
	return success
}

func ShouldDisableChannel(err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil || types.IsLocalBillingError(err) {
		return false
	}
	if statusCode, realUpstream := err.GetUpstreamStatusCode(); realUpstream && statusCode == http.StatusUnauthorized {
		return true
	}
	// Upstream health failures are evaluated by rolling samples, never by the legacy persistent rule.
	if isIgnoredChannelHealthError(strings.ToLower(err.Error())) || isRealUpstreamTimeout(err) {
		return false
	}
	if statusCode, realUpstream := err.GetUpstreamStatusCode(); realUpstream &&
		(statusCode == http.StatusRequestTimeout || statusCode >= http.StatusInternalServerError) {
		return false
	}
	if IsTemporaryQuotaError(err) {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
