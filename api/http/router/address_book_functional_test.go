package router

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/model/custom_types"
)

func TestFunctionalDefaultAddressBookRoundTrip(t *testing.T) {
	f := newFixture(t)
	foreign := &model.AddressBook{Id: "foreign", UserId: f.bob.Id, Tags: custom_types.AutoJson("[]")}
	f.create(foreign)
	payload := map[string]interface{}{"peers": []interface{}{map[string]interface{}{"id": "device-1", "alias": "Laptop", "tags": []string{"work"}, "user_id": f.bob.Id}}, "tags": []string{"work"}, "tag_colors": "{\"work\":123}"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	f.status("POST", "/api/ab", f.aliceToken, map[string]string{"data": string(raw)}, 200)
	out := f.status("GET", "/api/ab", f.aliceToken, nil, 200)
	var envelope struct {
		Data string `json:"data"`
	}
	if err = json.Unmarshal(out.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Peers []struct {
			Id     string `json:"id"`
			Alias  string `json:"alias"`
			UserId uint   `json:"user_id"`
		} `json:"peers"`
		TagColors string `json:"tag_colors"`
	}
	if err = json.Unmarshal([]byte(envelope.Data), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Peers) != 1 || result.Peers[0].Id != "device-1" || result.Peers[0].Alias != "Laptop" || result.Peers[0].UserId != f.alice.Id {
		t.Fatalf("round trip/ownership failed: %+v", result)
	}
	var colors map[string]uint
	if err = json.Unmarshal([]byte(result.TagColors), &colors); err != nil || colors["work"] != 123 {
		t.Fatalf("tag colors: %v %v", colors, err)
	}
	for _, bad := range []interface{}{"{", map[string]string{"data": "invalid"}, map[string]string{"data": "{\"tag_colors\":\"invalid\"}"}} {
		f.status("POST", "/api/ab", f.aliceToken, bad, 400)
	}
	f.status("POST", "/api/ab", f.aliceToken, map[string]string{"data": "{\"peers\":[],\"tag_colors\":\"{}\"}"}, 200)
	var count int64
	if err = f.db.Model(&model.AddressBook{}).Where("user_id = ?", f.alice.Id).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("replacement failed count=%d err=%v", count, err)
	}
	if err = f.db.First(&model.AddressBook{}, foreign.RowId).Error; err != nil {
		t.Fatal("foreign address book removed", err)
	}
}

