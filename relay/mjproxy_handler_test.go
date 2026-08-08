package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestRelayMidjourneyImageChecksTaskOwner verifies a user cannot fetch another user's image.
func TestRelayMidjourneyImageChecksTaskOwner(t *testing.T) {
	originalDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}))
	model.DB = db
	t.Cleanup(func() { model.DB = originalDB })
	require.NoError(t, db.Create(&model.Midjourney{
		UserId:   2,
		MjId:     "mj-private",
		ImageUrl: "https://supplier.example/private.png",
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/mj/image/mj-private", nil)
	c.Params = gin.Params{{Key: "id", Value: "mj-private"}}
	c.Set("id", 1)

	RelayMidjourneyImage(c)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.JSONEq(t, `{"error":"media_not_available"}`, recorder.Body.String())
}
