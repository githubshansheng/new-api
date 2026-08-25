package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRedemptionReclaimControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousLogConsumeEnabled := common.LogConsumeEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Redemption{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.LogConsumeEnabled = true
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.LogConsumeEnabled = previousLogConsumeEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func performRedemptionReclaimReviewRequest(
	t *testing.T,
	handler gin.HandlerFunc,
	redemptionId int,
	body string,
	admin *model.User,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/redemption/reclaim/%d/review", redemptionId),
		strings.NewReader(body),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", redemptionId)}}
	c.Set("id", admin.Id)
	c.Set("username", admin.Username)
	c.Set("role", admin.Role)
	handler(c)
	return recorder
}

func createRedemptionReclaimControllerUsers(t *testing.T, db *gorm.DB) (*model.User, *model.User) {
	t.Helper()
	admin := &model.User{
		Username: "reclaim-admin",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Quota:    100,
		AffCode:  "reclaim-admin-aff",
	}
	target := &model.User{
		Username:  "reclaim-target",
		Password:  "password",
		Role:      common.RoleCommonUser,
		Status:    common.UserStatusEnabled,
		Quota:     90,
		UsedQuota: 7,
		AffCode:   "reclaim-target-aff",
	}
	require.NoError(t, db.Create(admin).Error)
	require.NoError(t, db.Create(target).Error)
	return admin, target
}

func TestResolveManualRedemptionReclaimAPISettlesAndAudits(t *testing.T) {
	db := setupRedemptionReclaimControllerTestDB(t)
	admin, target := createRedemptionReclaimControllerUsers(t, db)
	now := common.GetTimestamp()
	redemption := &model.Redemption{
		Key:                common.GetUUID(),
		Name:               "manual-review",
		Status:             common.RedemptionCodeStatusUsed,
		Quota:              50,
		RedeemedTime:       now - 600,
		UsedUserId:         target.Id,
		ExpiredTime:        now - 60,
		ReclaimStatus:      model.RedemptionReclaimStatusManualReview,
		ReclaimEnabledTime: now - 300,
		ReclaimError:       "historical records are ambiguous",
	}
	require.NoError(t, db.Create(redemption).Error)

	recorder := performRedemptionReclaimReviewRequest(
		t,
		ResolveManualRedemptionReclaim,
		redemption.Id,
		`{"remaining_quota":20,"note":"Verified against the wallet ledger."}`,
		admin,
	)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			UserId         int   `json:"user_id"`
			RedemptionIds  []int `json:"redemption_ids"`
			CompletedCount int   `json:"completed_count"`
			ReclaimedQuota int   `json:"reclaimed_quota"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, target.Id, response.Data.UserId)
	assert.Equal(t, []int{redemption.Id}, response.Data.RedemptionIds)
	assert.Equal(t, 1, response.Data.CompletedCount)
	assert.Equal(t, 20, response.Data.ReclaimedQuota)

	var storedUser model.User
	require.NoError(t, db.First(&storedUser, target.Id).Error)
	assert.Equal(t, 70, storedUser.Quota)
	assert.Equal(t, 7, storedUser.UsedQuota)

	var storedRedemption model.Redemption
	require.NoError(t, db.First(&storedRedemption, redemption.Id).Error)
	assert.Equal(t, model.RedemptionReclaimStatusCompleted, storedRedemption.ReclaimStatus)
	assert.Equal(t, 20, storedRedemption.ReclaimedQuota)
	assert.Equal(t, admin.Id, storedRedemption.ReclaimReviewedBy)
	assert.Equal(t, "Verified against the wallet ledger.", storedRedemption.ReclaimReviewNote)

	var consumeLogs int64
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&consumeLogs).Error)
	assert.Zero(t, consumeLogs)

	var audit model.Log
	require.NoError(t, db.Where("type = ?", model.LogTypeManage).First(&audit).Error)
	assert.Equal(t, admin.Id, audit.UserId)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(audit.Other, &other))
	op, ok := other["op"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "redemption.reclaim.review_resolve", op["action"])
	params, ok := op["params"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, target.Id, params["target_user_id"])
	assert.Equal(t, logger.LogQuota(20), params["remaining_quota"])
	assert.Equal(t, "Verified against the wallet ledger.", params["review_note"])
}

func TestRedemptionReclaimReviewAPIErrorsUseActionableStatuses(t *testing.T) {
	db := setupRedemptionReclaimControllerTestDB(t)
	admin, target := createRedemptionReclaimControllerUsers(t, db)
	now := common.GetTimestamp()
	redemption := &model.Redemption{
		Key:                common.GetUUID(),
		Name:               "manual-review-errors",
		Status:             common.RedemptionCodeStatusUsed,
		Quota:              50,
		RedeemedTime:       now - 600,
		UsedUserId:         target.Id,
		ExpiredTime:        now + 600,
		ReclaimStatus:      model.RedemptionReclaimStatusManualReview,
		ReclaimEnabledTime: now - 300,
	}
	require.NoError(t, db.Create(redemption).Error)

	tests := []struct {
		name           string
		handler        gin.HandlerFunc
		body           string
		prepare        func()
		expectedStatus int
	}{
		{
			name:           "missing remaining quota",
			handler:        ResolveManualRedemptionReclaim,
			body:           `{"note":"Reviewed."}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:    "state changed",
			handler: ResolveManualRedemptionReclaim,
			body:    `{"remaining_quota":10,"note":"Reviewed."}`,
			prepare: func() {
				require.NoError(t, db.Model(&model.Redemption{}).
					Where("id = ?", redemption.Id).
					Update("reclaim_status", model.RedemptionReclaimStatusPending).Error)
			},
			expectedStatus: http.StatusConflict,
		},
		{
			name:    "automatic reconstruction remains ambiguous",
			handler: RetryManualRedemptionReclaim,
			body:    "",
			prepare: func() {
				require.NoError(t, db.Model(&model.Redemption{}).
					Where("id = ?", redemption.Id).
					Update("reclaim_status", model.RedemptionReclaimStatusManualReview).Error)
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.prepare != nil {
				testCase.prepare()
			}
			recorder := performRedemptionReclaimReviewRequest(
				t,
				testCase.handler,
				redemption.Id,
				testCase.body,
				admin,
			)
			assert.Equal(t, testCase.expectedStatus, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}
