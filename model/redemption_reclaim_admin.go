package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	RedemptionReclaimPreviewEligible       = "eligible"
	RedemptionReclaimPreviewAlreadyEnabled = "already_enabled"
	RedemptionReclaimPreviewManualReview   = "manual_review"
	RedemptionReclaimPreviewNotFound       = "not_found"
	RedemptionReclaimPreviewNoExpiry       = "no_expiry"
	RedemptionReclaimPreviewExpiredUnused  = "expired_unused"
	RedemptionReclaimPreviewConflict       = "conflict"

	maxRedemptionReclaimPreviewKeys       = 1000
	maxRedemptionReclaimKeyBytes          = 128
	maxRedemptionReclaimHistoricalLogRows = 100000
	maxRedemptionReclaimReviewNoteBytes   = 2000
)

var (
	ErrRedemptionReclaimPreviewConflict = errors.New("redemption reclaim preview changed")
	ErrRedemptionReclaimNotManualReview = errors.New("redemption is not awaiting manual review")
	ErrRedemptionReclaimReviewInvalid   = errors.New("invalid redemption reclaim review")
	ErrRedemptionReclaimReconstruct     = errors.New("automatic historical reconstruction failed")
)

type RedemptionReclaimPreviewItem struct {
	Key              string `json:"key"`
	Id               int    `json:"id,omitempty"`
	Name             string `json:"name,omitempty"`
	RedemptionStatus int    `json:"redemption_status,omitempty"`
	Quota            int    `json:"quota"`
	UsedUserId       int    `json:"used_user_id"`
	Username         string `json:"username,omitempty"`
	RedeemedTime     int64  `json:"redeemed_time"`
	ExpiredTime      int64  `json:"expired_time"`
	RemainingQuota   int    `json:"remaining_quota"`
	Result           string `json:"result"`
	Reason           string `json:"reason,omitempty"`
	CanEnable        bool   `json:"can_enable"`

	reclaimStatus      int
	reclaimEnabledTime int64
	reclaimedQuota     int
	reclaimedTime      int64
	reclaimError       string
}

type RedemptionReclaimPreviewSummary struct {
	CodeCount      int `json:"code_count"`
	UserCount      int `json:"user_count"`
	TotalQuota     int `json:"total_quota"`
	DeadlineCount  int `json:"deadline_count"`
	ExceptionCount int `json:"exception_count"`
}

type RedemptionReclaimPreview struct {
	Snapshot string                          `json:"snapshot"`
	Summary  RedemptionReclaimPreviewSummary `json:"summary"`
	Items    []RedemptionReclaimPreviewItem  `json:"items"`

	userSnapshots map[int]redemptionReclaimUserSnapshot
}

type RedemptionReclaimEnableResult struct {
	EnabledCount      int `json:"enabled_count"`
	ManualReviewCount int `json:"manual_review_count"`
	SkippedCount      int `json:"skipped_count"`
}

type RedemptionReclaimReviewResult struct {
	UserId         int   `json:"user_id"`
	RedemptionIds  []int `json:"redemption_ids"`
	UpdatedCount   int   `json:"updated_count"`
	PendingCount   int   `json:"pending_count"`
	CompletedCount int   `json:"completed_count"`
	ReclaimedQuota int   `json:"reclaimed_quota"`
}

type redemptionReclaimUserSnapshot struct {
	Id        int `json:"id"`
	Quota     int `json:"quota"`
	UsedQuota int `json:"used_quota"`
}

type redemptionReplayLot struct {
	redemption *Redemption
	remaining  int
	expired    bool
}

type redemptionReplaySegment struct {
	redemptionId int
	quota        int
	expiredTime  int64
}

type redemptionReplayEvent struct {
	timestamp  int64
	kind       int
	redemption *Redemption
	log        *Log
	source     string
}

type redemptionLogOther struct {
	BillingSource string `json:"billing_source"`
}

