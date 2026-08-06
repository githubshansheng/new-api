package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	liandongMonitorModelName    = "card_marketplace_upstream"
	liandongMonitorLoginContent = "Card marketplace login refresh"
)

type LiandongUpstreamCall struct {
	RequestID    string `json:"request_id"`
	Source       string `json:"source"`
	Reference    string `json:"reference,omitempty"`
	Operation    string `json:"operation"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	StatusCode   int    `json:"status_code"`
	Success      bool   `json:"success"`
	DurationMS   int64  `json:"duration_ms"`
	RequestBody  string `json:"request_body,omitempty"`
	ResponseBody string `json:"response_body,omitempty"`
	Error        string `json:"error,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

type LiandongUpstreamCallPage struct {
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
	Total    int64                  `json:"total"`
	Items    []LiandongUpstreamCall `json:"items"`
}

type liandongMonitorLogData struct {
	LiandongCall LiandongUpstreamCall `json:"liandong_call"`
}

func RecordLiandongUpstreamCall(call LiandongUpstreamCall) error {
	if call.Success && call.Operation != "login" {
		return nil
	}
	call.RequestID = strings.TrimSpace(call.RequestID)
	if call.RequestID == "" {
		call.RequestID = common.NewRequestId()
	}
	if call.CreatedAt == 0 {
		call.CreatedAt = common.GetTimestamp()
	}
	other, err := common.Marshal(map[string]any{
		"admin_info": liandongMonitorLogData{LiandongCall: call},
	})
	if err != nil {
		return err
	}
	logType := LogTypeSystem
	if !call.Success {
		logType = LogTypeError
	}
	content := "Card marketplace upstream request failed"
	if call.Operation == "login" {
		content = liandongMonitorLoginContent
	}
	return createLog(&Log{
		CreatedAt: call.CreatedAt,
		Type:      logType,
		Content:   content,
		ModelName: liandongMonitorModelName,
		RequestId: call.RequestID,
		Other:     string(other),
	})
}

func ListLiandongUpstreamCalls(page int, pageSize int, resultFilter string) (LiandongUpstreamCallPage, error) {
	if page < 1 {
		page = 1
	}
	switch pageSize {
	case 10, 20, 50, 100, 200, 500:
	default:
		pageSize = 10
	}

	result := LiandongUpstreamCallPage{
		Page:     page,
		PageSize: pageSize,
		Items:    []LiandongUpstreamCall{},
	}
	tx := LOG_DB.Model(&Log{}).
		Where("model_name = ?", liandongMonitorModelName).
		Where("type = ? OR content = ?", LogTypeError, liandongMonitorLoginContent)
	switch resultFilter {
	case "success":
		tx = tx.Where("type = ?", LogTypeSystem)
	case "failed":
		tx = tx.Where("type = ?", LogTypeError)
	}
	if err := tx.Count(&result.Total).Error; err != nil {
		return result, err
	}

	order := "id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	var logs []*Log
	if err := tx.Order(order).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&logs).Error; err != nil {
		return result, err
	}
	for _, log := range logs {
		var payload struct {
			AdminInfo liandongMonitorLogData `json:"admin_info"`
		}
		if err := common.UnmarshalJsonStr(log.Other, &payload); err != nil {
			continue
		}
		call := payload.AdminInfo.LiandongCall
		if call.RequestID == "" {
			call.RequestID = log.RequestId
		}
		if call.CreatedAt == 0 {
			call.CreatedAt = log.CreatedAt
		}
		result.Items = append(result.Items, call)
	}
	return result, nil
}
