package service

import (
	"math"
	"testing"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/utils"
)

func TestAuditF05OAuthExpiredAndWrongPhase(t *testing.T) {
	s := &OauthService{}
	key := t.Name()
	t.Cleanup(func() { s.DeleteOauthCache(key) })
	item := &OauthCacheItem{Action: OauthActionTypeLogin, Op: "oidc"}
	for _, ttl := range []uint{0, 301} {
		if err := s.SetOauthCache(key, item, ttl); err == nil {
			t.Fatalf("accepted TTL %d", ttl)
		}
	}
	if err := s.SetOauthCache(key, item, 60); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOauthCache(key, item, 60); err == nil {
		t.Fatal("overwrote existing state")
	}
	if s.AuthorizeWebLogin(key, 1) {
		t.Fatal("web confirmation authorized OIDC flow")
	}
	if s.CompleteCallback(key, &model.OauthUser{OpenId: "subject"}, 1) {
		t.Fatal("completed before callback verification")
	}
	if s.StartCallback(key) == nil {
		t.Fatal("callback did not start")
	}
	if s.CompleteCallback(key, &model.OauthUser{}, 1) {
		t.Fatal("empty subject accepted")
	}
	if !s.CompleteCallback(key, &model.OauthUser{OpenId: "subject"}, 1) {
		t.Fatal("valid callback rejected")
	}
	oauthMu.Lock()
	oauthCache[key].expiresAt = time.Now().Add(-time.Second)
	oauthMu.Unlock()
	if s.ConsumeLogin(key) != nil || s.GetOauthCache(key) != nil {
		t.Fatal("expired state usable")
	}
}

func TestAuditF05OAuthEmailDoesNotMergeAndRollback(t *testing.T) {
	db := testDB(t)
	provider := model.Oauth{Op: "audit-provider", OauthType: model.OauthTypeOidc}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	local := model.User{Username: "local", Email: "same@example.test", Status: model.COMMON_STATUS_ENABLE}
	if err := db.Create(&local).Error; err != nil {
		t.Fatal(err)
	}
	svc := &UserService{}
	identity := &model.OauthUser{Username: "remote", OpenId: "subject", Email: local.Email}
	if err, u := svc.RegisterByOauth(identity, provider.Op); err == nil || u != nil {
		t.Fatal("matching email implicitly merged")
	}
	var n int64
	if err := db.Model(&model.UserThird{}).Count(&n).Error; err != nil || n != 0 {
		t.Fatalf("binding persisted: %d %v", n, err)
	}
	identity.Email = "new@example.test"
	if err := db.Exec("CREATE TRIGGER reject_binding BEFORE INSERT ON user_thirds BEGIN SELECT RAISE(ABORT,'injected binding failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err, u := svc.RegisterByOauth(identity, provider.Op); err == nil || u != nil {
		t.Fatal("binding failure swallowed")
	}
	if err := db.Model(&model.User{}).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("orphan account: %d %v", n, err)
	}
}

func TestAuditF09PasswordRevocationRollback(t *testing.T) {
	db := testDB(t)
	old, err := utils.EncryptPassword("old-password")
	if err != nil {
		t.Fatal(err)
	}
	u := model.User{Username: "password-user", Password: old, Status: model.COMMON_STATUS_ENABLE}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	token := model.UserToken{UserId: u.Id, Token: "existing-session"}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER reject_revoke BEFORE DELETE ON user_tokens BEGIN SELECT RAISE(ABORT,'injected revocation failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&UserService{}).UpdatePassword(&u, "new-password"); err == nil {
		t.Fatal("revocation failure swallowed")
	}
	var persisted model.User
	if err := db.First(&persisted, u.Id).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Password != old {
		t.Fatal("password changed despite rollback")
	}
	if err := db.First(&model.UserToken{}, token.Id).Error; err != nil {
		t.Fatal("session lost despite rollback", err)
	}
	if err := db.Exec("DROP TRIGGER reject_revoke").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&UserService{}).UpdatePassword(&u, "new-password"); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Model(&model.UserToken{}).Count(&n).Error; err != nil || n != 0 {
		t.Fatalf("session not revoked: %d %v", n, err)
	}
}

func TestAuditF09DeleteRollsBackEveryRelationship(t *testing.T) {
	db := testDB(t)
	u := model.User{Username: "rollback-user", Status: model.COMMON_STATUS_ENABLE}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	token := model.UserToken{UserId: u.Id, Token: "session"}
	peer := model.Peer{Id: "peer", Uuid: "uuid", UserId: u.Id}
	for _, row := range []interface{}{&token, &peer} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE TRIGGER reject_user_delete BEFORE DELETE ON users BEGIN SELECT RAISE(ABORT,'injected deletion failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&UserService{}).Delete(&u); err == nil {
		t.Fatal("deletion failure swallowed")
	}
	if err := db.First(&model.User{}, u.Id).Error; err != nil {
		t.Fatal("account removed", err)
	}
	if err := db.First(&model.UserToken{}, token.Id).Error; err != nil {
		t.Fatal("session not rolled back", err)
	}
	var got model.Peer
	if err := db.First(&got, peer.RowId).Error; err != nil || got.UserId != u.Id {
		t.Fatal("device detach not rolled back", err)
	}
}

func TestAuditF10PaginationBoundaries(t *testing.T) {
	db := testDB(t)
	for i := 0; i < 205; i++ {
		if err := db.Create(&model.Group{Name: "group"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name       string
		page, size uint
		want       int
	}{{"defaults", 0, 0, 10}, {"cap", 1, 1000, 200}, {"second", 2, 200, 5}, {"overflow", math.MaxUint, 200, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			var rows []model.Group
			if err := db.Scopes(Paginate(tc.page, tc.size)).Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.want {
				t.Fatalf("rows=%d want=%d", len(rows), tc.want)
			}
		})
	}
}