func normalizeRedemptionReclaimKeys(keys []string) ([]string, error) {
	if len(keys) > maxRedemptionReclaimPreviewKeys {
		return nil, fmt.Errorf("at most %d redemption codes may be processed at once", maxRedemptionReclaimPreviewKeys)
	}
	seen := make(map[string]struct{}, len(keys))
	normalized := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if len(key) > maxRedemptionReclaimKeyBytes {
			return nil, fmt.Errorf("redemption code exceeds %d bytes", maxRedemptionReclaimKeyBytes)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	if len(normalized) == 0 {
		return nil, errors.New("at least one redemption code is required")
	}
	if len(normalized) > maxRedemptionReclaimPreviewKeys {
		return nil, fmt.Errorf("at most %d redemption codes may be processed at once", maxRedemptionReclaimPreviewKeys)
	}
	return normalized, nil
}

func redemptionReclaimSnapshot(
	items []RedemptionReclaimPreviewItem,
	users map[int]redemptionReclaimUserSnapshot,
) (string, error) {
	type snapshotItem struct {
		Key                string `json:"key"`
		Id                 int    `json:"id"`
		RedemptionStatus   int    `json:"redemption_status"`
		Quota              int    `json:"quota"`
		UsedUserId         int    `json:"used_user_id"`
		RedeemedTime       int64  `json:"redeemed_time"`
		ExpiredTime        int64  `json:"expired_time"`
		RemainingQuota     int    `json:"remaining_quota"`
		Result             string `json:"result"`
		Reason             string `json:"reason"`
		CanEnable          bool   `json:"can_enable"`
		ReclaimStatus      int    `json:"reclaim_status"`
		ReclaimEnabledTime int64  `json:"reclaim_enabled_time"`
		ReclaimedQuota     int    `json:"reclaimed_quota"`
		ReclaimedTime      int64  `json:"reclaimed_time"`
		ReclaimError       string `json:"reclaim_error"`
	}

	canonicalItems := make([]snapshotItem, 0, len(items))
	for i := range items {
		canonicalItems = append(canonicalItems, snapshotItem{
			Key:                items[i].Key,
			Id:                 items[i].Id,
			RedemptionStatus:   items[i].RedemptionStatus,
			Quota:              items[i].Quota,
			UsedUserId:         items[i].UsedUserId,
			RedeemedTime:       items[i].RedeemedTime,
			ExpiredTime:        items[i].ExpiredTime,
			RemainingQuota:     items[i].RemainingQuota,
			Result:             items[i].Result,
			Reason:             items[i].Reason,
			CanEnable:          items[i].CanEnable,
			ReclaimStatus:      items[i].reclaimStatus,
			ReclaimEnabledTime: items[i].reclaimEnabledTime,
			ReclaimedQuota:     items[i].reclaimedQuota,
			ReclaimedTime:      items[i].reclaimedTime,
			ReclaimError:       items[i].reclaimError,
		})
	}
	sort.Slice(canonicalItems, func(i, j int) bool { return canonicalItems[i].Key < canonicalItems[j].Key })

	canonicalUsers := make([]redemptionReclaimUserSnapshot, 0, len(users))
	for _, user := range users {
		canonicalUsers = append(canonicalUsers, user)
	}
	sort.Slice(canonicalUsers, func(i, j int) bool { return canonicalUsers[i].Id < canonicalUsers[j].Id })

	payload := struct {
		Items []snapshotItem                  `json:"items"`
		Users []redemptionReclaimUserSnapshot `json:"users"`
	}{
		Items: canonicalItems,
		Users: canonicalUsers,
	}
	data, err := common.Marshal(payload)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:]), nil
}

func logBillingSource(log *Log) string {
	if log == nil || log.Other == "" {
		return ""
	}
	other := redemptionLogOther{}
	if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
		return ""
	}
	if other.BillingSource == "wallet" || other.BillingSource == "subscription" {
		return other.BillingSource
	}
	return ""
}

func getUserLoggedNetQuota(logDB *gorm.DB, userId int) (int64, error) {
	type quotaTotal struct {
		ConsumeQuota int64 `gorm:"column:consume_quota"`
		RefundQuota  int64 `gorm:"column:refund_quota"`
	}
	total := quotaTotal{}
	if err := logDB.Model(&Log{}).
		Select(
			"COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0) AS consume_quota, "+
				"COALESCE(SUM(CASE WHEN type = ? THEN quota ELSE 0 END), 0) AS refund_quota",
			LogTypeConsume,
			LogTypeRefund,
		).
		Where("user_id = ? AND type IN ?", userId, []int{LogTypeConsume, LogTypeRefund}).
		Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.ConsumeQuota - total.RefundQuota, nil
}