func TestFunctionalPersonalPeersAndTags(t *testing.T) {
	f := newFixture(t)
	global.Config.Rustdesk.Personal = 1
	guid := fmt.Sprintf("1-%d-0", f.alice.Id)
	out := f.status("POST", "/api/ab/personal", f.aliceToken, map[string]interface{}{}, 200)
	var personal map[string]interface{}
	if err := json.Unmarshal(out.Body.Bytes(), &personal); err != nil || personal["guid"] != guid {
		t.Fatalf("personal guid: %s", out.Body.String())
	}
	f.status("POST", "/api/ab/settings", f.aliceToken, map[string]interface{}{}, 200)
	f.status("POST", "/api/ab/peer/add/"+guid, f.aliceToken, map[string]interface{}{"id": "personal-device", "alias": "before", "tags": []string{}}, 200)
	f.status("PUT", "/api/ab/peer/update/"+guid, f.aliceToken, map[string]interface{}{"id": "personal-device", "alias": "", "tags": []string{"work"}, "user_id": f.bob.Id}, 200)
	var peer model.AddressBook
	if err := f.db.Where("id = ?", "personal-device").First(&peer).Error; err != nil {
		t.Fatal(err)
	}
	if peer.Alias != "" || peer.UserId != f.alice.Id || string(peer.Tags) != "[\"work\"]" {
		t.Fatalf("peer update: %+v", peer)
	}
	for _, body := range []interface{}{map[string]interface{}{"id": 42}, map[string]interface{}{"id": "personal-device", "alias": false}, map[string]interface{}{"id": "personal-device", "tags": []int{1}}} {
		f.status("PUT", "/api/ab/peer/update/"+guid, f.aliceToken, body, 400)
	}
	f.status("POST", "/api/ab/tag/add/"+guid, f.aliceToken, map[string]interface{}{"name": "work", "color": 123}, 200)
	f.status("POST", "/api/ab/tag/add/"+guid, f.aliceToken, map[string]interface{}{"name": "work", "color": 123}, 400)
	f.status("PUT", "/api/ab/tag/rename/"+guid, f.aliceToken, map[string]string{"old": "work", "new": "office"}, 200)
	f.status("PUT", "/api/ab/tag/update/"+guid, f.aliceToken, map[string]interface{}{"name": "office", "color": 456}, 200)
	tags := f.status("POST", "/api/ab/tags/"+guid, f.aliceToken, map[string]interface{}{}, 200)
	var rows []struct {
		Name  string `json:"name"`
		Color uint   `json:"color"`
	}
	if err := json.Unmarshal(tags.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Name != "office" || rows[0].Color != 456 {
		t.Fatalf("tags: %s", tags.Body.String())
	}
	f.status("POST", "/api/ab/peers?ab="+guid, f.bobToken, map[string]interface{}{}, 400)
	f.status("DELETE", "/api/ab/tag/"+guid, f.aliceToken, []string{"office"}, 200)
	f.status("DELETE", "/api/ab/peer/"+guid, f.aliceToken, []string{"personal-device"}, 200)
	var count int64
	if err := f.db.Model(&model.AddressBook{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("peer deletion: %d %v", count, err)
	}
}

func TestFunctionalSharedCollectionPermissionLevels(t *testing.T) {
	for _, rule := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("rule_%d", rule), func(t *testing.T) {
			f := newFixture(t)
			c := &model.AddressBookCollection{UserId: f.alice.Id, Name: "shared"}
			f.create(c)
			f.ok("POST", "/api/admin/my/address_book_collection_rule/create", f.aliceToken, map[string]interface{}{"collection_id": c.Id, "type": 1, "rule": rule, "to_id": f.bob.Id})
			guid := fmt.Sprintf("1-%d-%d", f.alice.Id, c.Id)
			f.status("POST", "/api/ab/peer/add/"+guid, f.aliceToken, map[string]interface{}{"id": "shared-device", "tags": []string{}}, 200)
			f.status("POST", "/api/ab/peers?ab="+guid, f.bobToken, map[string]interface{}{}, 200)
			profiles := f.status("POST", "/api/ab/shared/profiles", f.bobToken, map[string]interface{}{}, 200)
			var p struct {
				Data []struct {
					Guid string `json:"guid"`
					Rule int    `json:"rule"`
				} `json:"data"`
			}
			if err := json.Unmarshal(profiles.Body.Bytes(), &p); err != nil || len(p.Data) != 1 || p.Data[0].Guid != guid || p.Data[0].Rule != rule {
				t.Fatalf("shared profiles: %s", profiles.Body.String())
			}
			writeStatus := 200
			if rule == 1 {
				writeStatus = 400
			}
			f.status("PUT", "/api/ab/peer/update/"+guid, f.bobToken, map[string]string{"id": "shared-device", "alias": "changed"}, writeStatus)
			deleteStatus := 400
			if rule == 3 {
				deleteStatus = 200
			}
			f.status("DELETE", "/api/ab/peer/"+guid, f.bobToken, []string{"shared-device"}, deleteStatus)
			f.denied("POST", "/api/admin/my/address_book_collection/delete", f.bobToken, map[string]interface{}{"id": c.Id})
			f.status("POST", "/api/admin/my/address_book_collection/update", f.aliceToken, map[string]interface{}{"id": c.Id, "name": "hijack", "user_id": f.bob.Id}, 403)
			var rules []model.AddressBookCollectionRule
			if err := f.db.Find(&rules).Error; err != nil || len(rules) != 1 {
				t.Fatal("missing rule", err)
			}
			f.ok("POST", "/api/admin/my/address_book_collection_rule/delete", f.aliceToken, map[string]interface{}{"id": rules[0].Id})
			f.status("POST", "/api/ab/peers?ab="+guid, f.bobToken, map[string]interface{}{}, 400)
			f.ok("POST", "/api/admin/my/address_book_collection/delete", f.aliceToken, map[string]interface{}{"id": c.Id})
		})
	}
}

