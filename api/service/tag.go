package service

import (
	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
)

type TagService struct {
}

func (s *TagService) Info(id uint) *model.Tag {
	p := &model.Tag{}
	DB.Where("id = ?", id).First(p)
	return p
}
func (s *TagService) InfoByUserIdAndNameAndCollectionId(userid uint, name string, cid uint) *model.Tag {
	p := &model.Tag{}
	DB.Where("user_id = ? and name = ? and collection_id = ?", userid, name, cid).First(p)
	return p
}

func (s *TagService) ListByUserId(userId uint) (res *model.TagList) {
	res = s.List(1, 1000, func(tx *gorm.DB) {
		tx.Where("user_id = ?", userId)
	})
	return
}
func (s *TagService) ListByUserIdAndCollectionId(userId, cid uint) (res *model.TagList) {
	res = s.List(1, 1000, func(tx *gorm.DB) {
		tx.Where("user_id = ? and collection_id = ?", userId, cid)
		tx.Order("name asc")
	})
	return
}
func syncDefaultTags(tx *gorm.DB, userId uint, tags map[string]uint) error {
	var allTags []*model.Tag
	if err := tx.Where("user_id = ? AND collection_id = 0", userId).Find(&allTags).Error; err != nil {
		return err
	}
	seen := make(map[string]bool, len(allTags))
	for _, tag := range allTags {
		color, ok := tags[tag.Name]
		if !ok {
			if err := tx.Delete(tag).Error; err != nil {
				return err
			}
		} else {
			seen[tag.Name] = true
			if err := tx.Model(tag).Update("color", color).Error; err != nil {
				return err
			}
		}
	}
	for name, color := range tags {
		if !seen[name] {
			if err := tx.Create(&model.Tag{Name: name, Color: color, UserId: userId, CollectionId: 0}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// InfoById 根据用户id取用户信息
func (s *TagService) InfoById(id uint) *model.Tag {
	u := &model.Tag{}
	DB.Where("id = ?", id).First(u)
	return u
}

func (s *TagService) List(page, pageSize uint, where func(tx *gorm.DB)) (res *model.TagList) {
	res = &model.TagList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.Tag{})
	if where != nil {
		where(tx)
	}
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, pageSize))
	tx.Find(&res.Tags)
	return
}

// Create 创建
func (s *TagService) Create(u *model.Tag) error {
	res := DB.Create(u).Error
	return res
}
func (s *TagService) Delete(u *model.Tag) error {
	return DB.Delete(u).Error
}

// Update 更新
func (s *TagService) Update(u *model.Tag) error {
	return DB.Model(u).Select("*").Omit("created_at").Updates(u).Error
}