func reconstructRedemptionRemainders(
	db *gorm.DB,
	logDB *gorm.DB,
	user *User,
	candidates []*Redemption,
	now int64,
) (map[int]int, error) {
	if user == nil || len(candidates) == 0 {
		return nil, errors.New("historical redemption data is incomplete")
	}
	if !common.LogConsumeEnabled {
		return nil, errors.New("consumption logging is disabled")
	}

	loggedNetQuota, err := getUserLoggedNetQuota(logDB, user.Id)
	if err != nil {
		return nil, fmt.Errorf("query consumption log totals: %w", err)
	}
	if loggedNetQuota != int64(user.UsedQuota) {
		return nil, fmt.Errorf("consumption logs do not reconcile with used quota")
	}

	candidateIds := make(map[int]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateIds[candidate.Id] = struct{}{}
	}

	var existing []Redemption
	if err := db.Where("used_user_id = ? AND reclaim_status != ?", user.Id, RedemptionReclaimStatusDisabled).
		Order("redeemed_time, id").
		Find(&existing).Error; err != nil {
		return nil, err
	}
	for i := range existing {
		if _, retrying := candidateIds[existing[i].Id]; retrying {
			continue
		}
		if existing[i].ReclaimStatus == RedemptionReclaimStatusManualReview {
			return nil, errors.New("the user already has limited quota awaiting manual review")
		}
	}

	redemptions := make([]*Redemption, 0, len(existing)+len(candidates))
	minimumTime := int64(0)
	for i := range existing {
		if _, retrying := candidateIds[existing[i].Id]; retrying {
			continue
		}
		if existing[i].RedeemedTime <= 0 || existing[i].Quota < 0 || existing[i].ExpiredTime <= 0 {
			continue
		}
		redemptions = append(redemptions, &existing[i])
		if minimumTime == 0 || existing[i].RedeemedTime < minimumTime {
			minimumTime = existing[i].RedeemedTime
		}
	}
	for _, candidate := range candidates {
		if candidate.RedeemedTime <= 0 || candidate.Quota < 0 || candidate.ExpiredTime <= 0 {
			return nil, errors.New("historical redemption data is incomplete")
		}
		redemptions = append(redemptions, candidate)
		if minimumTime == 0 || candidate.RedeemedTime < minimumTime {
			minimumTime = candidate.RedeemedTime
		}
	}

	var logs []Log
	if err := logDB.Select("created_at", "type", "quota", "other", "request_id").
		Where("user_id = ? AND type IN ? AND created_at >= ?", user.Id, []int{LogTypeConsume, LogTypeRefund}, minimumTime).
		Order("created_at, request_id").
		Limit(maxRedemptionReclaimHistoricalLogRows + 1).
		Find(&logs).Error; err != nil {
		return nil, err
	}
	if len(logs) > maxRedemptionReclaimHistoricalLogRows {
		return nil, errors.New("historical consumption log volume exceeds the automatic reconstruction limit")
	}

	candidateTimes := make(map[int64]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateTimes[candidate.RedeemedTime] = struct{}{}
	}
	timestampKinds := make(map[int64]int)
	for i := range logs {
		source := logBillingSource(&logs[i])
		walletEvent := (logs[i].Type == LogTypeConsume && source != "subscription") ||
			(logs[i].Type == LogTypeRefund && source == "wallet")
		if !walletEvent || logs[i].Quota == 0 {
			continue
		}
		if logs[i].Quota < 0 {
			return nil, errors.New("a billing log contains a negative quota")
		}
		if _, ambiguous := candidateTimes[logs[i].CreatedAt]; ambiguous {
			return nil, errors.New("redemption and wallet consumption share the same second")
		}
		kind := 1
		if logs[i].Type == LogTypeRefund {
			kind = 2
		}
		timestampKinds[logs[i].CreatedAt] |= kind
		if timestampKinds[logs[i].CreatedAt] == 3 {
			return nil, errors.New("wallet consumption and refund ordering is ambiguous")
		}
	}

	events := make([]redemptionReplayEvent, 0, len(redemptions)+len(logs))
	for _, redemption := range redemptions {
		events = append(events, redemptionReplayEvent{
			timestamp:  redemption.RedeemedTime,
			kind:       0,
			redemption: redemption,
		})
	}
	for i := range logs {
		events = append(events, redemptionReplayEvent{
			timestamp: logs[i].CreatedAt,
			kind:      1,
			log:       &logs[i],
			source:    logBillingSource(&logs[i]),
		})
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].timestamp != events[j].timestamp {
			return events[i].timestamp < events[j].timestamp
		}
		if events[i].kind != events[j].kind {
			return events[i].kind < events[j].kind
		}
		if events[i].redemption != nil && events[j].redemption != nil {
			return events[i].redemption.Id < events[j].redemption.Id
		}
		return false
	})

	lots := make(map[int]*redemptionReplayLot, len(redemptions))
	consumed := make([]redemptionReplaySegment, 0)
	expiredRemainders := make(map[int]int)
	expireBefore := func(timestamp int64) {
		for id, lot := range lots {
			if lot.expired || lot.redemption.ExpiredTime >= timestamp {
				continue
			}
			expiredRemainders[id] = lot.remaining
			lot.remaining = 0
			lot.expired = true
		}
	}

	for i := range events {
		event := &events[i]
		expireBefore(event.timestamp)
		if event.redemption != nil {
			lots[event.redemption.Id] = &redemptionReplayLot{
				redemption: event.redemption,
				remaining:  event.redemption.Quota,
			}
			continue
		}
		if event.log == nil || event.log.Quota == 0 {
			continue
		}

		if event.log.Type == LogTypeConsume {
			if event.source == "subscription" {
				continue
			}
			remaining := event.log.Quota
			active := make([]*redemptionReplayLot, 0, len(lots))
			for _, lot := range lots {
				if !lot.expired && lot.remaining > 0 && lot.redemption.ExpiredTime >= event.timestamp {
					active = append(active, lot)
				}
			}
			sort.Slice(active, func(i, j int) bool {
				if active[i].redemption.ExpiredTime != active[j].redemption.ExpiredTime {
					return active[i].redemption.ExpiredTime < active[j].redemption.ExpiredTime
				}
				if active[i].redemption.RedeemedTime != active[j].redemption.RedeemedTime {
					return active[i].redemption.RedeemedTime < active[j].redemption.RedeemedTime
				}
				return active[i].redemption.Id < active[j].redemption.Id
			})
			for _, lot := range active {
				if remaining == 0 {
					break
				}
				take := lot.remaining
				if take > remaining {
					take = remaining
				}
				lot.remaining -= take
				remaining -= take
				consumed = append(consumed, redemptionReplaySegment{
					redemptionId: lot.redemption.Id,
					quota:        take,
					expiredTime:  lot.redemption.ExpiredTime,
				})
			}
			if remaining > 0 {
				consumed = append(consumed, redemptionReplaySegment{quota: remaining})
			}
			continue
		}

		if event.log.Type != LogTypeRefund || event.source != "wallet" {
			continue
		}
		remaining := event.log.Quota
		for index := len(consumed) - 1; index >= 0 && remaining > 0; index-- {
			segment := &consumed[index]
			if segment.quota <= 0 {
				continue
			}
			refund := segment.quota
			if refund > remaining {
				refund = remaining
			}
			segment.quota -= refund
			remaining -= refund
			if segment.redemptionId == 0 || segment.expiredTime < event.timestamp {
				continue
			}
			lot := lots[segment.redemptionId]
			if lot == nil || lot.expired {
				continue
			}
			capacity := lot.redemption.Quota - lot.remaining
			if refund > capacity {
				refund = capacity
			}
			lot.remaining += refund
		}
	}
	expireBefore(now)

	for i := range existing {
		redemption := &existing[i]
		lot := lots[redemption.Id]
		if lot == nil || redemption.ReclaimStatus == RedemptionReclaimStatusManualReview {
			continue
		}
		switch redemption.ReclaimStatus {
		case RedemptionReclaimStatusPending:
			expected := lot.remaining
			if redemption.ExpiredTime < now {
				expected = expiredRemainders[redemption.Id]
			}
			if expected != redemption.ReclaimRemainingQuota {
				return nil, errors.New("existing limited quota attribution cannot be reproduced")
			}
		case RedemptionReclaimStatusCompleted:
			if expiredRemainders[redemption.Id] != redemption.ReclaimedQuota {
				return nil, errors.New("existing reclaimed quota cannot be reproduced")
			}
		case RedemptionReclaimStatusError:
			if expiredRemainders[redemption.Id] != redemption.ReclaimRemainingQuota {
				return nil, errors.New("existing reclaim error attribution cannot be reproduced")
			}
		}
	}

	result := make(map[int]int, len(candidates))
	for _, candidate := range candidates {
		lot := lots[candidate.Id]
		if lot == nil {
			return nil, errors.New("historical redemption data is incomplete")
		}
		if lot.expired {
			result[candidate.Id] = expiredRemainders[candidate.Id]
		} else {
			result[candidate.Id] = lot.remaining
		}
	}
	return result, nil
}

