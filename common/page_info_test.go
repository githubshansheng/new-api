package common

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestGetPageQueryRejectsNegativePagination(t *testing.T) {
	request := httptest.NewRequest("GET", "/?p=-3&page_size=-1", nil)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	page := GetPageQuery(context)

	assert.Equal(t, 1, page.Page)
	assert.Equal(t, ItemsPerPage, page.PageSize)
	assert.Zero(t, page.GetStartIdx())
}
