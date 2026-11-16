package api

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTelemetryRequiresExactSessionAndPeerOwner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "telemetry.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err = db.AutoMigrate(&model.Peer{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldService := service.DB, service.AllService
	defer func() { service.DB, service.AllService = oldDB, oldService }()
	service.DB, service.AllService = db, &service.Service{}
	user := &model.User{IdModel: model.IdModel{Id: 1}}
	session := &model.UserToken{UserId: 1, DeviceId: "device", DeviceUuid: "uuid"}
	gin.SetMode(gin.TestMode)
	check := func(token *model.UserToken, id, uuid string, want bool) {
		t.Helper()
		out := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(out)
		c.Set("curUser", user)
		if token != nil {
			c.Set("deviceSession", token)
		}
		got := authorizeDevice(c, id, uuid)
		if got != want || (!want && out.Code != 403) {
			t.Fatalf("identity %q/%q: allowed=%v status=%d", id, uuid, got, out.Code)
		}
	}
	check(nil, "device", "uuid", false)
	check(session, "device", "wrong", false)
	check(session, "other", "uuid", false)
	check(session, "device", "uuid", true)
	peer := &model.Peer{Id: "device", Uuid: "uuid", UserId: 2}
	if err = db.Create(peer).Error; err != nil {
		t.Fatal(err)
	}
	check(session, "device", "uuid", false)
	if err = db.Model(peer).Update("user_id", 1).Error; err != nil {
		t.Fatal(err)
	}
	check(session, "device", "uuid", true)
	if err = db.Model(peer).Update("uuid", "reassigned").Error; err != nil {
		t.Fatal(err)
	}
	check(session, "device", "uuid", false)
}

func TestMalformedSharedPeerInputDoesNotPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The handler must reject a non-string token before any DB access.
	router := gin.New()
	router.POST("/shared", (&WebClient{}).SharedPeer)
	for _, payload := range []string{`{}`, `{"share_token":123}`, `{"share_token":true}`, `{"share_token":[]}`} {
		out := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/shared", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(out, req)
		if out.Code != 400 {
			t.Fatalf("%s: status=%d", payload, out.Code)
		}
	}
}
