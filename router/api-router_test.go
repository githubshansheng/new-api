package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
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
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
	})

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.LiandongProduct{},
		&model.LiandongProductThumbnail{},
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

	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("liandong-router-test"))))
	engine.GET("/test/liandong-session/:role", func(c *gin.Context) {
		role := common.RoleAdminUser
		id := 42
		username := "admin"
		if c.Param("role") == "root" {
			role = common.RoleRootUser
			id = 1
			username = "root"
		}
		session := sessions.Default(c)
		session.Set("username", username)
		session.Set("role", role)
		session.Set("id", id)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
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

	adminLoginRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		adminLoginRecorder,
		httptest.NewRequest(http.MethodGet, "/test/liandong-session/admin", nil),
	)
	require.Equal(t, http.StatusNoContent, adminLoginRecorder.Code)
	adminCookie := strings.Split(adminLoginRecorder.Header().Get("Set-Cookie"), ";")[0]
	require.NotEmpty(t, adminCookie)

	rootRoutes := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/option/liandong"},
		{method: http.MethodPut, path: "/api/option/liandong"},
		{method: http.MethodGet, path: "/api/option/liandong/products"},
		{method: http.MethodPost, path: "/api/option/liandong/products"},
		{method: http.MethodPatch, path: "/api/option/liandong/products/1"},
		{method: http.MethodPost, path: "/api/option/liandong/products/1/inventory"},
		{method: http.MethodPost, path: "/api/option/liandong/products/1/inventory/disable"},
		{method: http.MethodPut, path: "/api/option/liandong/products/1/thumbnail"},
		{method: http.MethodDelete, path: "/api/option/liandong/products/1/thumbnail"},
		{method: http.MethodGet, path: "/api/option/liandong/provider-goods"},
		{method: http.MethodGet, path: "/api/option/liandong/orders"},
		{method: http.MethodPost, path: "/api/option/liandong/orders/LDTEST/requeue"},
		{method: http.MethodPost, path: "/api/option/liandong/orders/LDTEST/close"},
		{method: http.MethodPost, path: "/api/option/liandong/orders/LDTEST/manual-fulfill"},
		{method: http.MethodPost, path: "/api/option/liandong/orders/LDTEST/retry-fulfillment"},
	}
	for _, route := range rootRoutes {
		request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		request.Header.Set("Cookie", adminCookie)
		request.Header.Set("New-Api-User", "42")
		request.Header.Set("Accept-Language", "en")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		engine.ServeHTTP(recorder, request)

		assert.Equal(t, http.StatusOK, recorder.Code, "%s %s", route.method, route.path)
		assert.Contains(t, recorder.Body.String(), `"message":"auth.insufficient_privilege"`, "%s %s", route.method, route.path)
	}

	rootLoginRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		rootLoginRecorder,
		httptest.NewRequest(http.MethodGet, "/test/liandong-session/root", nil),
	)
	require.Equal(t, http.StatusNoContent, rootLoginRecorder.Code)
	rootCookie := strings.Split(rootLoginRecorder.Header().Get("Set-Cookie"), ";")[0]
	require.NotEmpty(t, rootCookie)

	for _, path := range []string{"/api/option/liandong", "/api/option/liandong/products"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Cookie", rootCookie)
		request.Header.Set("New-Api-User", "1")
		recorder := httptest.NewRecorder()

		engine.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code, path)
		assert.Contains(t, recorder.Body.String(), `"success":true`, path)
	}
}