func PreviewRedemptionReclaims(keys []string, now int64) (*RedemptionReclaimPreview, error) {
	keys, err := normalizeRedemptionReclaimKeys(keys)
	if err != nil {
		return nil, err
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}

	var redemptions []Redemption
	if err := DB.Where(commonKeyCol+" IN ?", keys).Find(&redemptions).Error; err != nil {
		return nil, err
	}
	byKey := make(map[string]*Redemption, len(redemptions))
	userIds := make(map[int]struct{})
	for i := range redemptions {
		byKey[redemptions[i].Key] = &redemptions[i]
		if redemptions[i].UsedUserId > 0 {
			userIds[redemptions[i].UsedUserId] = struct{}{}
		}
	}

	users := make(map[int]*User, len(userIds))
	if len(userIds) > 0 {
		ids := make([]int, 0, len(userIds))
		for id := range userIds {
			ids = append(ids, id)
		}
		var rows []User
		if err := DB.Select("id", "username", "quota", "used_quota").Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return nil, err
		}
		for i := range rows {
			users[rows[i].Id] = &rows[i]
		}
	}

	preview := &RedemptionReclaimPreview{
		Items:         make([]RedemptionReclaimPreviewItem, 0, len(keys)),
		userSnapshots: make(map[int]redemptionReclaimUserSnapshot),
	}
	candidatesByUser := make(map[int][]*Redemption)
	itemIndexById := make(map[int]int)
	for _, key := range keys {
		redemption := byKey[key]
		if redemption == nil {
			preview.Items = append(preview.Items, RedemptionReclaimPreviewItem{
				Key:    key,
				Result: RedemptionReclaimPreviewNotFound,
				Reason: "redemption code does not exist",
			})
			continue
		}
		item := RedemptionReclaimPreviewItem{
			Key:                redemption.Key,
			Id:                 redemption.Id,
			Name:               redemption.Name,
			RedemptionStatus:   redemption.Status,
			Quota:              redemption.Quota,
			UsedUserId:         redemption.UsedUserId,
			RedeemedTime:       redemption.RedeemedTime,
			ExpiredTime:        redemption.ExpiredTime,
			RemainingQuota:     redemption.ReclaimRemainingQuota,
			reclaimStatus:      redemption.ReclaimStatus,
			reclaimEnabledTime: redemption.ReclaimEnabledTime,
			reclaimedQuota:     redemption.ReclaimedQuota,
			reclaimedTime:      redemption.ReclaimedTime,
			reclaimError:       redemption.ReclaimError,
		}
		if user := users[redemption.UsedUserId]; user != nil {
			item.Username = user.Username
		}

		switch redemption.ReclaimStatus {
		case RedemptionReclaimStatusPending, RedemptionReclaimStatusCompleted, RedemptionReclaimStatusError:
			item.Result = RedemptionReclaimPreviewAlreadyEnabled
			item.Reason = redemption.ReclaimError
		case RedemptionReclaimStatusManualReview:
			item.Result = RedemptionReclaimPreviewManualReview
			item.Reason = redemption.ReclaimError
		case RedemptionReclaimStatusDisabled:
			switch {
			case redemption.ExpiredTime == 0:
				item.Result = RedemptionReclaimPreviewNoExpiry
				item.Reason = "redemption code has no expiry time"
			case redemption.Status == common.RedemptionCodeStatusEnabled && redemption.ExpiredTime < now:
				item.Result = RedemptionReclaimPreviewExpiredUnused
				item.Reason = "unused redemption code has expired"
			case redemption.Status == common.RedemptionCodeStatusEnabled:
				item.Result = RedemptionReclaimPreviewEligible
				item.RemainingQuota = redemption.Quota
			case redemption.Status != common.RedemptionCodeStatusUsed:
				item.Result = RedemptionReclaimPreviewConflict
				item.Reason = "redemption code is disabled"
			case redemption.UsedUserId <= 0 || redemption.RedeemedTime <= 0:
				item.Result = RedemptionReclaimPreviewManualReview
				item.Reason = "redemption record is incomplete"
			default:
				item.Result = RedemptionReclaimPreviewEligible
				item.RemainingQuota = 0
				candidatesByUser[redemption.UsedUserId] = append(candidatesByUser[redemption.UsedUserId], redemption)
			}
		}
		itemIndexById[redemption.Id] = len(preview.Items)
		preview.Items = append(preview.Items, item)
	}

	for userId, candidates := range candidatesByUser {
		remainingById, reconstructErr := reconstructRedemptionRemainders(DB, LOG_DB, users[userId], candidates, now)
		for _, candidate := range candidates {
			index := itemIndexById[candidate.Id]
			if reconstructErr != nil {
				preview.Items[index].Result = RedemptionReclaimPreviewManualReview
				preview.Items[index].Reason = reconstructErr.Error()
				continue
			}
			preview.Items[index].RemainingQuota = remainingById[candidate.Id]
		}
		if reconstructErr == nil {
			user := users[userId]
			preview.userSnapshots[userId] = redemptionReclaimUserSnapshot{
				Id:        user.Id,
				Quota:     user.Quota,
				UsedQuota: user.UsedQuota,
			}
		}
	}

	deadlineSet := make(map[int64]struct{})
	userSet := make(map[int]struct{})
	preview.Summary.CodeCount = len(preview.Items)
	for i := range preview.Items {
		item := &preview.Items[i]
		item.CanEnable = item.Result == RedemptionReclaimPreviewEligible ||
			(item.Result == RedemptionReclaimPreviewManualReview &&
				item.reclaimStatus == RedemptionReclaimStatusDisabled)
		preview.Summary.TotalQuota += item.Quota
		if item.ExpiredTime > 0 {
			deadlineSet[item.ExpiredTime] = struct{}{}
		}
		if item.UsedUserId > 0 {
			userSet[item.UsedUserId] = struct{}{}
		}
		if item.Result != RedemptionReclaimPreviewEligible && item.Result != RedemptionReclaimPreviewAlreadyEnabled {
			preview.Summary.ExceptionCount++
		}
	}
	preview.Summary.DeadlineCount = len(deadlineSet)
	preview.Summary.UserCount = len(userSet)

	sort.SliceStable(preview.Items, func(i, j int) bool {
		left := preview.Items[i].ExpiredTime
		right := preview.Items[j].ExpiredTime
		if left == 0 {
			return false
		}
		if right == 0 {
			return true
		}
		if left != right {
			return left < right
		}
		return preview.Items[i].Key < preview.Items[j].Key
	})
	preview.Snapshot, err = redemptionReclaimSnapshot(preview.Items, preview.userSnapshots)
	if err != nil {
		return nil, err
	}
	return preview, nil
}