func TestFunctionalMyAddressBookOwnershipAndCRUD(t *testing.T) {
	f := newFixture(t)
	c := &model.AddressBookCollection{UserId: f.bob.Id, Name: "private-bob"}
	f.create(c)
	base := "/api/admin/my/address_book"
	form := map[string]interface{}{"id": "my-device", "alias": "original", "tags": []string{}, "user_id": f.bob.Id}
	f.ok("POST", base+"/create", f.aliceToken, form)
	var ab model.AddressBook
	if err := f.db.Where("id = ?", "my-device").First(&ab).Error; err != nil {
		t.Fatal(err)
	}
	if ab.UserId != f.alice.Id {
		t.Fatal("forged owner accepted")
	}
	form["row_id"] = ab.RowId
	form["user_id"] = f.alice.Id
	form["alias"] = "updated"
	f.denied("POST", base+"/update", f.bobToken, form)
	f.ok("POST", base+"/update", f.aliceToken, form)
	form["collection_id"] = c.Id
	f.denied("POST", base+"/update", f.aliceToken, form)
	f.denied("POST", base+"/delete", f.bobToken, map[string]interface{}{"row_id": ab.RowId})
	list := dataMap(f.ok("GET", base+"/list?user_id=2", f.bobToken, nil))
	if list["total"] != float64(0) {
		t.Fatalf("foreign list leaked: %v", list)
	}
	f.ok("POST", base+"/delete", f.aliceToken, map[string]interface{}{"row_id": ab.RowId})
}

func TestFunctionalWebShareLifecycle(t *testing.T) {
	f := newFixture(t)
	f.create(&model.AddressBook{Id: "share-device", UserId: f.alice.Id, Hostname: "test-host", Tags: custom_types.AutoJson("[]")})
	form := map[string]interface{}{"id": "share-device", "password_type": "fixed", "password": "device-password", "expire": 60}
	f.denied("POST", "/api/admin/address_book/shareByWebClient", f.bobToken, form)
	f.ok("POST", "/api/admin/address_book/shareByWebClient", f.aliceToken, form)
	var share model.ShareRecord
	if err := f.db.First(&share).Error; err != nil {
		t.Fatal(err)
	}
	if share.ShareToken == "" {
		t.Fatal("missing capability token")
	}
	f.ok("POST", "/api/shared-peer", "", map[string]string{"share_token": share.ShareToken})
	f.denied("POST", "/api/shared-peer", "", map[string]string{"share_token": "not-found"})
	f.status("POST", "/api/shared-peer", "", map[string]interface{}{}, 400)
	f.status("POST", "/api/shared-peer?share_token="+share.ShareToken, "", map[string]interface{}{}, 400)
	if err := f.db.Model(&share).Update("created_at", time.Now().Add(-2*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	f.denied("POST", "/api/shared-peer", "", map[string]string{"share_token": share.ShareToken})
	f.denied("POST", "/api/admin/my/share_record/delete", f.bobToken, map[string]interface{}{"id": share.Id})
	f.ok("POST", "/api/admin/my/share_record/delete", f.aliceToken, map[string]interface{}{"id": share.Id})
	f.denied("POST", "/api/shared-peer", "", map[string]string{"share_token": share.ShareToken})
}
