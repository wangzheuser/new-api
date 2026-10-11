package model

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
}

func GetAllEnableAbilityWithChannels() ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := DB.Table("abilities").
		Select("abilities.*, channels.type as channel_type").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ?", true).
		Scan(&abilities).Error
	return abilities, err
}

func GetGroupEnabledModels(group string) []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where(commonGroupCol+" = ? and enabled = ?", group, true).Distinct("model").Pluck("model", &models)
	return models
}

func GetEnabledModels() []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where("enabled = ?", true).Distinct("model").Pluck("model", &models)
	return models
}

func GetAllEnableAbilities() []Ability {
	var abilities []Ability
	DB.Find(&abilities, "enabled = ?", true)
	return abilities
}

func getPriority(group string, model string, retry int, excludedChannelIDs []int) (int, error) {

	var priorities []int
	query := DB.Model(&Ability{}).
		Select("DISTINCT(priority)").
		Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
	if len(excludedChannelIDs) > 0 {
		query = query.Not("channel_id IN ?", excludedChannelIDs)
	}
	err := query.
		Order("priority DESC").              // 按优先级降序排序
		Pluck("priority", &priorities).Error // Pluck用于将查询的结果直接扫描到一个切片中

	if err != nil {
		// 处理错误
		return 0, err
	}

	if len(priorities) == 0 {
		// 如果没有查询到优先级，则返回错误
		return 0, errors.New("database consistency is broken")
	}

	// 确定要使用的优先级
	var priorityToUse int
	if retry >= len(priorities) {
		// 如果重试次数大于优先级数，则使用最小的优先级
		priorityToUse = priorities[len(priorities)-1]
	} else {
		priorityToUse = priorities[retry]
	}
	return priorityToUse, nil
}

func getChannelQuery(group string, model string, retry int, excludedChannelIDs []int) (*gorm.DB, error) {
	maxPrioritySubQuery := DB.Model(&Ability{}).Select("MAX(priority)").Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
	if len(excludedChannelIDs) > 0 {
		maxPrioritySubQuery = maxPrioritySubQuery.Not("channel_id IN ?", excludedChannelIDs)
	}
	channelQuery := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = (?)", group, model, true, maxPrioritySubQuery)
	if len(excludedChannelIDs) > 0 {
		channelQuery = channelQuery.Not("channel_id IN ?", excludedChannelIDs)
	}
	if retry != 0 {
		priority, err := getPriority(group, model, retry, excludedChannelIDs)
		if err != nil {
			return nil, err
		} else {
			channelQuery = DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = ?", group, model, true, priority)
			if len(excludedChannelIDs) > 0 {
				channelQuery = channelQuery.Not("channel_id IN ?", excludedChannelIDs)
			}
		}
	}

	return channelQuery, nil
}