func EnableRedemptionReclaims(keys []string, snapshot string, now int64) (RedemptionReclaimEnableResult, error) {
	preview, err := PreviewRedemptionReclaims(keys, now)
	if err != nil {
		return RedemptionReclaimEnableResult{}, err
	}
	if snapshot == "" || snapshot != preview.Snapshot {
		return RedemptionReclaimEnableResult{}, ErrRedemptionReclaimPreviewConflict
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}

	result := RedemptionReclaimEnableResult{}
	err = DB.Transaction(func(tx *gorm.DB) error {
		userSet := make(map[int]struct{})
		for i := range preview.Items {
			item := &preview.Items[i]
			if item.UsedUserId > 0 && item.Result == RedemptionReclaimPreviewEligible {
				userSet[item.UsedUserId] = struct{}{}
			}
		}
		userIds := make([]int, 0, len(userSet))
		for userId := range userSet {
			userIds = append(userIds, userId)
		}
		sort.Ints(userIds)
		for _, userId := range userIds {
			expected, ok := preview.userSnapshots[userId]
			if !ok {
				return ErrRedemptionReclaimPreviewConflict
			}
			user, err := lockUserForQuotaUpdate(tx, userId)
			if err != nil {
				return err
			}
			if user.Quota != expected.Quota || user.UsedQuota != expected.UsedQuota {
				return ErrRedemptionReclaimPreviewConflict
			}

			// SELECT FOR UPDATE serializes this check on MySQL and PostgreSQL.
			// SQLite has no row-level lock syntax, so this conditional no-op write
			// acquires its transaction write lock before attribution is persisted.
			if err := tx.Model(&User{}).
				Where("id = ? AND quota = ? AND used_quota = ?", userId, expected.Quota, expected.UsedQuota).
				UpdateColumn("quota", gorm.Expr("quota")).Error; err != nil {
				return err
			}
			if err := tx.Select("quota", "used_quota").Where("id = ?", userId).First(user).Error; err != nil {
				return err
			}
			if user.Quota != expected.Quota || user.UsedQuota != expected.UsedQuota {
				return ErrRedemptionReclaimPreviewConflict
			}
		}

		keyList := make([]string, 0, len(preview.Items))
		for i := range preview.Items {
			keyList = append(keyList, preview.Items[i].Key)
		}
		var locked []Redemption
		if err := lockForUpdate(tx).Where(commonKeyCol+" IN ?", keyList).Order("id").Find(&locked).Error; err != nil {
			return err
		}
		lockedById := make(map[int]*Redemption, len(locked))
		for i := range locked {
			lockedById[locked[i].Id] = &locked[i]
		}

		for i := range preview.Items {
			item := &preview.Items[i]
			if !item.CanEnable {
				result.SkippedCount++
				continue
			}
			redemption := lockedById[item.Id]
			if redemption == nil || redemption.Key != item.Key || redemption.Status != item.RedemptionStatus ||
				redemption.Quota != item.Quota || redemption.UsedUserId != item.UsedUserId ||
				redemption.RedeemedTime != item.RedeemedTime || redemption.ExpiredTime != item.ExpiredTime ||
				redemption.ReclaimStatus != RedemptionReclaimStatusDisabled {
				return ErrRedemptionReclaimPreviewConflict
			}

			updates := map[string]any{
				"reclaim_enabled_time":  now,
				"reclaimed_quota":       0,
				"reclaimed_time":        0,
				"reclaim_reviewed_by":   0,
				"reclaim_reviewed_time": 0,
				"reclaim_review_note":   "",
			}
			if item.Result == RedemptionReclaimPreviewManualReview {
				updates["reclaim_status"] = RedemptionReclaimStatusManualReview
				updates["reclaim_remaining_quota"] = 0
				updates["reclaim_error"] = item.Reason
				result.ManualReviewCount++
			} else {
				updates["reclaim_status"] = RedemptionReclaimStatusPending
				updates["reclaim_error"] = ""
				if redemption.Status == common.RedemptionCodeStatusUsed {
					updates["reclaim_remaining_quota"] = item.RemainingQuota
				} else {
					updates["reclaim_remaining_quota"] = 0
				}
				result.EnabledCount++
			}
			update := tx.Model(&Redemption{}).
				Where("id = ? AND reclaim_status = ?", redemption.Id, RedemptionReclaimStatusDisabled).
				Updates(updates)
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrRedemptionReclaimPreviewConflict
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RedemptionReclaimEnableResult{}, ErrRedemptionReclaimPreviewConflict
		}
		return RedemptionReclaimEnableResult{}, err
	}
	return result, nil
}

func lockUserForRedemptionReviewTx(tx *gorm.DB, userId int) (*User, error) {
	user, err := lockUserForQuotaUpdate(tx, userId)
	if err != nil {
		return nil, err
	}
	expectedQuota := user.Quota
	expectedUsedQuota := user.UsedQuota
	if err := tx.Model(&User{}).
		Where("id = ? AND quota = ? AND used_quota = ?", userId, expectedQuota, expectedUsedQuota).
		UpdateColumn("quota", gorm.Expr("quota")).Error; err != nil {
		return nil, err
	}
	if err := tx.Select("id", "quota", "used_quota").Where("id = ?", userId).First(user).Error; err != nil {
		return nil, err
	}
	if user.Quota != expectedQuota || user.UsedQuota != expectedUsedQuota {
		return nil, ErrRedemptionReclaimPreviewConflict
	}
	return user, nil
}

func redemptionReclaimLogDBForTx(tx *gorm.DB) *gorm.DB {
	if LOG_DB == DB {
		return tx
	}
	return LOG_DB
}

func RetryManualRedemptionReclaims(redemptionId int, reviewerId int, now int64) (RedemptionReclaimReviewResult, error) {
	if redemptionId <= 0 || reviewerId <= 0 {
		return RedemptionReclaimReviewResult{}, ErrRedemptionReclaimReviewInvalid
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}

	target := Redemption{}
	if err := DB.Select("id", "used_user_id", "reclaim_status").Where("id = ?", redemptionId).First(&target).Error; err != nil {
		return RedemptionReclaimReviewResult{}, err
	}
	if target.ReclaimStatus != RedemptionReclaimStatusManualReview {
		return RedemptionReclaimReviewResult{}, ErrRedemptionReclaimNotManualReview
	}
	if target.UsedUserId <= 0 {
		return RedemptionReclaimReviewResult{}, fmt.Errorf("%w: redemption user is unavailable", ErrRedemptionReclaimReviewInvalid)
	}

	result := RedemptionReclaimReviewResult{UserId: target.UsedUserId}
	err := DB.Transaction(func(tx *gorm.DB) error {
		user, err := lockUserForRedemptionReviewTx(tx, target.UsedUserId)
		if err != nil {
			return err
		}

		var manual []Redemption
		if err := lockForUpdate(tx).
			Where("used_user_id = ? AND reclaim_status = ?", target.UsedUserId, RedemptionReclaimStatusManualReview).
			Order("redeemed_time, id").
			Find(&manual).Error; err != nil {
			return err
		}
		foundTarget := false
		candidates := make([]*Redemption, 0, len(manual))
		for i := range manual {
			if manual[i].Id == redemptionId {
				foundTarget = true
			}
			candidates = append(candidates, &manual[i])
		}
		if !foundTarget {
			return ErrRedemptionReclaimNotManualReview
		}

		remainingById, err := reconstructRedemptionRemainders(
			tx,
			redemptionReclaimLogDBForTx(tx),
			user,
			candidates,
			now,
		)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrRedemptionReclaimReconstruct, err)
		}

		settleRows := make([]Redemption, 0, len(manual))
		for i := range manual {
			remaining, ok := remainingById[manual[i].Id]
			if !ok || remaining < 0 || remaining > manual[i].Quota {
				return ErrRedemptionReclaimReviewInvalid
			}
			update := tx.Model(&Redemption{}).
				Where("id = ? AND reclaim_status = ?", manual[i].Id, RedemptionReclaimStatusManualReview).
				Updates(map[string]any{
					"reclaim_status":          RedemptionReclaimStatusPending,
					"reclaim_remaining_quota": remaining,
					"reclaimed_quota":         0,
					"reclaimed_time":          0,
					"reclaim_error":           "",
					"reclaim_reviewed_by":     reviewerId,
					"reclaim_reviewed_time":   now,
					"reclaim_review_note":     "",
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrRedemptionReclaimPreviewConflict
			}
			manual[i].ReclaimStatus = RedemptionReclaimStatusPending
			manual[i].ReclaimRemainingQuota = remaining
			manual[i].ReclaimedQuota = 0
			result.RedemptionIds = append(result.RedemptionIds, manual[i].Id)
			result.UpdatedCount++
			if remaining == 0 || manual[i].ExpiredTime < now {
				settleRows = append(settleRows, manual[i])
			} else {
				result.PendingCount++
			}
		}

		reclaimed, completed, err := settleRedemptionQuotaRowsTx(tx, target.UsedUserId, settleRows, now)
		if err != nil {
			return err
		}
		result.ReclaimedQuota = reclaimed
		result.CompletedCount = completed
		return nil
	})
	if err != nil {
		return RedemptionReclaimReviewResult{}, err
	}
	if result.ReclaimedQuota > 0 {
		syncUserQuotaCacheDelta(target.UsedUserId, -int64(result.ReclaimedQuota), "limited quota review retry")
	}
	return result, nil
}

