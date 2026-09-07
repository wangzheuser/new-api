package common

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestResolveMappedModel keeps routing aliases consistent with the submitted model.
func TestResolveMappedModel(t *testing.T) {
	for _, tc := range []struct {
		mapping, model, want string
		invalid              bool
	}{
		{`{"alias":"MODEL_A","other":"MODEL_A"}`, "alias", "MODEL_A", false},
		{`{"alias":"MODEL_A","other":"MODEL_A"}`, "other", "MODEL_A", false},
		{`{"a":"b","b":"c","c":"c"}`, "a", "c", false},
		{`{"a":"b","b":"a"}`, "a", "", true},
		{`{"a":123}`, "a", "", true},
		{"", "a", "a", false},
	} {
		mapped, err := ResolveMappedModel(tc.mapping, tc.model)
		if tc.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, tc.want, mapped)
	}
}