// GetChannel 从数据库候选中选择渠道，重试时优先排除本次请求已使用的渠道。
func GetChannel(group string, model string, retry int, args ...any) (*Channel, error) {
	requestPath, usedChannelIds, excludedChannelIds, filters := parseChannelSelectionArgs(args)
	var abilities []Ability
	err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true).
		Order("priority DESC, weight DESC").Find(&abilities).Error
	if err != nil {
		return nil, err
	}
	abilities = filterAbilitiesByConstraints(abilities, model, filters)
	if len(excludedChannelIds) > 0 {
		filtered := abilities[:0]
		for _, ability := range abilities {
			if _, excluded := excludedChannelIds[ability.ChannelId]; !excluded {
				filtered = append(filtered, ability)
			}
		}
		abilities = filtered
	}
	if len(abilities) > 0 {
		priorities := make([]int64, 0)
		seen := make(map[int64]bool)
		for _, ability := range abilities {
			priority := int64(0)
			if ability.Priority != nil {
				priority = *ability.Priority
			}
			if !seen[priority] {
				seen[priority] = true
				priorities = append(priorities, priority)
			}
		}
		sort.Slice(priorities, func(i, j int) bool { return priorities[i] > priorities[j] })
		if retry >= len(priorities) {
			retry = len(priorities) - 1
		}
		targetPriority := priorities[retry]
		abilities = lo.Filter(abilities, func(ability Ability, _ int) bool {
			return (ability.Priority == nil && targetPriority == 0) || (ability.Priority != nil && *ability.Priority == targetPriority)
		})
	}
	abilities = filterAbilitiesByRequestPathAndModel(abilities, requestPath, model)
	candidateIds := make([]int, 0, len(abilities))
	for _, ability := range abilities {
		candidateIds = append(candidateIds, ability.ChannelId)
	}
	if untriedIds := preferredUntriedChannelIds(candidateIds, usedChannelIds); untriedIds != nil {
		filtered := abilities[:0]
		for _, ability := range abilities {
			if _, ok := untriedIds[ability.ChannelId]; ok {
				filtered = append(filtered, ability)
			}
		}
		abilities = filtered
	}
	channel := Channel{}
	if len(abilities) > 0 {
		// Randomly choose one
		weightSum := uint(0)
		for _, ability_ := range abilities {
			weightSum += ability_.Weight + 10
		}
		// Randomly choose one
		weight := common.GetRandomInt(int(weightSum))
		for _, ability_ := range abilities {
			weight -= int(ability_.Weight) + 10
			//log.Printf("weight: %d, ability weight: %d", weight, *ability_.Weight)
			if weight <= 0 {
				channel.Id = ability_.ChannelId
				break
			}
		}
	} else {
		return nil, nil
	}
	err = DB.First(&channel, "id = ?", channel.Id).Error
	return &channel, err
}

func parseChannelSelectionArgs(args []any) (string, map[int]struct{}, map[int]struct{}, []dto.ChannelFilter) {
	var requestPath string
	var usedChannelIds, excludedChannelIds map[int]struct{}
	var filters []dto.ChannelFilter
	if len(args) >= 3 {
		requestPath, _ = args[0].(string)
		usedChannelIds, _ = args[1].(map[int]struct{})
		excludedChannelIds, _ = args[2].(map[int]struct{})
		args = args[3:]
	}
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			requestPath = value
		case map[int]struct{}:
			if usedChannelIds == nil && excludedChannelIds == nil {
				usedChannelIds = value
			} else {
				excludedChannelIds = value
			}
		case []dto.ChannelFilter:
			filters = value
		}
	}
	return requestPath, usedChannelIds, excludedChannelIds, filters
}

// filterExcludedChannelIds 永久排除本次路由不允许命中的渠道。
func filterExcludedChannelIds(channelIds []int, excludedChannelIds map[int]struct{}) []int {
	if len(channelIds) == 0 || len(excludedChannelIds) == 0 {
		return channelIds
	}
	filtered := channelIds[:0]
	for _, channelId := range channelIds {
		if _, excluded := excludedChannelIds[channelId]; !excluded {
			filtered = append(filtered, channelId)
		}
	}
	return filtered
}

// preferredUntriedChannelIds 返回未使用候选；全部用过时返回 nil 以允许兜底复用。
func preferredUntriedChannelIds(candidateIds []int, usedChannelIds map[int]struct{}) map[int]struct{} {
	if len(usedChannelIds) == 0 {
		return nil
	}
	untriedIds := make(map[int]struct{}, len(candidateIds))
	for _, channelId := range candidateIds {
		if _, used := usedChannelIds[channelId]; !used {
			untriedIds[channelId] = struct{}{}
		}
	}
	if len(untriedIds) == 0 {
		return nil
	}
	return untriedIds
}

// filterAbilitiesByRequestPathAndModel restricts candidates by request path and
// model for the DB (non-memory-cache) selection path. Only Advanced Custom
// (type 58) channels are path-checked: kept only when one of their routes matches
// requestPath and model; all other channel types always pass. When requestPath is
// empty, filtering is skipped.
func filterAbilitiesByRequestPathAndModel(abilities []Ability, requestPath string, model string) []Ability {
	if requestPath == "" || len(abilities) == 0 {
		return abilities
	}

	channelIds := make([]int, 0, len(abilities))
	seen := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, ok := seen[ability.ChannelId]; ok {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIds = append(channelIds, ability.ChannelId)
	}

	var channels []*Channel
	if err := DB.Where("id IN ?", channelIds).Find(&channels).Error; err != nil {
		return abilities
	}

	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		var channel *Channel
		for _, candidate := range channels {
			if candidate.Id == ability.ChannelId {
				channel = candidate
				break
			}
		}
		if channel == nil {
			continue
		}
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			filtered = append(filtered, ability)
			continue
		}
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil && config.SupportsPathForModel(requestPath, model) {
			filtered = append(filtered, ability)
		}
	}
	return filtered
}