func ResolveManualRedemptionReclaim(
	redemptionId int,
	reviewerId int,
	remainingQuota int,
	note string,
	now int64,
) (RedemptionReclaimReviewResult, error) {
	note = strings.TrimSpace(note)
	if redemptionId <= 0 || reviewerId <= 0 || remainingQuota < 0 || note == "" || len(note) > maxRedemptionReclaimReviewNoteBytes {
		return RedemptionReclaimReviewResult{}, ErrRedemptionReclaimReviewInvalid
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}

	target := Redemption{}
	if err := DB.Select("id", "used_user_id", "reclaim_status").Where("id = ?", redemptionId).First(&target).Error; err != nil {
		return RedemptionReclaimReviewResult{}, err
	}
	if target.ReclaimStatus != RedemptionReclaimStatusManualReview {
		return RedemptionReclaimReviewResult{}, ErrRedemptionReclaimNotManualReview
	}
	if target.UsedUserId <= 0 && remainingQuota > 0 {
		return RedemptionReclaimReviewResult{}, fmt.Errorf("%w: cannot reclaim quota without a redemption user", ErrRedemptionReclaimReviewInvalid)
	}

	result := RedemptionReclaimReviewResult{UserId: target.UsedUserId}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if target.UsedUserId <= 0 {
			redemption := Redemption{}
			if err := lockForUpdate(tx).Where("id = ?", redemptionId).First(&redemption).Error; err != nil {
				return err
			}
			if redemption.ReclaimStatus != RedemptionReclaimStatusManualReview {
				return ErrRedemptionReclaimNotManualReview
			}
			if remainingQuota != 0 {
				return ErrRedemptionReclaimReviewInvalid
			}
			update := tx.Model(&Redemption{}).
				Where("id = ? AND reclaim_status = ?", redemption.Id, RedemptionReclaimStatusManualReview).
				Updates(map[string]any{
					"reclaim_status":          RedemptionReclaimStatusCompleted,
					"reclaim_remaining_quota": 0,
					"reclaimed_quota":         0,
					"reclaimed_time":          now,
					"reclaim_error":           "",
					"reclaim_reviewed_by":     reviewerId,
					"reclaim_reviewed_time":   now,
					"reclaim_review_note":     note,
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrRedemptionReclaimPreviewConflict
			}
			result.UpdatedCount = 1
			result.CompletedCount = 1
			result.RedemptionIds = []int{redemption.Id}
			return nil
		}

		if _, err := lockUserForRedemptionReviewTx(tx, target.UsedUserId); err != nil {
			return err
		}
		redemption := Redemption{}
		if err := lockForUpdate(tx).Where("id = ?", redemptionId).First(&redemption).Error; err != nil {
			return err
		}
		if redemption.ReclaimStatus != RedemptionReclaimStatusManualReview {
			return ErrRedemptionReclaimNotManualReview
		}
		if redemption.UsedUserId != target.UsedUserId || redemption.Quota <= 0 ||
			redemption.Quota > common.MaxWalletQuota || redemption.ExpiredTime <= 0 ||
			redemption.ReclaimedQuota != 0 {
			return ErrRedemptionReclaimPreviewConflict
		}
		if remainingQuota > redemption.Quota {
			return ErrRedemptionReclaimReviewInvalid
		}

		update := tx.Model(&Redemption{}).
			Where("id = ? AND reclaim_status = ?", redemption.Id, RedemptionReclaimStatusManualReview).
			Updates(map[string]any{
				"reclaim_status":          RedemptionReclaimStatusPending,
				"reclaim_remaining_quota": remainingQuota,
				"reclaimed_quota":         0,
				"reclaimed_time":          0,
				"reclaim_error":           "",
				"reclaim_reviewed_by":     reviewerId,
				"reclaim_reviewed_time":   now,
				"reclaim_review_note":     note,
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrRedemptionReclaimPreviewConflict
		}
		redemption.ReclaimStatus = RedemptionReclaimStatusPending
		redemption.ReclaimRemainingQuota = remainingQuota
		redemption.ReclaimedQuota = 0
		result.RedemptionIds = []int{redemption.Id}
		result.UpdatedCount = 1
		if remainingQuota > 0 && redemption.ExpiredTime >= now {
			result.PendingCount = 1
			return nil
		}

		reclaimed, completed, err := settleRedemptionQuotaRowsTx(
			tx,
			target.UsedUserId,
			[]Redemption{redemption},
			now,
		)
		if err != nil {
			return err
		}
		result.ReclaimedQuota = reclaimed
		result.CompletedCount = completed
		return nil
	})
	if err != nil {
		return RedemptionReclaimReviewResult{}, err
	}
	if result.ReclaimedQuota > 0 {
		syncUserQuotaCacheDelta(target.UsedUserId, -int64(result.ReclaimedQuota), "manual limited quota reclaim")
	}
	return result, nil
}
