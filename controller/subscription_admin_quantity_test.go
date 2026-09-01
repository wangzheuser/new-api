package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveAdminSubscriptionQuantity verifies the compatible default and management API bounds.
func TestResolveAdminSubscriptionQuantity(t *testing.T) {
	maximum := model.MaxAdminSubscriptionQuantity
	testCases := []struct {
		name     string
		value    *int
		expected int
		invalid  bool
	}{
		{name: "omitted defaults to one", expected: 1},
		{name: "minimum", value: common.GetPointer(1), expected: 1},
		{name: "maximum", value: &maximum, expected: maximum},
		{name: "zero", value: common.GetPointer(0), invalid: true},
		{name: "negative", value: common.GetPointer(-1), invalid: true},
		{name: "above maximum", value: common.GetPointer(maximum + 1), invalid: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := resolveAdminSubscriptionQuantity(testCase.value)
			if testCase.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, actual)
		})
	}
}
