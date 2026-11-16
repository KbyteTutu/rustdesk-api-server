package api

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// F07: an authenticated write must not acknowledge a failed database update.
func TestAuditF07HeartbeatReportsDatabaseFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "heartbeat.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.AutoMigrate(&model.Peer{}); err != nil {
		t.Fatal(err)
	}
	savedDB, savedService := service.DB, service.AllService
	service.DB, service.AllService = db, &service.Service{}
	t.Cleanup(func() { service.DB, service.AllService = savedDB, savedService })
	before := time.Now().Add(-time.Hour).Unix()
	peer := model.Peer{Id: "device", Uuid: "uuid", UserId: 1, LastOnlineTime: before}
	if err := db.Create(&peer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER reject_heartbeat BEFORE UPDATE ON peers BEGIN SELECT RAISE(ABORT,'injected heartbeat failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	savedLogger := global.Logger
	global.Logger = logrus.New()
	global.Logger.SetOutput(&logs)
	t.Cleanup(func() { global.Logger = savedLogger })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/heartbeat", func(c *gin.Context) {
		c.Set("curUser", &model.User{IdModel: model.IdModel{Id: 1}})
		c.Set("deviceSession", &model.UserToken{UserId: 1, DeviceId: "device", DeviceUuid: "uuid"})
		(&Index{}).Heartbeat(c)
	})
	heartbeat := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/heartbeat", strings.NewReader(`{"id":"device","uuid":"uuid"}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		return response
	}
	response := heartbeat()
	var persisted model.Peer
	if err := db.First(&persisted, peer.RowId).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.LastOnlineTime != before {
		t.Fatal("fault injection did not prevent update")
	}
	if response.Code != 500 {
		t.Fatalf("database write failed but HTTP %d acknowledged success: %s", response.Code, response.Body.String())
	}
	if response.Body.String() != `{"error":"heartbeat update failed"}` {
		t.Fatalf("unexpected error response: %s", response.Body.String())
	}
	if !strings.Contains(logs.String(), "heartbeat persistence failed") || strings.Contains(logs.String(), "injected heartbeat failure") {
		t.Fatalf("missing safe operational log or leaked DB error: %s", logs.String())
	}

	// Removing the injected fault must permit a real persisted heartbeat.
	if err := db.Exec("DROP TRIGGER reject_heartbeat").Error; err != nil {
		t.Fatal(err)
	}
	response = heartbeat()
	if response.Code != 200 || response.Body.String() != "{}" {
		t.Fatalf("normal heartbeat failed: %d %s", response.Code, response.Body.String())
	}
	if err := db.First(&persisted, peer.RowId).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.LastOnlineTime <= before || persisted.LastOnlineIp != "192.0.2.1" {
		t.Fatalf("heartbeat not persisted: time=%d ip=%q", persisted.LastOnlineTime, persisted.LastOnlineIp)
	}
	lastOnline := persisted.LastOnlineTime

	// A recent heartbeat must not attempt another write even when writes fail.
	if err := db.Exec("CREATE TRIGGER reject_heartbeat BEFORE UPDATE ON peers BEGIN SELECT RAISE(ABORT,'injected heartbeat failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	response = heartbeat()
	if response.Code != 200 || response.Body.String() != "{}" || logs.Len() != 0 {
		t.Fatalf("throttled heartbeat attempted a write: %d %s", response.Code, logs.String())
	}
	if err := db.First(&persisted, peer.RowId).Error; err != nil || persisted.LastOnlineTime != lastOnline {
		t.Fatalf("throttled heartbeat changed persisted state: %v", err)
	}
}
