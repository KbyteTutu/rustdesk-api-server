package middleware

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/config"
	"github.com/lejianwen/rustdesk-api/v2/lib/jwt"
	"github.com/lejianwen/rustdesk-api/v2/lib/lock"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAuthenticationAndAdminAuthorization(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err = db.AutoMigrate(&model.User{}, &model.UserToken{}, &model.LoginLog{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldConfig, oldJWT, oldService, oldLock, oldLogger := service.DB, service.Config, service.Jwt, service.AllService, service.Lock, service.Logger
	defer func() {
		service.DB, service.Config, service.Jwt, service.AllService, service.Lock, service.Logger = oldDB, oldConfig, oldJWT, oldService, oldLock, oldLogger
	}()
	service.New(&config.Config{}, db, logrus.New(), jwt.NewJwt(strings.Repeat("s", 32), time.Hour), lock.NewLocal())
	user := &model.User{Username: "user", Status: model.COMMON_STATUS_ENABLE}
	if err = db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	token, err := service.AllService.UserService.Login(user, &model.LoginLog{})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	done := func(c *gin.Context) { c.Status(204) }
	router.GET("/api", RustAuth(), done)
	router.POST("/control", BackendUserAuth(), AdminPrivilege(), done)
	check := func(path, header, value string, want int) {
		t.Helper()
		method := "GET"
		if path == "/control" {
			method = "POST"
		}
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set(header, value)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("%s: status %d want %d", path, out.Code, want)
		}
	}
	for _, value := range []string{"", "Bearer", "Basic " + token.Token, "invalid " + token.Token, "Bearer invalid"} {
		check("/api", "Authorization", value, 401)
	}
	check("/api", "Authorization", "Bearer "+token.Token, 204)
	check("/control", "api-token", token.Token, 403)
	if err = db.Model(user).Update("is_admin", true).Error; err != nil {
		t.Fatal(err)
	}
	check("/control", "api-token", token.Token, 204)
	if err = db.Model(user).Update("status", model.COMMON_STATUS_DISABLED).Error; err != nil {
		t.Fatal(err)
	}
	check("/api", "Authorization", "Bearer "+token.Token, 401)
	if err = db.Model(user).Update("status", model.COMMON_STATUS_ENABLE).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Delete(token).Error; err != nil {
		t.Fatal(err)
	}
	check("/api", "Authorization", "Bearer "+token.Token, 401)
	check("/control", "api-token", token.Token, 401)
}
