package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLiandongUserRoutesRequireAuthenticationAndManagementRequiresRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalRedisEnabled := common.RedisEnabled
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		common.RedisEnabled = originalRedisEnabled
	})

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.User{},
		&model.Log{},
		&model.SystemTask{},
		&model.LiandongProduct{},
		&model.LiandongProductThumbnail{},
		&model.LiandongOrder{},
	))
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	imageData := []byte("liandong-thumbnail")
	require.NoError(t, db.Create(&model.LiandongProductThumbnail{
		ProductID:   19,
		ContentType: "image/png",
		Data:        imageData,
		Width:       440,
		Height:      440,
		Size:        len(imageData),
		Version:     123,
	}).Error)

	adminToken := "liandong-router-admin-token"
	rootToken := "liandong-router-root-token"
	require.NoError(t, db.Create(&model.User{
		Username:    "liandong-router-admin",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleAdminUser,
		Group:       "default",
		AffCode:     "LDROUTERADMIN",
		AccessToken: &adminToken,
	}).Error)
	require.NoError(t, db.Create(&model.User{
		Username:    "liandong-router-root",
		Password:    "password",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleRootUser,
		Group:       "default",
		AffCode:     "LDROUTERROOT",
		AccessToken: &rootToken,
	}).Error)

	engine := gin.New()
	SetApiRouter(engine)

	thumbnailRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		thumbnailRecorder,
		httptest.NewRequest(http.MethodGet, "/api/payment/liandong/products/19/thumbnail?v=123", nil),
	)
	require.Equal(t, http.StatusOK, thumbnailRecorder.Code)
	assert.Equal(t, "image/png", thumbnailRecorder.Header().Get("Content-Type"))
	assert.Equal(t, imageData, thumbnailRecorder.Body.Bytes())

	productsRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		productsRecorder,
		httptest.NewRequest(http.MethodGet, "/api/payment/liandong/products", nil),
	)
	assert.Equal(t, http.StatusUnauthorized, productsRecorder.Code)

	rootRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		rootRecorder,
		httptest.NewRequest(http.MethodGet, "/api/option/liandong", nil),
	)
	assert.Equal(t, http.StatusUnauthorized, rootRecorder.Code)

	rootRoutes := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/option/liandong"},
		{method: http.MethodPut, path: "/api/option/liandong"},
		{method: http.MethodGet, path: "/api/option/liandong/products"},
		{method: http.MethodPost, path: "/api/option/liandong/products"},
		{method: http.MethodPatch, path: "/api/option/liandong/products/1"},
		{method: http.MethodDelete, path: "/api/option/liandong/products/1"},
		{method: http.MethodPost, path: "/api/option/liandong/products/1/inventory"},
		{method: http.MethodPost, path: "/api/option/liandong/products/1/inventory/disable"},
		{method: http.MethodPut, path: "/api/option/liandong/products/1/thumbnail"},
		{method: http.MethodDelete, path: "/api/option/liandong/products/1/thumbnail"},
		{method: http.MethodGet, path: "/api/option/liandong/provider-goods"},
		{method: http.MethodGet, path: "/api/option/liandong/orders"},
		{method: http.MethodGet, path: "/api/option/liandong/monitor/tasks"},
		{method: http.MethodGet, path: "/api/option/liandong/monitor/calls"},
		{method: http.MethodPost, path: "/api/option/liandong/orders/LDTEST/requeue"},
		{method: http.MethodPost, path: "/api/option/liandong/orders/LDTEST/close"},
		{method: http.MethodPost, path: "/api/option/liandong/orders/LDTEST/manual-fulfill"},
		{method: http.MethodPost, path: "/api/option/liandong/orders/LDTEST/retry-fulfillment"},
	}
	for _, route := range rootRoutes {
		request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+adminToken)
		request.Header.Set("Accept-Language", "en")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		engine.ServeHTTP(recorder, request)

		assert.Equal(t, http.StatusForbidden, recorder.Code, "%s %s", route.method, route.path)
		assert.Contains(t, recorder.Body.String(), `"code":"AUTH_INSUFFICIENT_PRIVILEGE"`, "%s %s", route.method, route.path)
	}

	for _, path := range []string{
		"/api/option/liandong",
		"/api/option/liandong/products",
		"/api/option/liandong/monitor/tasks",
		"/api/option/liandong/monitor/calls",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+rootToken)
		recorder := httptest.NewRecorder()

		engine.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code, path)
		assert.Contains(t, recorder.Body.String(), `"success":true`, path)
	}
}
