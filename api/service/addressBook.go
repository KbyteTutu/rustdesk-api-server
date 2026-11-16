package service

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
	"strings"
)

type AddressBookService struct {
}

func (s *AddressBookService) Info(id string) *model.AddressBook {
	p := &model.AddressBook{}
	DB.Where("id = ?", id).First(p)
	return p
}

func (s *AddressBookService) InfoByUserIdAndId(userid uint, id string) *model.AddressBook {
	p := &model.AddressBook{}
	DB.Where("user_id = ? and id = ?", userid, id).First(p)
	return p
}

func (s *AddressBookService) InfoByUserIdAndIdAndCid(userid uint, id string, cid uint) *model.AddressBook {
	p := &model.AddressBook{}
	DB.Where("user_id = ? and id = ? and collection_id = ?", userid, id, cid).First(p)
	return p
}
func (s *AddressBookService) InfoByRowId(id uint) *model.AddressBook {
	p := &model.AddressBook{}
	DB.Where("row_id = ?", id).First(p)
	return p
}
func (s *AddressBookService) ListByUserId(userId, page, pageSize uint) (res *model.AddressBookList) {
	res = s.List(page, pageSize, func(tx *gorm.DB) {
		tx.Where("user_id = ?", userId)
	})
	return
}
func (s *AddressBookService) ListByUserIds(userIds []uint, page, pageSize uint) (res *model.AddressBookList) {
	res = s.List(page, pageSize, func(tx *gorm.DB) {
		tx.Where("user_id in (?)", userIds)
	})
	return
}

// AddAddressBook
func (s *AddressBookService) AddAddressBook(ab *model.AddressBook) error {
	return DB.Create(ab).Error
}

// SyncDefaultCollection replaces only this user's default collection, atomically.
func (s *AddressBookService) SyncDefaultCollection(abs []*model.AddressBook, tags map[string]uint, userId uint) error {
	if userId == 0 || len(abs) > 1000 || len(tags) > 1000 {
		return errors.New("invalid address book")
	}
	seen := make(map[string]bool, len(abs))
	for _, ab := range abs {
		if ab == nil || ab.Id == "" || seen[ab.Id] {
			return errors.New("invalid or duplicate peer")
		}
		seen[ab.Id] = true
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var existing []*model.AddressBook
		if err := tx.Where("user_id = ? AND collection_id = 0", userId).Find(&existing).Error; err != nil {
			return err
		}
		byID := make(map[string]*model.AddressBook, len(existing))
		for _, ab := range existing {
			byID[ab.Id] = ab
		}
		for _, input := range abs {
			ab := *input
			ab.RowId, ab.UserId, ab.CollectionId = 0, userId, 0
			ab.Collection = nil
			ab.TimeModel = model.TimeModel{}
			if old := byID[ab.Id]; old != nil {
				ab.RowId = old.RowId
				if err := tx.Model(&model.AddressBook{}).Where("row_id = ? AND user_id = ? AND collection_id = 0", old.RowId, userId).Select("*").Omit("created_at", "Collection").Updates(&ab).Error; err != nil {
					return err
				}
			} else if err := tx.Omit("Collection").Create(&ab).Error; err != nil {
				return err
			}
		}
		for _, old := range existing {
			if !seen[old.Id] {
				if err := tx.Where("user_id = ? AND collection_id = 0", userId).Delete(old).Error; err != nil {
					return err
				}
			}
		}
		return syncDefaultTags(tx, userId, tags)
	})
}

func (s *AddressBookService) List(page, pageSize uint, where func(tx *gorm.DB)) (res *model.AddressBookList) {
	res = &model.AddressBookList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.AddressBook{})
	if where != nil {
		where(tx)
	}
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, pageSize))
	tx.Find(&res.AddressBooks)
	return
}

func (s *AddressBookService) FromPeer(peer *model.Peer) (a *model.AddressBook) {
	a = &model.AddressBook{}
	a.Id = peer.Id
	a.Username = peer.Username
	a.Hostname = peer.Hostname
	a.UserId = peer.UserId
	a.Platform = s.PlatformFromOs(peer.Os)
	return a
}

// Create 创建
func (s *AddressBookService) Create(u *model.AddressBook) error {
	res := DB.Create(u).Error
	return res
}
func (s *AddressBookService) Delete(u *model.AddressBook) error {
	return DB.Delete(u).Error
}

