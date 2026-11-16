package router

// These tests exercise the real routers, validators, authentication, services and
// SQLite persistence. No listening ports, production configuration or external
// identity providers are used. Globals are restored; do not run in parallel.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/config"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/lib/jwt"
	"github.com/lejianwen/rustdesk-api/v2/lib/lock"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
	"github.com/lejianwen/rustdesk-api/v2/utils"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testPassword = "functional-password-42"

// The limiter has no shutdown API; share one process-scoped disabled instance.
var functionalLoginLimiter = utils.NewLoginLimiter(utils.SecurityPolicy{CaptchaThreshold: -1})

type fixture struct {
	t                                *testing.T
	db                               *gorm.DB
	router                           *gin.Engine
	admin, alice, bob                *model.User
	adminToken, aliceToken, bobToken string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "functional.db")), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	oldConfig, oldLogger, oldDB, oldJWT, oldLock := global.Config, global.Logger, global.DB, global.Jwt, global.Lock
	oldValidator, oldLocalizer, oldLimiter := global.Validator, global.Localizer, global.LoginLimiter
	sd, sc, sj, ss, sl, slog := service.DB, service.Config, service.Jwt, service.AllService, service.Lock, service.Logger
	oldMode := gin.Mode()
	t.Cleanup(func() {
		global.Config, global.Logger, global.DB, global.Jwt, global.Lock = oldConfig, oldLogger, oldDB, oldJWT, oldLock
		global.Validator, global.Localizer, global.LoginLimiter = oldValidator, oldLocalizer, oldLimiter
		service.DB, service.Config, service.Jwt, service.AllService, service.Lock, service.Logger = sd, sc, sj, ss, sl, slog
		gin.SetMode(oldMode)
		_ = sqlDB.Close()
	})
	global.Config = config.Config{Lang: "en", App: config.App{WebClient: 1, TokenExpire: time.Hour}}
	resources, err := filepath.Abs("../../resources")
	if err != nil {
		t.Fatal(err)
	}
	global.Config.Gin.ResourcesPath = resources
	global.Logger = logrus.New()
	global.Logger.SetOutput(io.Discard)
	global.DB = db
	global.Jwt = jwt.NewJwt(strings.Repeat("functional-key-", 3), time.Hour)
	global.Lock = lock.NewLocal()
	global.ApiInitValidator()
	global.InitI18n()
	global.LoginLimiter = functionalLoginLimiter
	service.New(&global.Config, db, global.Logger, global.Jwt, global.Lock)
	err = db.AutoMigrate(&model.User{}, &model.UserToken{}, &model.LoginLog{}, &model.Peer{}, &model.AddressBook{}, &model.AddressBookCollection{}, &model.AddressBookCollectionRule{}, &model.Tag{}, &model.UserThird{}, &model.Oauth{}, &model.ShareRecord{}, &model.Group{}, &model.DeviceGroup{}, &model.AuditConn{}, &model.AuditFile{}, &model.ServerCmd{})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	f := &fixture{t: t, db: db, router: gin.New()}
	if err = f.router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	Init(f.router)
	ApiInit(f.router)
	f.create(&model.Group{IdModel: model.IdModel{Id: 1}, Name: "functional-group"})
	password, err := utils.EncryptPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"admin", "alice", "bob"} {
		admin := i == 0
		u := &model.User{Username: name, Password: password, GroupId: 1, IsAdmin: &admin, Status: model.COMMON_STATUS_ENABLE}
		f.create(u)
		token, err := service.AllService.UserService.Login(u, &model.LoginLog{})
		if err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			f.admin = u
			f.adminToken = token.Token
		case 1:
			f.alice = u
			f.aliceToken = token.Token
		case 2:
			f.bob = u
			f.bobToken = token.Token
		}
	}
	return f
}
func (f *fixture) create(row interface{}) {
	f.t.Helper()
	if err := f.db.Create(row).Error; err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) request(method, path, token string, body interface{}) *httptest.ResponseRecorder {
	f.t.Helper()
	var raw []byte
	var err error
	if s, ok := body.(string); ok {
		raw = []byte(s)
	} else if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		if strings.HasPrefix(path, "/api/admin/") {
			req.Header.Set("api-token", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	out := httptest.NewRecorder()
	f.router.ServeHTTP(out, req)
	return out
}
func (f *fixture) ok(method, path, token string, body interface{}) map[string]interface{} {
	f.t.Helper()
	r := f.request(method, path, token, body)
	if r.Code != 200 {
		f.t.Fatalf("%s %s status=%d body=%s", method, path, r.Code, r.Body.String())
	}
	var result map[string]interface{}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		f.t.Fatalf("%s invalid JSON: %v", path, err)
	}
	if result["code"] != float64(0) {
		f.t.Fatalf("%s failed: %s", path, r.Body.String())
	}
	return result
}
func (f *fixture) status(method, path, token string, body interface{}, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	r := f.request(method, path, token, body)
	if r.Code != want {
		f.t.Fatalf("%s %s status=%d want=%d body=%s", method, path, r.Code, want, r.Body.String())
	}
	return r
}
func (f *fixture) denied(method, path, token string, body interface{}) {
	f.t.Helper()
	r := f.request(method, path, token, body)
	if r.Code >= 400 && r.Code < 500 {
		return
	}
	var result map[string]interface{}
	err := json.Unmarshal(r.Body.Bytes(), &result)
	if r.Code != 200 || err != nil || result["code"] == nil || result["code"] == float64(0) {
		f.t.Fatalf("expected rejection %s %s: %d %s", method, path, r.Code, r.Body.String())
	}
}
func dataMap(r map[string]interface{}) map[string]interface{} {
	return r["data"].(map[string]interface{})
}

func TestFunctionalRouteAuthenticationMatrix(t *testing.T) {
	f := newFixture(t)
	public := map[string]bool{}
	for _, p := range []string{"GET /api/", "GET /api/version", "GET /api/login-options", "POST /api/login", "POST /api/oidc/auth", "GET /api/oidc/auth-query", "GET /api/oauth/callback", "GET /api/oauth/login", "GET /api/oauth/msg", "GET /api/oidc/callback", "GET /api/oidc/login", "GET /api/oidc/msg", "POST /api/sysinfo_ver", "POST /api/shared-peer", "POST /api/admin/login", "GET /api/admin/captcha", "GET /api/admin/login-options", "POST /api/admin/oidc/auth", "GET /api/admin/oidc/auth-query", "POST /api/admin/user/register", "GET /api/admin/config/admin"} {
		public[p] = true
	}
	protected := 0
	for _, route := range f.router.Routes() {
		key := route.Method + " " + route.Path
		if public[key] {
			delete(public, key)
			continue
		}
		protected++
		t.Run(key, func(t *testing.T) {
			path := strings.ReplaceAll(strings.ReplaceAll(route.Path, ":id", "999"), ":guid", "1-2-0")
			for _, token := range []string{"", "invalid-token"} {
				f.status(route.Method, path, token, map[string]interface{}{}, 401)
			}
		})
	}
	if len(public) != 0 {
		t.Fatalf("public contract contains missing routes: %v", public)
	}
	t.Logf("checked %d protected routes with absent and invalid credentials", protected)
}

func TestFunctionalAdminPrivilegeMatrix(t *testing.T) {
	f := newFixture(t)
	n := 0
	prefixes := []string{"/user/list", "/user/detail/", "/user/create", "/user/update", "/user/delete", "/user/changePwd", "/group/", "/device_group/", "/tag/", "/address_book/", "/peer/", "/oauth/list", "/oauth/detail/", "/oauth/create", "/oauth/update", "/oauth/delete", "/login_log/", "/audit_conn/", "/audit_file/", "/address_book_collection/", "/address_book_collection_rule/", "/user_token/", "/share_record/", "/rustdesk/"}
	for _, route := range f.router.Routes() {
		short := strings.TrimPrefix(route.Path, "/api/admin/")
		if short == route.Path || short == "address_book/shareByWebClient" || short == "peer/simpleData" {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix("/"+short, prefix) {
				n++
				t.Run(route.Method+" "+route.Path, func(t *testing.T) {
					f.status(route.Method, strings.ReplaceAll(route.Path, ":id", "999"), f.aliceToken, map[string]interface{}{}, 403)
				})
				break
			}
		}
	}
	t.Logf("checked %d administrator-only routes", n)
}

func TestFunctionalLoginRegistrationAndSessionLifecycle(t *testing.T) {
	f := newFixture(t)
	login := map[string]interface{}{"username": "alice", "password": testPassword}
	token := dataMap(f.ok("POST", "/api/admin/login", "", login))["token"].(string)
	f.ok("GET", "/api/admin/user/current", token, nil)
	f.denied("POST", "/api/admin/login", "", map[string]string{"username": "alice", "password": "incorrect"})
	f.denied("POST", "/api/admin/login", "", "{")
	f.ok("POST", "/api/admin/logout", token, map[string]interface{}{})
	f.status("GET", "/api/admin/user/current", token, nil, 401)
	registration := map[string]interface{}{"username": "registered", "password": testPassword, "confirm_password": testPassword}
	f.denied("POST", "/api/admin/user/register", "", registration)
	global.Config.App.Register = true
	global.Config.App.RegisterStatus = int(model.COMMON_STATUS_ENABLE)
	bad := map[string]interface{}{"username": "bad-confirm", "password": testPassword, "confirm_password": "different"}
	f.denied("POST", "/api/admin/user/register", "", bad)
	f.ok("POST", "/api/admin/user/register", "", registration)
	f.denied("POST", "/api/admin/user/register", "", registration)
	f.ok("POST", "/api/admin/login", "", map[string]string{"username": "registered", "password": testPassword})
	f.denied("POST", "/api/admin/user/changeCurPwd", f.aliceToken, map[string]string{"old_password": "wrong", "new_password": "new-password-42"})
	f.ok("POST", "/api/admin/user/changeCurPwd", f.aliceToken, map[string]string{"old_password": testPassword, "new_password": "new-password-42"})
	f.status("GET", "/api/admin/user/current", f.aliceToken, nil, 401)
	f.denied("POST", "/api/admin/login", "", login)
	f.ok("POST", "/api/admin/login", "", map[string]string{"username": "alice", "password": "new-password-42"})
	global.Config.App.DisablePwdLogin = true
	f.denied("POST", "/api/admin/login", "", map[string]string{"username": "alice", "password": "new-password-42"})
}

func TestFunctionalAdminCRUD(t *testing.T) {
	for _, kind := range []string{"group", "device_group", "tag", "address_book_collection"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			base := "/api/admin/" + kind
			body := map[string]interface{}{"name": "functional-created", "type": 1, "user_id": f.alice.Id, "color": 123}
			f.ok("POST", base+"/create", f.adminToken, body)
			list := dataMap(f.ok("GET", base+"/list?page=1&page_size=200", f.adminToken, nil))["list"].([]interface{})
			var id float64
			for _, item := range list {
				row := item.(map[string]interface{})
				if row["name"] == "functional-created" {
					id = row["id"].(float64)
				}
			}
			if id == 0 {
				t.Fatal("created item missing from list")
			}
			f.ok("GET", fmt.Sprintf("%s/detail/%d", base, int(id)), f.adminToken, nil)
			body["id"] = id
			body["name"] = "functional-updated"
			body["color"] = 456
			f.ok("POST", base+"/update", f.adminToken, body)
			row := dataMap(f.ok("GET", fmt.Sprintf("%s/detail/%d", base, int(id)), f.adminToken, nil))
			if row["name"] != "functional-updated" {
				t.Fatalf("update not persisted: %v", row)
			}
			f.ok("POST", base+"/delete", f.adminToken, map[string]interface{}{"id": id})
			f.denied("GET", fmt.Sprintf("%s/detail/%d", base, int(id)), f.adminToken, nil)
			f.denied("POST", base+"/create", f.adminToken, "{")
			f.denied("POST", base+"/create", f.adminToken, map[string]interface{}{})
		})
	}
}

