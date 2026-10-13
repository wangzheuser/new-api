package middleware

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// recordDistributorSetupError persists a terminal error that occurred before Relay started.
func recordDistributorSetupError(c *gin.Context, channel *model.Channel, err *types.NewAPIError) {
	if c == nil || err == nil {
		return
	}
	logErr := err
	if logErr.StatusCode != http.StatusServiceUnavailable {
		logErr = types.NewErrorWithStatusCode(errors.New(err.Error()), err.GetErrorCode(), http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	}
	channelID, channelType, channelName := 0, 0, ""
	if channel != nil {
		channelID, channelType, channelName = channel.Id, channel.Type, channel.Name
	}
	service.ProcessChannelError(c, types.ChannelError{
		ChannelId: channelID, ChannelType: channelType, ChannelName: channelName,
	}, logErr, nil)
}