// Update 更新
func (s *AddressBookService) Update(u *model.AddressBook) error {
	return DB.Model(u).Updates(u).Error
}

// UpdateByMap 更新
func (s *AddressBookService) UpdateByMap(u *model.AddressBook, data map[string]interface{}) error {
	return DB.Model(u).Updates(data).Error
}

// UpdateAll 更新
func (s *AddressBookService) UpdateAll(u *model.AddressBook) error {
	return DB.Model(u).Select("*").Omit("created_at").Updates(u).Error
}

// ShareByWebClient 分享
func (s *AddressBookService) ShareByWebClient(m *model.ShareRecord) error {
	m.ShareToken = uuid.New().String()
	return DB.Create(m).Error
}

// SharedPeer
func (s *AddressBookService) SharedPeer(shareToken string) *model.ShareRecord {
	m := &model.ShareRecord{}
	DB.Where("share_token = ?", shareToken).First(m)
	return m
}

// PlatformFromOs
func (s *AddressBookService) PlatformFromOs(os string) string {
	if strings.Contains(os, "Android") || strings.Contains(os, "android") {
		return "Android"
	}
	if strings.Contains(os, "Windows") || strings.Contains(os, "windows") {
		return "Windows"
	}
	if strings.Contains(os, "Linux") || strings.Contains(os, "linux") {
		return "Linux"
	}
	if strings.Contains(os, "mac") || strings.Contains(os, "Mac") {
		return "Mac OS"
	}
	return ""
}
func (s *AddressBookService) ListByUserIdAndCollectionId(userId, cid, page, pageSize uint) (res *model.AddressBookList) {
	res = s.List(page, pageSize, func(tx *gorm.DB) {
		tx.Where("user_id = ? and collection_id = ?", userId, cid)
	})
	return
}
func (s *AddressBookService) ListCollection(page, pageSize uint, where func(tx *gorm.DB)) (res *model.AddressBookCollectionList) {
	res = &model.AddressBookCollectionList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.AddressBookCollection{})
	if where != nil {
		where(tx)
	}
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, pageSize))
	tx.Find(&res.AddressBookCollection)
	return
}
func (s *AddressBookService) ListCollectionByIds(ids []uint) (res []*model.AddressBookCollection) {
	DB.Where("id in ?", ids).Find(&res)
	return res
}

func (s *AddressBookService) ListCollectionByUserId(userId uint) (res *model.AddressBookCollectionList) {
	res = s.ListCollection(1, 100, func(tx *gorm.DB) {
		tx.Where("user_id = ?", userId)
	})
	return
}
func (s *AddressBookService) CollectionInfoById(id uint) *model.AddressBookCollection {
	p := &model.AddressBookCollection{}
	DB.Where("id = ?", id).First(p)
	return p
}

func (s *AddressBookService) CollectionReadRules(user *model.User) (res []*model.AddressBookCollectionRule) {
	// personalRules
	var personalRules []*model.AddressBookCollectionRule
	tx2 := DB.Model(&model.AddressBookCollectionRule{})
	tx2.Where("type = ? and to_id = ? and rule > 0", model.ShareAddressBookRuleTypePersonal, user.Id).Find(&personalRules)
	res = append(res, personalRules...)

	//group
	var groupRules []*model.AddressBookCollectionRule
	tx3 := DB.Model(&model.AddressBookCollectionRule{})
	tx3.Where("type = ? and to_id = ? and rule > 0", model.ShareAddressBookRuleTypeGroup, user.GroupId).Find(&groupRules)
	res = append(res, groupRules...)
	return
}

func (s *AddressBookService) UserMaxRule(user *model.User, uid, cid uint) int {
	// ismy?
	if user.Id == uid {
		return model.ShareAddressBookRuleRuleFullControl
	}
	max := 0
	personalRules := &model.AddressBookCollectionRule{}
	tx := DB.Model(personalRules)
	tx.Where("type = ? and collection_id = ? and to_id = ?", model.ShareAddressBookRuleTypePersonal, cid, user.Id).First(&personalRules)
	if personalRules.Id != 0 {
		max = personalRules.Rule
		if max == model.ShareAddressBookRuleRuleFullControl {
			return max
		}
	}

	groupRules := &model.AddressBookCollectionRule{}
	tx2 := DB.Model(groupRules)
	tx2.Where("type = ? and collection_id = ? and to_id = ?", model.ShareAddressBookRuleTypeGroup, cid, user.GroupId).First(&groupRules)
	if groupRules.Id != 0 {
		if groupRules.Rule > max {
			max = groupRules.Rule
		}
		if max == model.ShareAddressBookRuleRuleFullControl {
			return max
		}
	}
	return max
}

