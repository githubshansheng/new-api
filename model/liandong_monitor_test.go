package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiandongUpstreamCallMonitorStoresStructuredRecords(t *testing.T) {
	require.NoError(t, LOG_DB.Where("model_name = ?", liandongMonitorModelName).Delete(&Log{}).Error)
	t.Cleanup(func() {
		_ = LOG_DB.Where("model_name = ?", liandongMonitorModelName).Delete(&Log{}).Error
	})

	require.NoError(t, RecordLiandongUpstreamCall(LiandongUpstreamCall{
		Source:     "scheduled_reconcile",
		Operation:  "query_orders",
		Method:     "POST",
		Path:       "/merchantApi/order/list",
		StatusCode: 200,
		Success:    true,
		DurationMS: 18,
	}))
	require.NoError(t, RecordLiandongUpstreamCall(LiandongUpstreamCall{
		Source:     "client_order_poll",
		Operation:  "login",
		Method:     "POST",
		Path:       "/merchantApi/user/login",
		StatusCode: 200,
		Success:    true,
		DurationMS: 25,
	}))
	require.NoError(t, RecordLiandongUpstreamCall(LiandongUpstreamCall{
		Source:       "client_order_poll",
		Reference:    "LD-LOCAL-001",
		Operation:    "query_orders",
		Method:       "POST",
		Path:         "/merchantApi/order/list",
		StatusCode:   0,
		Success:      false,
		DurationMS:   30000,
		RequestBody:  `{"current":1,"pageSize":50,"status":999}`,
		ResponseBody: `{"code":500,"message":"upstream unavailable"}`,
		Error:        "TLS handshake timeout",
	}))

	page, err := ListLiandongUpstreamCalls(1, 500, "all")

	require.NoError(t, err)
	assert.Equal(t, 500, page.PageSize)
	assert.EqualValues(t, 2, page.Total)
	require.Len(t, page.Items, 2)
	assert.Equal(t, "client_order_poll", page.Items[0].Source)
	assert.Equal(t, "LD-LOCAL-001", page.Items[0].Reference)
	assert.Equal(t, "TLS handshake timeout", page.Items[0].Error)
	assert.JSONEq(t, `{"current":1,"pageSize":50,"status":999}`, page.Items[0].RequestBody)
	assert.JSONEq(t, `{"code":500,"message":"upstream unavailable"}`, page.Items[0].ResponseBody)
	assert.False(t, page.Items[0].Success)
	assert.NotEmpty(t, page.Items[0].RequestID)
	assert.Equal(t, "login", page.Items[1].Operation)
	assert.True(t, page.Items[1].Success)

	successPage, err := ListLiandongUpstreamCalls(1, 10, "success")
	require.NoError(t, err)
	assert.EqualValues(t, 1, successPage.Total)
	require.Len(t, successPage.Items, 1)
	assert.True(t, successPage.Items[0].Success)

	failedPage, err := ListLiandongUpstreamCalls(1, 10, "failed")
	require.NoError(t, err)
	assert.EqualValues(t, 1, failedPage.Total)
	require.Len(t, failedPage.Items, 1)
	assert.False(t, failedPage.Items[0].Success)
}
