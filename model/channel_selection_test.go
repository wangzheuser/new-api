package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPreferredUntriedChannelIds 验证有备选时不重复渠道，全部用过时允许兜底复用。
func TestPreferredUntriedChannelIds(t *testing.T) {
	used := map[int]struct{}{14: {}}

	assert.Equal(t, map[int]struct{}{16: {}}, preferredUntriedChannelIds([]int{14, 16}, used))
	assert.Nil(t, preferredUntriedChannelIds([]int{14}, used))
}
