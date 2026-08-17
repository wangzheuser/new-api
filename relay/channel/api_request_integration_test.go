package channel_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoApiRequest_StreamPingsWhileWaitingForUpstreamHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	settings := operation_setting.GetGeneralSetting()
	oldEnabled := settings.PingIntervalEnabled
	oldSeconds := settings.PingIntervalSeconds
	settings.PingIntervalEnabled = true
	settings.PingIntervalSeconds = 1
	t.Cleanup(func() {
		settings.PingIntervalEnabled = oldEnabled
		settings.PingIntervalSeconds = oldSeconds
	})

	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-releaseUpstream
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","stream":true}`))
	info := openAIRelayInfo(upstream.URL, true)
	resultCh := make(chan error, 1)
	go func() {
		resp, err := channel.DoApiRequest(&openai.Adaptor{}, c, info, strings.NewReader(`{"model":"test","stream":true}`))
		if resp != nil {
			_ = resp.Body.Close()
		}
		resultCh <- err
	}()
	time.Sleep(1500 * time.Millisecond)
	assert.Contains(t, recorder.Body.String(), ": PING\n\n")
	close(releaseUpstream)
	select {
	case err := <-resultCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not finish after upstream response headers arrived")
	}
	info.StopStreamPinger()
}

func TestDoApiRequest_StreamCommitsHeadersBeforeUpstreamResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	upstreamStarted := make(chan struct{})
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(upstreamStarted)
		<-releaseUpstream
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handlerDone := make(chan error, 1)
	router := gin.New()
	router.POST("/relay", func(c *gin.Context) {
		resp, err := channel.DoApiRequest(&openai.Adaptor{}, c, openAIRelayInfo(upstream.URL, true), strings.NewReader(`{"model":"test","stream":true}`))
		if resp != nil {
			_ = resp.Body.Close()
		}
		handlerDone <- err
	})
	downstream := httptest.NewServer(router)
	defer downstream.Close()

	responseCh := make(chan *http.Response, 1)
	requestErrCh := make(chan error, 1)
	go func() {
		resp, err := http.Post(downstream.URL+"/relay", "application/json", strings.NewReader(`{"model":"test","stream":true}`))
		if err != nil {
			requestErrCh <- err
			return
		}
		responseCh <- resp
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not start")
	}

	var downstreamResp *http.Response
	select {
	case downstreamResp = <-responseCh:
	case err := <-requestErrCh:
		t.Fatalf("downstream request failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("stream response headers were not committed before the upstream response")
	}
	require.NotNil(t, downstreamResp)
	assert.Equal(t, http.StatusOK, downstreamResp.StatusCode)
	assert.Equal(t, "text/event-stream", downstreamResp.Header.Get("Content-Type"))

	close(releaseUpstream)
	_, err := io.ReadAll(downstreamResp.Body)
	require.NoError(t, err)
	require.NoError(t, downstreamResp.Body.Close())
	select {
	case err := <-handlerDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("relay handler did not finish")
	}
}

func TestDoApiRequest_ClientCancellationCancelsUpstreamRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	upstreamStarted := make(chan struct{})
	stopUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		select {
		case <-r.Context().Done():
		case <-stopUpstream:
		}
	}))
	defer func() {
		close(stopUpstream)
		upstream.Close()
	}()

	requestCtx, cancel := context.WithCancel(context.Background())
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test"}`)).WithContext(requestCtx)

	resultCh := make(chan error, 1)
	go func() {
		resp, err := channel.DoApiRequest(&openai.Adaptor{}, ctx, openAIRelayInfo(upstream.URL, false), strings.NewReader(`{"model":"test"}`))
		if resp != nil {
			_ = resp.Body.Close()
		}
		resultCh <- err
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()

	select {
	case err := <-resultCh:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "do request failed")
	case <-time.After(2 * time.Second):
		t.Fatal("relay request did not return after cancellation")
	}
}

// openAIRelayInfo 构造真实 HTTP 中继所需的最小上下文。
func openAIRelayInfo(baseURL string, stream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		IsStream:       stream,
		RelayMode:      relayconstant.RelayModeChatCompletions,
		RelayFormat:    types.RelayFormatOpenAI,
		RequestURLPath: "/v1/chat/completions",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    baseURL,
			ApiKey:            "test-key",
			UpstreamModelName: "test",
		},
	}
}
