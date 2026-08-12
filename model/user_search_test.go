package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedUserSearchUser inserts one deterministic user for search-filter tests.
func seedUserSearchUser(t *testing.T, id int, username string) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:       id,
		Username: username,
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  fmt.Sprintf("search-%d", id),
	}).Error)
}

// TestSearchUsersFiltersActiveSubscriptionsByPlan verifies active-window and plan semantics.
func TestSearchUsersFiltersActiveSubscriptionsByPlan(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	for id, username := range []string{"plan-a", "plan-b", "expired", "future", "cancelled", "none"} {
		seedUserSearchUser(t, 8100+id, username)
	}
	require.NoError(t, DB.Create(&[]UserSubscription{
		{UserId: 8100, PlanId: 10, Status: "active", StartTime: now - 60, EndTime: now + 3600},
		{UserId: 8101, PlanId: 11, Status: "active", StartTime: now - 60, EndTime: now + 3600},
		{UserId: 8102, PlanId: 10, Status: "active", StartTime: now - 3600, EndTime: now - 1},
		{UserId: 8103, PlanId: 10, Status: "active", StartTime: now + 60, EndTime: now + 3600},
		{UserId: 8104, PlanId: 10, Status: "cancelled", StartTime: now - 60, EndTime: now + 3600},
	}).Error)

	active := true
	inactive := false
	plan10 := 10

	users, total, err := SearchUsers(UserSearchFilters{ActiveSubscription: &active, SubscriptionPlanId: &plan10}, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	assert.Equal(t, 8100, users[0].Id)

	users, total, err = SearchUsers(UserSearchFilters{ActiveSubscription: &inactive, SubscriptionPlanId: &plan10}, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.ElementsMatch(t, []int{8101, 8102, 8103, 8104, 8105}, userSearchResultIds(users))

	users, total, err = SearchUsers(UserSearchFilters{ActiveSubscription: &active}, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.ElementsMatch(t, []int{8100, 8101}, userSearchResultIds(users))

	users, total, err = SearchUsers(UserSearchFilters{ActiveSubscription: &inactive}, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 4, total)
	assert.ElementsMatch(t, []int{8102, 8103, 8104, 8105}, userSearchResultIds(users))

	users, total, err = SearchUsers(UserSearchFilters{SubscriptionPlanId: &plan10}, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, []int{8100}, userSearchResultIds(users))
}

// TestSearchUsersFiltersAllEffectiveGroupSources verifies the effective-group union.
func TestSearchUsersFiltersAllEffectiveGroupSources(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	for id, username := range []string{
		"base",
		"manual-permanent",
		"manual-unexpired",
		"manual-expired",
		"entitlement",
		"grant-list",
		"future-sub",
		"cancelled-sub",
		"expired-sub",
	} {
		seedUserSearchUser(t, 8200+id, username)
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 8200).Update("group", "base-match").Error)
	require.NoError(t, DB.Create(&[]UserGroupGrant{
		{UserId: 8201, GroupName: "target", ExpiresAt: 0},
		{UserId: 8202, GroupName: "target", ExpiresAt: now + 3600},
		{UserId: 8203, GroupName: "target", ExpiresAt: now - 1},
	}).Error)
	require.NoError(t, DB.Create(&[]UserSubscription{
		{UserId: 8204, PlanId: 20, Status: "active", StartTime: now - 60, EndTime: now + 3600, EntitlementGroup: "target"},
		{UserId: 8205, PlanId: 21, Status: "active", StartTime: now - 60, EndTime: now + 3600, GrantGroups: GroupNames{"target"}},
		{UserId: 8206, PlanId: 22, Status: "active", StartTime: now + 60, EndTime: now + 3600, GrantGroups: GroupNames{"target"}},
		{UserId: 8207, PlanId: 23, Status: "cancelled", StartTime: now - 60, EndTime: now + 3600, GrantGroups: GroupNames{"target"}},
		{UserId: 8208, PlanId: 24, Status: "active", StartTime: now - 3600, EndTime: now - 1, GrantGroups: GroupNames{"target"}},
	}).Error)

	users, total, err := SearchUsers(UserSearchFilters{
		EffectiveGroup:      "target",
		EffectiveBaseGroups: []string{"base-match"},
	}, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.ElementsMatch(t, []int{8200, 8201, 8202, 8204, 8205}, userSearchResultIds(users))
}

// TestSearchUsersEscapesGrantGroupLikeMetacharacters verifies exact JSON element matching.
func TestSearchUsersEscapesGrantGroupLikeMetacharacters(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	seedUserSearchUser(t, 8300, "exact")
	seedUserSearchUser(t, 8301, "wildcard-lookalike")
	require.NoError(t, DB.Create(&[]UserSubscription{
		{UserId: 8300, PlanId: 30, Status: "active", StartTime: now - 60, EndTime: now + 3600, GrantGroups: GroupNames{"percent%_group"}},
		{UserId: 8301, PlanId: 31, Status: "active", StartTime: now - 60, EndTime: now + 3600, GrantGroups: GroupNames{"percentXXgroup"}},
	}).Error)

	users, total, err := SearchUsers(UserSearchFilters{EffectiveGroup: "percent%_group"}, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	assert.Equal(t, 8300, users[0].Id)
}

// TestSearchUsersCombinesFiltersAndPreservesBaseGroup verifies AND composition and legacy group filtering.
func TestSearchUsersCombinesFiltersAndPreservesBaseGroup(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	seedUserSearchUser(t, 8400, "matching-user")
	seedUserSearchUser(t, 8401, "matching-disabled")
	seedUserSearchUser(t, 8402, "matching-root")
	require.NoError(t, DB.Model(&User{}).Where("id IN ?", []int{8400, 8401, 8402}).Update("group", "legacy").Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 8401).Update("status", common.UserStatusDisabled).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 8402).Update("role", common.RoleRootUser).Error)
	require.NoError(t, DB.Create(&[]UserSubscription{
		{UserId: 8400, PlanId: 40, Status: "active", StartTime: now - 60, EndTime: now + 3600},
		{UserId: 8401, PlanId: 40, Status: "active", StartTime: now - 60, EndTime: now + 3600},
		{UserId: 8402, PlanId: 40, Status: "active", StartTime: now - 60, EndTime: now + 3600},
	}).Error)

	active := true
	status := common.UserStatusEnabled
	role := common.RoleCommonUser
	planID := 40
	users, total, err := SearchUsers(UserSearchFilters{
		Keyword:            "matching",
		BaseGroup:          "legacy",
		Status:             &status,
		Role:               &role,
		ActiveSubscription: &active,
		SubscriptionPlanId: &planID,
	}, 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, users, 1)
	assert.Equal(t, 8400, users[0].Id)
}

// TestSearchUsersCountsAndPaginatesFilteredRows verifies total and descending page contents share one query.
func TestSearchUsersCountsAndPaginatesFilteredRows(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	for id := 8500; id <= 8502; id++ {
		seedUserSearchUser(t, id, fmt.Sprintf("paged-%d", id))
		require.NoError(t, DB.Create(&UserSubscription{
			UserId: id, PlanId: 50, Status: "active", StartTime: now - 60, EndTime: now + 3600,
		}).Error)
	}
	planID := 50

	firstPage, total, err := SearchUsers(UserSearchFilters{
		Keyword:            "paged-",
		SubscriptionPlanId: &planID,
	}, 0, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Equal(t, []int{8502, 8501}, userSearchResultIds(firstPage))

	secondPage, total, err := SearchUsers(UserSearchFilters{
		Keyword:            "paged-",
		SubscriptionPlanId: &planID,
	}, 2, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Equal(t, []int{8500}, userSearchResultIds(secondPage))
}

// userSearchResultIds extracts stable identifiers from one search result page.
func userSearchResultIds(users []*User) []int {
	ids := make([]int, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.Id)
	}
	return ids
}
