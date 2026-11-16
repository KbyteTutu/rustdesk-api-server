package service

import (
	"github.com/lejianwen/rustdesk-api/v2/config"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"testing"
)

func TestLDAPNeverAdoptsExistingLocalIdentity(t *testing.T) {
	db := testDB(t)
	local := model.User{Username: "alice", Email: "alice@example.test", Status: model.COMMON_STATUS_ENABLE}
	if err := db.Create(&local).Error; err != nil {
		t.Fatal(err)
	}
	cfg := &config.Ldap{Url: "ldaps://directory.example.test:636", User: config.LdapUser{Sync: true}}
	incoming := &LdapUser{Username: "alice", Dn: "uid=alice,dc=example,dc=test", Enabled: true}
	svc := &LdapService{}
	if _, err := svc.mapToLocalUser(cfg, incoming); err == nil {
		t.Fatal("LDAP adopted local account")
	}
	incoming.Username = "ldap-only"
	u, err := svc.mapToLocalUser(cfg, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if u.LdapDN != incoming.Dn || u.LdapURL != cfg.Url {
		t.Fatal("identity source not persisted")
	}
	if _, err := svc.mapToLocalUser(cfg, incoming); err != nil {
		t.Fatal(err)
	}
	incoming.Dn = "uid=another,dc=example,dc=test"
	if _, err := svc.mapToLocalUser(cfg, incoming); err == nil {
		t.Fatal("different DN adopted account")
	}
	incoming.Dn = u.LdapDN
	cfg.Url = "ldaps://another.example.test:636"
	if _, err := svc.mapToLocalUser(cfg, incoming); err == nil {
		t.Fatal("different directory adopted account")
	}
}

func TestLDAPRejectsInsecureTransportAndEmptyCredentials(t *testing.T) {
	svc := &LdapService{}
	for _, tc := range []struct {
		url            string
		verify         bool
		user, password string
	}{
		{"ldap://127.0.0.1", true, "admin", "password"},
		{"ldaps://127.0.0.1", false, "admin", "password"},
		{"ldaps://127.0.0.1", true, "", "password"},
		{"ldaps://127.0.0.1", true, "admin", ""},
	} {
		if conn, err := svc.connectAndBind(&config.Ldap{Url: tc.url, TlsVerify: tc.verify}, tc.user, tc.password); err == nil || conn != nil {
			t.Fatal("unsafe LDAP bind accepted")
		}
	}
}
