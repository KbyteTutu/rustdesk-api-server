package service

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/lejianwen/rustdesk-api/v2/config"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/model/custom_types"
)

func TestDefaultCollectionIsolationAndRollback(t *testing.T) {
	db := testDB(t)
	fixtures := []model.AddressBook{
		{Id: "same", UserId: 1, Alias: "old", Tags: custom_types.AutoJson("[]")},
		{Id: "same", UserId: 1, CollectionId: 7, Alias: "named", Tags: custom_types.AutoJson("[]")},
		{Id: "same", UserId: 2, Alias: "foreign", Tags: custom_types.AutoJson("[]")},
	}
	if err := db.Create(&fixtures).Error; err != nil {
		t.Fatal(err)
	}
	namedTag := model.Tag{UserId: 1, CollectionId: 7, Name: "named", Color: 3}
	if err := db.Create(&namedTag).Error; err != nil {
		t.Fatal(err)
	}
	tags := map[string]uint{"fresh": 8}
	input := &model.AddressBook{RowId: fixtures[2].RowId, Id: "same", UserId: 2, CollectionId: 7, Tags: custom_types.AutoJson("[]")}
	svc := &AddressBookService{}
	if err := svc.SyncDefaultCollection([]*model.AddressBook{input}, tags, 1); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"", "named", "foreign"} {
		var got model.AddressBook
		if err := db.First(&got, fixtures[i].RowId).Error; err != nil {
			t.Fatal(err)
		}
		if got.Alias != want {
			t.Fatalf("row %d crossed ownership boundary: %q", i, got.Alias)
		}
	}
	if input.UserId != 2 || input.CollectionId != 7 || len(tags) != 1 {
		t.Fatal("caller input mutated")
	}
	var tag model.Tag
	if err := db.First(&tag, namedTag.Id).Error; err != nil || tag.Color != 3 {
		t.Fatal("named collection tag changed", err)
	}
	if err := db.Exec("CREATE TRIGGER fail_tags BEFORE INSERT ON tags BEGIN SELECT RAISE(ABORT,'injected failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	input.Alias = "must rollback"
	if err := svc.SyncDefaultCollection([]*model.AddressBook{input}, map[string]uint{"fail": 1}, 1); err == nil {
		t.Fatal("failure swallowed")
	}
	var got model.AddressBook
	db.First(&got, fixtures[0].RowId)
	if got.Alias != "" {
		t.Fatal("peer update committed despite tag failure")
	}
	if err := svc.SyncDefaultCollection([]*model.AddressBook{input, input}, nil, 1); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
}

func TestLastEnabledAdminInvariant(t *testing.T) {
	db := testDB(t)
	yes, no := true, false
	a := model.User{Username: "one", IsAdmin: &yes, Status: model.COMMON_STATUS_ENABLE}
	b := model.User{Username: "two", IsAdmin: &yes, Status: model.COMMON_STATUS_DISABLED}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	svc := &UserService{}
	if err := svc.Delete(&a); err == nil {
		t.Fatal("deleted last enabled admin")
	}
	a.IsAdmin = &no
	if err := svc.UpdateAdmin(&a); err == nil {
		t.Fatal("demoted last enabled admin")
	}
	a.IsAdmin = &yes
	a.Status = model.COMMON_STATUS_DISABLED
	if err := svc.UpdateAdmin(&a); err == nil {
		t.Fatal("disabled last enabled admin")
	}
	// Two concurrent deletes must leave one enabled administrator.
	b.Status = model.COMMON_STATUS_ENABLE
	if err := svc.UpdateAdmin(&b); err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, u := range []*model.User{&a, &b} {
		wg.Add(1)
		go func(u *model.User) {
			defer wg.Done()
			if svc.Delete(u) == nil {
				successes.Add(1)
			}
		}(u)
	}
	wg.Wait()
	var count int64
	db.Model(&model.User{}).Where("is_admin = ? AND status = ?", true, model.COMMON_STATUS_ENABLE).Count(&count)
	if successes.Load() != 1 || count != 1 {
		t.Fatalf("successes=%d admins=%d", successes.Load(), count)
	}
}

func TestUserDeletionCleansRelationshipsAndAnonymizesAudit(t *testing.T) {
	db := testDB(t)
	u := model.User{Username: "delete", Status: model.COMMON_STATUS_ENABLE}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	rows := []interface{}{
		&model.UserToken{UserId: u.Id, Token: "old"},
		&model.AddressBookCollection{UserId: u.Id, Name: "private"},
		&model.AddressBookCollectionRule{UserId: 99, Type: model.ShareAddressBookRuleTypePersonal, ToId: u.Id},
		&model.ShareRecord{UserId: u.Id},
		&model.Peer{Id: "device", Uuid: "uuid", UserId: u.Id},
		&model.LoginLog{UserId: u.Id, UserTokenId: 8, Ip: "127.0.0.1", DeviceId: "device", Uuid: "uuid"},
	}
	for _, r := range rows {
		if err := db.Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := (&UserService{}).Delete(&u); err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{&model.UserToken{}, &model.AddressBookCollection{}, &model.AddressBookCollectionRule{}, &model.ShareRecord{}} {
		var n int64
		db.Model(m).Count(&n)
		if n != 0 {
			t.Fatalf("dangling %T", m)
		}
	}
	var peer model.Peer
	db.First(&peer)
	if peer.UserId != 0 {
		t.Fatal("device not unbound")
	}
	var event model.LoginLog
	if err := db.First(&event).Error; err != nil {
		t.Fatal("audit evidence lost", err)
	}
	if event.UserId != 0 || event.UserTokenId != 0 || event.Ip != "" || event.Uuid != "" || event.DeviceId != "" {
		t.Fatal("audit identifiers retained")
	}
}

func TestLDAPEscapingAndAccountDisable(t *testing.T) {
	s := &LdapService{}
	filter := s.filterField("uid", "a*)(uid=*)\x00")
	if filter != "(uid=a\\2a\\29\\28uid=\\2a\\29\\00)" {
		t.Fatalf("unsafe filter %q", filter)
	}
	if _, err := ldap.CompileFilter(filter); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"", "uid)", "a=b", "a b"} {
		if s.filterField(field, "x") != "" {
			t.Fatal("invalid attribute accepted")
		}
	}
	cfg := &config.Ldap{}
	for _, tc := range []struct {
		value string
		want  bool
	}{{"512", true}, {"514", false}, {"", false}, {"invalid", false}} {
		if got := s.isUserEnabled(cfg, &LdapUser{EnableAttrValue: tc.value}); got != tc.want {
			t.Fatalf("UAC %q enabled=%v", tc.value, got)
		}
	}
}