func TestFunctionalUserCRUDAndDisable(t *testing.T) {
	f := newFixture(t)
	form := map[string]interface{}{"username": "created-user", "password": testPassword, "group_id": 1, "status": 1, "is_admin": false, "nickname": "Created"}
	f.ok("POST", "/api/admin/user/create", f.adminToken, form)
	f.denied("POST", "/api/admin/user/create", f.adminToken, form)
	var u model.User
	if err := f.db.Where("username = ?", "created-user").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	if u.Password == testPassword || u.Password == "" {
		t.Fatal("password not hashed")
	}
	f.ok("GET", fmt.Sprintf("/api/admin/user/detail/%d", u.Id), f.adminToken, nil)
	token := dataMap(f.ok("POST", "/api/admin/login", "", map[string]string{"username": u.Username, "password": testPassword}))["token"].(string)
	form["id"] = u.Id
	form["status"] = model.COMMON_STATUS_DISABLED
	form["nickname"] = ""
	f.ok("POST", "/api/admin/user/update", f.adminToken, form)
	f.status("GET", "/api/admin/user/current", token, nil, 401)
	f.denied("POST", "/api/admin/login", "", map[string]string{"username": u.Username, "password": testPassword})
	form["status"] = model.COMMON_STATUS_ENABLE
	f.ok("POST", "/api/admin/user/update", f.adminToken, form)
	f.ok("POST", "/api/admin/user/changePwd", f.adminToken, map[string]interface{}{"id": u.Id, "password": "reset-password-42"})
	f.ok("POST", "/api/admin/login", "", map[string]string{"username": u.Username, "password": "reset-password-42"})
	f.ok("POST", "/api/admin/user/delete", f.adminToken, map[string]interface{}{"id": u.Id})
	f.denied("GET", fmt.Sprintf("/api/admin/user/detail/%d", u.Id), f.adminToken, nil)
}
