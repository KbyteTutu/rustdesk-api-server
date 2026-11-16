package service

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/config"
	"github.com/lejianwen/rustdesk-api/v2/lib/jwt"
	"github.com/lejianwen/rustdesk-api/v2/lib/lock"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	savedDB, savedConfig, savedJWT, savedService, savedLock, savedLogger := DB, Config, Jwt, AllService, Lock, Logger
	New(&config.Config{}, db, logrus.New(), jwt.NewJwt(strings.Repeat("k", 32), time.Hour), lock.NewLocal())
	t.Cleanup(func() {
		DB, Config, Jwt, AllService, Lock, Logger = savedDB, savedConfig, savedJWT, savedService, savedLock, savedLogger
		sqlDB.Close()
	})
	if err := db.AutoMigrate(&model.User{}, &model.UserToken{}, &model.LoginLog{}, &model.Peer{}, &model.AddressBook{}, &model.AddressBookCollection{}, &model.Tag{}, &model.UserThird{}, &model.Oauth{}, &model.AddressBookCollectionRule{}, &model.ShareRecord{}, &model.Group{}, &model.DeviceGroup{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLoginPersistsExactExpiryAndSessionLink(t *testing.T) {
	db := testDB(t)
	user := &model.User{IdModel: model.IdModel{Id: 42}, Username: "test", Status: model.COMMON_STATUS_ENABLE}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	log := &model.LoginLog{}
	token, err := (&UserService{}).Login(user, log)
	if err != nil {
		t.Fatal(err)
	}
	if log.UserTokenId != token.Id || token.UserId != 42 {
		t.Fatal("session log incorrectly linked")
	}
	if token.ExpiredAt < time.Now().Add(59*time.Minute).Unix() || token.ExpiredAt > time.Now().Add(time.Hour).Unix() {
		t.Fatal("invalid expiry")
	}
	if uid, err := Jwt.ParseToken(token.Token); err != nil || uid != 42 {
		t.Fatal("invalid token", err)
	}
	for _, bad := range []*model.User{nil, {}, {IdModel: model.IdModel{Id: 1}, Status: model.COMMON_STATUS_DISABLED}} {
		if _, err := (&UserService{}).Login(bad, &model.LoginLog{}); err == nil {
			t.Fatal("invalid account logged in")
		}
	}
}

func TestLoginRollsBackOnLogFailure(t *testing.T) {
	db := testDB(t)
	if err := db.Exec("CREATE TRIGGER fail_login BEFORE INSERT ON login_logs BEGIN SELECT RAISE(ABORT,'injected failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	user := &model.User{IdModel: model.IdModel{Id: 42}, Status: model.COMMON_STATUS_ENABLE}
	if token, err := (&UserService{}).Login(user, &model.LoginLog{}); err == nil || token != nil {
		t.Fatal("reported success after persistence failure")
	}
	var n int64
	db.Model(&model.UserToken{}).Count(&n)
	if n != 0 {
		t.Fatal("orphan session")
	}
}

func TestOauthFlowTransitionsAndSingleConsumption(t *testing.T) {
	s := &OauthService{}
	key := t.Name()
	s.SetOauthCache(key, &OauthCacheItem{Action: OauthActionTypeLogin, Op: model.OauthTypeWebauth}, 60)
	t.Cleanup(func() { s.DeleteOauthCache(key) })
	copy := s.GetOauthCache(key)
	copy.UserId = 99
	if s.GetOauthCache(key).UserId != 0 {
		t.Fatal("mutable cache escaped")
	}
	if s.ConsumeLogin(key) != nil {
		t.Fatal("pending flow consumed")
	}
	if !s.AuthorizeWebLogin(key, 42) || s.AuthorizeWebLogin(key, 43) {
		t.Fatal("invalid authorization transition")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.ConsumeLogin(key) != nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("consumed %d times", successes.Load())
	}
	s.SetOauthCache(key, &OauthCacheItem{Action: OauthActionTypeBind, Op: "oidc", UserId: 42}, 60)
	if s.ConsumeLogin(key) != nil || s.AuthorizeWebLogin(key, 43) {
		t.Fatal("bind flow became login")
	}
	if s.StartCallback(key) == nil || s.StartCallback(key) != nil {
		t.Fatal("callback replay accepted")
	}
	if s.CompleteCallback(key, &model.OauthUser{OpenId: "subject"}, 42) {
		t.Fatal("bind callback became login")
	}
}

func TestOauthUniqueIdentity(t *testing.T) {
	db := testDB(t)
	one := model.UserThird{UserId: 1, Op: "provider", OauthUser: model.OauthUser{OpenId: "subject"}}
	if err := db.Create(&one).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []model.UserThird{{UserId: 2, Op: "provider", OauthUser: model.OauthUser{OpenId: "subject"}}, {UserId: 1, Op: "provider", OauthUser: model.OauthUser{OpenId: "other"}}} {
		if err := db.Create(&row).Error; err == nil {
			t.Fatal("duplicate binding accepted")
		}
	}
}
