package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContextBudgetDoesNotIncreaseBilling keeps conservative context accounting out of charges.
func TestContextBudgetDoesNotIncreaseBilling(t *testing.T) {
	c := newEntitlementBillingContext()
	info := inputPolicyBillingInfo()
	info.ContextTruncation.Before = 116009
	info.ContextTruncation.BudgetBefore = 280000
	original := &dto.Usage{PromptTokens: 1000, CompletionTokens: 40, TotalTokens: 1040, UsageSemantic: "openai"}
	_, changed := relaycommon.RewriteClientContextUsageJSON([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":40,"total_tokens":1040}}`), types.RelayFormatOpenAI, info.ContextTruncation, 200)
	require.True(t, changed)
	assert.EqualValues(t, 280000, info.ContextTruncation.Reported)
	billed := applyContextTruncationBilling(c, info, original)
	assert.Equal(t, 116009, billed.PromptTokens)
	assert.Equal(t, 1000, original.PromptTokens)
	assert.Equal(t, 116009, info.ContextTruncation.Billed)
}
