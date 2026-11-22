package openai

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelMappingEffortReachesUpstream(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	settings.PassThroughRequestEnabled = false
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeOpenRouter} {
		for _, responses := range []bool{false, true} {
			for _, suffix := range []string{"", "-high", "@effort:max"} {
				t.Run(fmt.Sprintf("channel=%d/responses=%t/suffix=%s", channelType, responses, suffix), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Set("model_mapping", `{"gpt-6.2-sol":"gpt-6-astra@effort:low"}`)
					origin := "gpt-6.2-sol" + suffix
					var request dto.Request = &dto.GeneralOpenAIRequest{Model: origin, ReasoningEffort: "none"}
					format := types.RelayFormatOpenAI
					if responses {
						request = &dto.OpenAIResponsesRequest{Model: origin, Reasoning: &dto.Reasoning{Effort: "none"}}
						format = types.RelayFormatOpenAIResponses
					}
					info := &relaycommon.RelayInfo{OriginModelName: origin, Request: request, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: channelType, UpstreamModelName: origin}}
					require.NoError(t, helper.ModelMappedHelper(c, info, request))
					require.NoError(t, helper.ApplyReasoningModelSuffix(c, info, request))
					adaptor := &Adaptor{}
					var outbound any
					var err error
					switch req := request.(type) {
					case *dto.GeneralOpenAIRequest:
						outbound, err = adaptor.ConvertOpenAIRequest(c, info, req)
					case *dto.OpenAIResponsesRequest:
						outbound, err = adaptor.ConvertOpenAIResponsesRequest(c, info, *req)
					}
					require.NoError(t, err)
					body, err := common.Marshal(outbound)
					require.NoError(t, err)
					var payload map[string]any
					require.NoError(t, common.Unmarshal(body, &payload))
					assert.Equal(t, "gpt-6-astra", payload["model"])
					if responses || channelType == constant.ChannelTypeOpenRouter {
						reasoning, ok := payload["reasoning"].(map[string]any)
						require.True(t, ok)
						assert.Equal(t, "low", reasoning["effort"])
					} else {
						assert.Equal(t, "low", payload["reasoning_effort"])
					}
				})
			}
		}
	}
}