// filterAbilitiesByConstraints applies the same channel constraints as the cache path.
func filterAbilitiesByConstraints(abilities []Ability, modelName string, filters []dto.ChannelFilter) []Ability {
	if len(abilities) == 0 || len(filters) == 0 {
		return abilities
	}
	channelIDs := make([]int, 0, len(abilities))
	for _, ability := range abilities {
		channelIDs = append(channelIDs, ability.ChannelId)
	}
	var channels []Channel
	if err := DB.Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
		return nil
	}
	byID := make(map[int]*Channel, len(channels))
	for i := range channels {
		byID[channels[i].Id] = &channels[i]
	}
	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if ok, _ := ChannelSatisfiesFilters(byID[ability.ChannelId], modelName, filters); ok {
			filtered = append(filtered, ability)
		}
	}
	return filtered
}

func (channel *Channel) AddAbilities(tx *gorm.DB) error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}
	if len(abilities) == 0 {
		return nil
	}
	// choose DB or provided tx
	useDB := DB
	if tx != nil {
		useDB = tx
	}
	for _, chunk := range lo.Chunk(abilities, 50) {
		err := useDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {
	isNewTx := false
	// 如果没有传入事务，创建新的事务
	if tx == nil {
		tx = DB.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		isNewTx = true
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()
	}

	// First delete all abilities of this channel
	err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
	if err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}

	// Then add new abilities
	models_ := channel.GetModels()
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}

	if len(abilities) > 0 {
		for _, chunk := range lo.Chunk(abilities, 50) {
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
			if err != nil {
				if isNewTx {
					tx.Rollback()
				}
				return err
			}
		}
	}

	// 如果是新创建的事务，需要提交
	if isNewTx {
		return tx.Commit().Error
	}

	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityStatusByTag(tag string, status bool) error {
	return DB.Model(&Ability{}).Where("tag = ?", tag).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityByTag(tag string, newTag *string, priority *int64, weight *uint) error {
	ability := Ability{}
	if newTag != nil {
		ability.Tag = newTag
	}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return DB.Model(&Ability{}).Where("tag = ?", tag).Updates(ability).Error
}

var fixLock = sync.Mutex{}

func FixAbility() (int, int, error) {
	lock := fixLock.TryLock()
	if !lock {
		return 0, 0, common.NewMessage("A repair task is already running. Please try again later")
	}
	defer fixLock.Unlock()

	// truncate abilities table
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		err := DB.Exec("DELETE FROM abilities").Error
		if err != nil {
			common.SysLog(common.LogText("Delete abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	} else {
		err := DB.Exec("TRUNCATE TABLE abilities").Error
		if err != nil {
			common.SysLog(common.LogText("Truncate abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	}
	var channels []*Channel
	// Find all channels
	err := DB.Model(&Channel{}).Find(&channels).Error
	if err != nil {
		return 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, nil
	}
	successCount := 0
	failCount := 0
	for _, chunk := range lo.Chunk(channels, 50) {
		ids := lo.Map(chunk, func(c *Channel, _ int) int { return c.Id })
		// Delete all abilities of this channel
		err = DB.Where("channel_id IN ?", ids).Delete(&Ability{}).Error
		if err != nil {
			common.SysLog(common.LogText("Delete abilities failed: %s", err.Error()))
			failCount += len(chunk)
			continue
		}
		// Then add new abilities
		for _, channel := range chunk {
			err = channel.AddAbilities(nil)
			if err != nil {
				common.SysLog(common.LogText("Add abilities for channel %d failed: %s", channel.Id, err.Error()))
				failCount++
			} else {
				successCount++
			}
		}
	}
	InitChannelCache()
	return successCount, failCount, nil
}
