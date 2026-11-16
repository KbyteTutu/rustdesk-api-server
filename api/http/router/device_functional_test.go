package router

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
)

func TestFunctionalNativeDeviceAndAuditLifecycle(t *testing.T) {
	f := newFixture(t)
	login := map[string]interface{}{"username": "alice", "password": testPassword, "id": "native-device", "uuid": "native-uuid", "deviceInfo": map[string]string{"os": "linux", "type": "rustdesk"}}
	response := f.status("POST", "/api/login", "", login, 200)
	var auth struct {
		AccessToken string `json:"access_token"`
		Type        string `json:"type"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &auth); err != nil || auth.AccessToken == "" || auth.Type != "access_token" {
		t.Fatalf("native login: %s", response.Body.String())
	}
	token := auth.AccessToken
	f.status("GET", "/api/user/info", token, nil, 200)
	f.status("POST", "/api/currentUser", token, map[string]interface{}{}, 200)
	sysinfo := map[string]interface{}{"id": "native-device", "uuid": "native-uuid", "hostname": "functional-host", "os": "Linux", "username": "tester", "version": "1.5.3"}
	result := f.status("POST", "/api/sysinfo", token, sysinfo, 200)
	if result.Body.String() != "SYSINFO_UPDATED" {
		t.Fatalf("sysinfo: %s", result.Body.String())
	}
	f.status("POST", "/api/sysinfo", token, sysinfo, 200)
	heartbeat := map[string]interface{}{"id": "native-device", "uuid": "native-uuid"}
	f.status("POST", "/api/heartbeat", token, heartbeat, 200)
	var peer model.Peer
	if err := f.db.Where("id = ?", "native-device").First(&peer).Error; err != nil {
		t.Fatal(err)
	}
	if peer.UserId != f.alice.Id || peer.Hostname != "functional-host" || peer.LastOnlineTime == 0 {
		t.Fatalf("telemetry not persisted: %+v", peer)
	}
	for _, path := range []string{"/api/heartbeat", "/api/sysinfo", "/api/audit/conn", "/api/audit/file"} {
		f.status("POST", path, f.bobToken, heartbeat, 403)
		f.status("POST", path, token, map[string]string{"id": "native-device", "uuid": "wrong"}, 403)
	}
	conn := map[string]interface{}{"id": "native-device", "uuid": "native-uuid", "action": "new", "conn_id": 1234, "peer": []string{"remote-id", "Remote"}, "session_id": 42, "type": 0, "ip": "1.2.3.4"}
	f.ok("POST", "/api/audit/conn", token, conn)
	conn["action"] = ""
	conn["peer"] = []string{"updated-remote", "Updated"}
	f.ok("POST", "/api/audit/conn", token, conn)
	conn["action"] = "close"
	f.ok("POST", "/api/audit/conn", token, conn)
	var event model.AuditConn
	if err := f.db.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.CloseTime == 0 || event.FromPeer != "updated-remote" || event.Ip == "1.2.3.4" || event.SessionId != "42" {
		t.Fatalf("audit lifecycle: %+v", event)
	}
	file := map[string]interface{}{"id": "native-device", "uuid": "native-uuid", "peer_id": "remote-id", "is_file": true, "path": "/test.txt", "info": "{\"name\":\"Remote\",\"num\":1,\"ip\":\"1.2.3.4\"}"}
	f.ok("POST", "/api/audit/file", token, file)
	var transfer model.AuditFile
	if err := f.db.First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	if transfer.PeerId != "native-device" || transfer.FromName != "Remote" || transfer.Path != "/test.txt" || transfer.Ip == "1.2.3.4" {
		t.Fatalf("file audit: %+v", transfer)
	}
	for _, kind := range []string{"audit_conn", "audit_file"} {
		list := dataMap(f.ok("GET", "/api/admin/"+kind+"/list", f.adminToken, nil))
		if list["total"] != float64(1) {
			t.Fatalf("%s count: %v", kind, list)
		}
		id := event.Id
		if kind == "audit_file" {
			id = transfer.Id
		}
		f.ok("POST", "/api/admin/"+kind+"/delete", f.adminToken, map[string]interface{}{"id": id})
	}
	f.status("POST", "/api/logout", token, map[string]interface{}{}, 200)
	f.status("GET", "/api/user/info", token, nil, 401)
}

func TestFunctionalPeerCRUDSearchAndBatchDelete(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 2; i++ {
		body := map[string]interface{}{"id": fmt.Sprintf("managed-%d", i), "uuid": fmt.Sprintf("uuid-%d", i), "alias": "initial", "hostname": "test-host"}
		f.ok("POST", "/api/admin/peer/create", f.adminToken, body)
	}
	f.denied("POST", "/api/admin/peer/create", f.adminToken, map[string]string{"id": "managed-1", "uuid": "different"})
	list := dataMap(f.ok("GET", "/api/admin/peer/list?uuids=uuid-1&hostname=test", f.adminToken, nil))
	if list["total"] != float64(1) {
		t.Fatalf("filter failed: %v", list)
	}
	var peers []model.Peer
	if err := f.db.Order("row_id").Find(&peers).Error; err != nil || len(peers) != 2 {
		t.Fatalf("peers: %v %v", peers, err)
	}
	f.ok("POST", "/api/admin/peer/update", f.adminToken, map[string]interface{}{"row_id": peers[0].RowId, "id": peers[0].Id, "uuid": peers[0].Uuid, "alias": ""})
	detail := dataMap(f.ok("GET", fmt.Sprintf("/api/admin/peer/detail/%d", peers[0].RowId), f.adminToken, nil))
	if detail["alias"] != "" {
		t.Fatal("cannot clear alias", detail)
	}
	f.status("GET", "/api/admin/peer/list?uuids="+strings.Repeat("x", 129), f.adminToken, nil, 400)
	f.ok("POST", "/api/admin/peer/batchDelete", f.adminToken, map[string]interface{}{"row_ids": []uint{peers[0].RowId, peers[1].RowId}})
	var count int64
	if err := f.db.Model(&model.Peer{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("batch delete %d %v", count, err)
	}
}

func TestFunctionalReadEndpointsAndFeatureFlags(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/api/", "/api/version", "/api/admin/config/admin"} {
		f.ok("GET", path, "", nil)
	}
	for _, path := range []string{"/api/login-options", "/api/admin/login-options"} {
		f.status("GET", path, "", nil, 200)
	}
	f.status("POST", "/api/sysinfo_ver", "", map[string]interface{}{}, 200)
	for _, path := range []string{"config/server", "config/app", "user/current", "my/peer/list", "my/login_log/list", "my/tag/list", "my/address_book_collection/list", "my/address_book_collection_rule/list", "my/share_record/list", "login_log/list", "user_token/list", "oauth/list", "share_record/list", "rustdesk/cmdList"} {
		t.Run(path, func(t *testing.T) { f.ok("GET", "/api/admin/"+path, f.adminToken, nil) })
	}
	for _, path := range []string{"/api/users", "/api/peers"} {
		f.status("GET", path, f.aliceToken, nil, 200)
	}
	f.status("GET", "/api/device-group/accessible", f.aliceToken, nil, 400)
	f.status("GET", "/api/device-group/accessible", f.adminToken, nil, 200)
	for _, path := range []string{"/api/server-config", "/api/server-config-v2"} {
		f.ok("POST", path, f.aliceToken, map[string]interface{}{})
	}
	global.Config.App.WebClient = 0
	// Register a fresh router to test registration-time feature gating.
	disabled := gin.New()
	Init(disabled)
	ApiInit(disabled)
	for _, path := range []string{"/api/shared-peer", "/api/server-config", "/api/server-config-v2"} {
		original := f.router
		f.router = disabled
		f.status("POST", path, f.aliceToken, map[string]interface{}{}, 404)
		f.router = original
	}
}
