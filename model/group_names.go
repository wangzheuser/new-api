package model

import (
	"database/sql/driver"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// GroupNames stores a normalized group-name list as JSON in a database TEXT column.
type GroupNames []string

// NormalizeGroupNames trims, deduplicates, and sorts group names.
func NormalizeGroupNames(groups GroupNames) GroupNames {
	unique := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group != "" {
			unique[group] = struct{}{}
		}
	}
	normalized := make(GroupNames, 0, len(unique))
	for group := range unique {
		normalized = append(normalized, group)
	}
	sort.Strings(normalized)
	return normalized
}

// MergeGroupNames returns the normalized union of two group-name lists.
func MergeGroupNames(left, right GroupNames) GroupNames {
	groups := make(GroupNames, 0, len(left)+len(right))
	groups = append(groups, left...)
	groups = append(groups, right...)
	return NormalizeGroupNames(groups)
}

// Value serializes group names for database storage.
func (groups GroupNames) Value() (driver.Value, error) {
	normalized := NormalizeGroupNames(groups)
	data, err := common.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

// Scan deserializes group names returned by supported database drivers.
func (groups *GroupNames) Scan(value interface{}) error {
	if groups == nil {
		return fmt.Errorf("GroupNames scan target is nil")
	}
	if value == nil {
		*groups = GroupNames{}
		return nil
	}
	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return fmt.Errorf("unsupported GroupNames database value %T", value)
	}
	if len(data) == 0 {
		*groups = GroupNames{}
		return nil
	}
	var decoded GroupNames
	if err := common.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*groups = NormalizeGroupNames(decoded)
	return nil
}
