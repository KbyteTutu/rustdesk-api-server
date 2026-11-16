package service

import (
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/model/custom_types"
	"testing"
)

func TestAuditF08RejectInvalidBatchWithoutDeletingData(t *testing.T) {
	db := testDB(t)
	original := model.AddressBook{Id: "keep", UserId: 1, Tags: custom_types.AutoJson("[]")}
	if err := db.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	manyPeers := make([]*model.AddressBook, 1001)
	manyTags := make(map[string]uint, 1001)
	for i := 0; i < 1001; i++ {
		manyTags[string(rune(i+1))] = 1
	}
	for _, tc := range []struct {
		name  string
		peers []*model.AddressBook
		tags  map[string]uint
		uid   uint
	}{{"zero-owner", nil, nil, 0}, {"nil-peer", []*model.AddressBook{nil}, nil, 1}, {"empty-id", []*model.AddressBook{{}}, nil, 1}, {"too-many-peers", manyPeers, nil, 1}, {"too-many-tags", nil, manyTags, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := (&AddressBookService{}).SyncDefaultCollection(tc.peers, tc.tags, tc.uid); err == nil {
				t.Fatal("invalid batch accepted")
			}
			var got model.AddressBook
			if err := db.First(&got, original.RowId).Error; err != nil || got.Id != "keep" {
				t.Fatal("existing data changed", err)
			}
		})
	}
}

func TestAuditF08EmptySyncOnlyClearsDefaultCollection(t *testing.T) {
	db := testDB(t)
	for _, owner := range []uint{1, 2} {
		for _, collection := range []uint{0, 7} {
			for _, row := range []interface{}{&model.AddressBook{Id: "peer", UserId: owner, CollectionId: collection, Tags: custom_types.AutoJson("[]")}, &model.Tag{UserId: owner, CollectionId: collection, Name: "tag"}} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := (&AddressBookService{}).SyncDefaultCollection(nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{&model.AddressBook{}, &model.Tag{}} {
		var remaining, removed int64
		if err := db.Model(m).Count(&remaining).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(m).Where("user_id = 1 AND collection_id = 0").Count(&removed).Error; err != nil {
			t.Fatal(err)
		}
		if remaining != 3 || removed != 0 {
			t.Fatalf("%T remaining=%d default=%d", m, remaining, removed)
		}
	}
}
