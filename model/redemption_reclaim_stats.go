package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
)

type RedemptionReclaimTrend struct {
	Date      string `json:"date"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
	Quota     int64  `json:"quota"`
	CodeCount int64  `json:"code_count"`
	UserCount int64  `json:"user_count"`
}

type RedemptionReclaimAttention struct {
	ExpiredTime int64  `json:"expired_time"`
	Quota       int64  `json:"quota"`
	CodeCount   int64  `json:"code_count"`
	UserCount   int64  `json:"user_count"`
	Status      string `json:"status"`
}

type RedemptionReclaimStats struct {
	ActiveQuota         int64                        `json:"active_quota"`
	ActiveUsers         int64                        `json:"active_users"`
	Expiring24hQuota    int64                        `json:"expiring_24h_quota"`
	Expiring24hCodes    int64                        `json:"expiring_24h_codes"`
	ReclaimedTodayQuota int64                        `json:"reclaimed_today_quota"`
	ReclaimedTodayUsers int64                        `json:"reclaimed_today_users"`
	OverdueCount        int64                        `json:"overdue_count"`
	ManualReviewCount   int64                        `json:"manual_review_count"`
	ErrorCount          int64                        `json:"error_count"`
	Trend               []RedemptionReclaimTrend     `json:"trend"`
	Attention           []RedemptionReclaimAttention `json:"attention"`
	TodayStart          int64                        `json:"today_start"`
	UpdatedAt           int64                        `json:"updated_at"`
}

type redemptionReclaimAggregate struct {
	ExpiredTime int64 `gorm:"column:expired_time"`
	Quota       int64 `gorm:"column:quota"`
	CodeCount   int64 `gorm:"column:code_count"`
	UserCount   int64 `gorm:"column:user_count"`
}

func scanRedemptionReclaimAggregate(queryWhere string, args ...any) (redemptionReclaimAggregate, error) {
	row := redemptionReclaimAggregate{}
	err := DB.Model(&Redemption{}).
		Select("COALESCE(SUM(reclaim_remaining_quota), 0) AS quota, COUNT(*) AS code_count, COUNT(DISTINCT used_user_id) AS user_count").
		Where(queryWhere, args...).
		Scan(&row).Error
	return row, err
}

func appendRedemptionReclaimAttention(
	attention []RedemptionReclaimAttention,
	status string,
	where string,
	args ...any,
) ([]RedemptionReclaimAttention, error) {
	remaining := 5 - len(attention)
	if remaining <= 0 {
		return attention, nil
	}
	var rows []redemptionReclaimAggregate
	err := DB.Model(&Redemption{}).
		Select("expired_time, COALESCE(SUM(reclaim_remaining_quota), 0) AS quota, COUNT(*) AS code_count, COUNT(DISTINCT CASE WHEN used_user_id > 0 THEN used_user_id END) AS user_count").
		Where(where, args...).
		Group("expired_time").
		Order("expired_time").
		Limit(remaining).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		attention = append(attention, RedemptionReclaimAttention{
			ExpiredTime: rows[i].ExpiredTime,
			Quota:       rows[i].Quota,
			CodeCount:   rows[i].CodeCount,
			UserCount:   rows[i].UserCount,
			Status:      status,
		})
	}
	return attention, nil
}

func GetRedemptionReclaimStats(now int64) (RedemptionReclaimStats, error) {
	if now <= 0 {
		now = common.GetTimestamp()
	}
	stats := RedemptionReclaimStats{
		Trend:     make([]RedemptionReclaimTrend, 0),
		Attention: make([]RedemptionReclaimAttention, 0, 5),
		UpdatedAt: now,
	}

	activeWhere := "used_user_id > 0 AND reclaim_status = ? AND expired_time >= ? AND reclaim_remaining_quota > 0"
	active, err := scanRedemptionReclaimAggregate(activeWhere, RedemptionReclaimStatusPending, now)
	if err != nil {
		return stats, err
	}
	stats.ActiveQuota = active.Quota
	stats.ActiveUsers = active.UserCount

	expiring, err := scanRedemptionReclaimAggregate(
		activeWhere+" AND expired_time <= ?",
		RedemptionReclaimStatusPending,
		now,
		now+24*60*60,
	)
	if err != nil {
		return stats, err
	}
	stats.Expiring24hQuota = expiring.Quota
	stats.Expiring24hCodes = expiring.CodeCount

	startOfToday := StartOfLocalDay(now)
	stats.TodayStart = startOfToday
	startOfTomorrow := time.Unix(startOfToday, 0).In(time.Local).AddDate(0, 0, 1).Unix()
	reclaimed := redemptionReclaimAggregate{}
	if err := DB.Model(&Redemption{}).
		Select("COALESCE(SUM(reclaimed_quota), 0) AS quota, COUNT(*) AS code_count, COUNT(DISTINCT used_user_id) AS user_count").
		Where("used_user_id > 0 AND reclaim_status = ? AND reclaimed_time >= ? AND reclaimed_time < ?",
			RedemptionReclaimStatusCompleted, startOfToday, startOfTomorrow).
		Scan(&reclaimed).Error; err != nil {
		return stats, err
	}
	stats.ReclaimedTodayQuota = reclaimed.Quota
	stats.ReclaimedTodayUsers = reclaimed.UserCount

	if err := DB.Model(&Redemption{}).
		Where("used_user_id > 0 AND reclaim_status = ? AND expired_time > 0 AND expired_time < ?",
			RedemptionReclaimStatusPending, now).
		Count(&stats.OverdueCount).Error; err != nil {
		return stats, err
	}
	if err := DB.Model(&Redemption{}).
		Where("reclaim_status = ?", RedemptionReclaimStatusManualReview).
		Count(&stats.ManualReviewCount).Error; err != nil {
		return stats, err
	}
	if err := DB.Model(&Redemption{}).
		Where("reclaim_status = ?", RedemptionReclaimStatusError).
		Count(&stats.ErrorCount).Error; err != nil {
		return stats, err
	}

	trend := make([]RedemptionReclaimTrend, 7)
	hasTrend := false
	for day := 0; day < len(trend); day++ {
		start := time.Unix(startOfToday, 0).In(time.Local).AddDate(0, 0, day)
		end := start.AddDate(0, 0, 1)
		queryStart := start.Unix()
		if queryStart < now {
			queryStart = now
		}
		aggregate, err := scanRedemptionReclaimAggregate(
			"used_user_id > 0 AND reclaim_status = ? AND expired_time >= ? AND expired_time < ? AND reclaim_remaining_quota > 0",
			RedemptionReclaimStatusPending,
			queryStart,
			end.Unix(),
		)
		if err != nil {
			return stats, err
		}
		trend[day] = RedemptionReclaimTrend{
			Date:      start.Format("2006-01-02"),
			StartTime: start.Unix(),
			EndTime:   end.Unix() - 1,
			Quota:     aggregate.Quota,
			CodeCount: aggregate.CodeCount,
			UserCount: aggregate.UserCount,
		}
		if aggregate.CodeCount > 0 {
			hasTrend = true
		}
	}
	if hasTrend {
		stats.Trend = trend
	}

	stats.Attention, err = appendRedemptionReclaimAttention(
		stats.Attention,
		"4",
		"reclaim_status = ?",
		RedemptionReclaimStatusError,
	)
	if err != nil {
		return stats, err
	}
	stats.Attention, err = appendRedemptionReclaimAttention(
		stats.Attention,
		"due",
		"used_user_id > 0 AND reclaim_status = ? AND expired_time > 0 AND expired_time < ?",
		RedemptionReclaimStatusPending,
		now,
	)
	if err != nil {
		return stats, err
	}
	stats.Attention, err = appendRedemptionReclaimAttention(
		stats.Attention,
		"3",
		"reclaim_status = ?",
		RedemptionReclaimStatusManualReview,
	)
	if err != nil {
		return stats, err
	}
	stats.Attention, err = appendRedemptionReclaimAttention(
		stats.Attention,
		"active",
		"used_user_id > 0 AND reclaim_status = ? AND expired_time >= ?",
		RedemptionReclaimStatusPending,
		now,
	)
	if err != nil {
		return stats, err
	}

	return stats, nil
}