func (s *AddressBookService) CheckUserReadPrivilege(user *model.User, uid, cid uint) bool {
	return s.UserMaxRule(user, uid, cid) >= model.ShareAddressBookRuleRuleRead
}
func (s *AddressBookService) CheckUserWritePrivilege(user *model.User, uid, cid uint) bool {
	return s.UserMaxRule(user, uid, cid) >= model.ShareAddressBookRuleRuleReadWrite
}
func (s *AddressBookService) CheckUserFullControlPrivilege(user *model.User, uid, cid uint) bool {
	return s.UserMaxRule(user, uid, cid) >= model.ShareAddressBookRuleRuleFullControl
}

func (s *AddressBookService) CreateCollection(t *model.AddressBookCollection) error {
	return DB.Create(t).Error
}

func (s *AddressBookService) UpdateCollection(t *model.AddressBookCollection) error {
	return DB.Model(&model.AddressBookCollection{}).Where("id = ? AND user_id = ?", t.Id, t.UserId).Update("name", t.Name).Error
}

func (s *AddressBookService) DeleteCollection(t *model.AddressBookCollection) error {
	//删除集合下的所有规则、地址簿，再删除集合
	tx := DB.Begin()
	tx.Where("collection_id = ?", t.Id).Delete(&model.AddressBookCollectionRule{})
	tx.Where("collection_id = ?", t.Id).Delete(&model.AddressBook{})
	tx.Delete(t)
	return tx.Commit().Error
}

func (s *AddressBookService) RuleInfoById(u uint) *model.AddressBookCollectionRule {
	p := &model.AddressBookCollectionRule{}
	DB.Where("id = ?", u).First(p)
	return p
}
func (s *AddressBookService) RulePersonalInfoByToIdAndCid(toid, cid uint) *model.AddressBookCollectionRule {
	return s.RuleInfoByToIdAndCid(model.ShareAddressBookRuleTypePersonal, toid, cid)
}
func (s *AddressBookService) RuleInfoByToIdAndCid(t int, toid, cid uint) *model.AddressBookCollectionRule {
	p := &model.AddressBookCollectionRule{}
	DB.Where("type = ? and to_id = ? and collection_id = ?", t, toid, cid).First(p)
	return p
}
func (s *AddressBookService) CreateRule(t *model.AddressBookCollectionRule) error {
	return DB.Create(t).Error
}

func (s *AddressBookService) ListRules(page uint, size uint, f func(tx *gorm.DB)) *model.AddressBookCollectionRuleList {
	res := &model.AddressBookCollectionRuleList{}
	res.Page = int64(page)
	res.PageSize = int64(size)
	tx := DB.Model(&model.AddressBookCollectionRule{})
	if f != nil {
		f(tx)
	}
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, size))
	tx.Find(&res.AddressBookCollectionRule)
	return res
}

func (s *AddressBookService) UpdateRule(t *model.AddressBookCollectionRule) error {
	return DB.Model(t).Updates(t).Error
}

func (s *AddressBookService) DeleteRule(t *model.AddressBookCollectionRule) error {
	return DB.Delete(t).Error
}

// CheckCollectionOwner 检查Collection的所有者
func (s *AddressBookService) CheckCollectionOwner(uid uint, cid uint) bool {
	p := s.CollectionInfoById(cid)
	return p.UserId == uid
}

func (s *AddressBookService) BatchUpdateTags(abs []*model.AddressBook, tags []string) error {
	ids := make([]uint, 0)
	for _, ab := range abs {
		ids = append(ids, ab.RowId)
	}
	tagsv, _ := json.Marshal(tags)
	return DB.Model(&model.AddressBook{}).Where("row_id in ?", ids).Update("tags", tagsv).Error
}
