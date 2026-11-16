package service

import (
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserService struct {
}

// InfoById 根据用户id取用户信息
func (us *UserService) InfoById(id uint) *model.User {
	u := &model.User{}
	DB.Where("id = ?", id).First(u)
	return u
}

// InfoByUsername 根据用户名取用户信息
func (us *UserService) InfoByUsername(un string) *model.User {
	u := &model.User{}
	DB.Where("username = ?", un).First(u)
	return u
}

// InfoByEmail 根据邮箱取用户信息
func (us *UserService) InfoByEmail(email string) *model.User {
	u := &model.User{}
	DB.Where("email = ?", email).First(u)
	return u
}

// InfoByOpenid 根据openid取用户信息
func (us *UserService) InfoByOpenid(openid string) *model.User {
	u := &model.User{}
	DB.Where("openid = ?", openid).First(u)
	return u
}

// InfoByUsernamePassword 根据用户名密码取用户信息
func (us *UserService) InfoByUsernamePassword(username, password string) *model.User {
	if Config.Ldap.Enable {
		u, err := AllService.LdapService.Authenticate(username, password)
		if err == nil {
			return u
		}
		Logger.Errorf("LDAP authentication failed, %v", err)
		return &model.User{}
	}
	u := &model.User{}
	DB.Where("username = ?", username).First(u)
	if u.Id == 0 || u.LdapDN != "" {
		return &model.User{}
	}
	ok, err := utils.VerifyPassword(u.Password, password)
	if err != nil || !ok {
		return &model.User{}
	}
	return u
}

// InfoByAccesstoken 根据accesstoken取用户信息
func (us *UserService) InfoByAccessToken(token string) (*model.User, *model.UserToken) {
	u := &model.User{}
	ut := &model.UserToken{}
	DB.Where("token = ?", token).First(ut)
	if ut.Id == 0 {
		return u, ut
	}
	if ut.ExpiredAt <= time.Now().Unix() {
		return u, ut
	}
	DB.Where("id = ?", ut.UserId).First(u)
	return u, ut
}

// Login atomically persists a fixed-lifetime session and its audit record.
func (us *UserService) Login(u *model.User, llog *model.LoginLog) (*model.UserToken, error) {
	if !us.CheckUserEnable(u) || llog == nil {
		return nil, errors.New("UserDisabled")
	}
	token, expires, err := Jwt.GenerateToken(u.Id)
	if err != nil {
		return nil, err
	}
	ut := &model.UserToken{UserId: u.Id, Token: token, DeviceUuid: llog.Uuid, DeviceId: llog.DeviceId, ExpiredAt: expires}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(ut).Error; err != nil {
			return err
		}
		llog.UserId = u.Id
		llog.UserTokenId = ut.Id
		if err := tx.Create(llog).Error; err != nil {
			return err
		}
		if llog.Uuid != "" && llog.DeviceId != "" {
			// A login must never reassign another user's device.
			return tx.Model(&model.Peer{}).Where("id = ? AND uuid = ? AND (user_id = 0 OR user_id = ?)", llog.DeviceId, llog.Uuid, u.Id).Update("user_id", u.Id).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ut, nil
}

// CurUser 获取当前用户
func (us *UserService) CurUser(c *gin.Context) *model.User {
	user, _ := c.Get("curUser")
	u, ok := user.(*model.User)
	if !ok {
		return nil
	}
	return u
}

func (us *UserService) List(page, pageSize uint, where func(tx *gorm.DB)) (res *model.UserList) {
	res = &model.UserList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.User{})
	if where != nil {
		where(tx)
	}
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, pageSize))
	tx.Find(&res.Users)
	return
}

func (us *UserService) ListByIds(ids []uint) (res []*model.User) {
	DB.Where("id in ?", ids).Find(&res)
	return res
}

// ListByGroupId 根据组id取用户列表
func (us *UserService) ListByGroupId(groupId, page, pageSize uint) (res *model.UserList) {
	res = us.List(page, pageSize, func(tx *gorm.DB) {
		tx.Where("group_id = ?", groupId)
	})
	return
}

// ListIdsByGroupId 根据组id取用户id列表
func (us *UserService) ListIdsByGroupId(groupId uint) (ids []uint) {
	DB.Model(&model.User{}).Where("group_id = ?", groupId).Pluck("id", &ids)
	return ids

}

// ListIdAndNameByGroupId 根据组id取用户id和用户名列表
func (us *UserService) ListIdAndNameByGroupId(groupId uint) (res []*model.User) {
	DB.Model(&model.User{}).Where("group_id = ?", groupId).Select("id, username").Find(&res)
	return res
}

// CheckUserEnable 判断用户是否禁用
func (us *UserService) CheckUserEnable(u *model.User) bool {
	return u != nil && u.Id != 0 && u.Status == model.COMMON_STATUS_ENABLE
}

// Create 创建
func (us *UserService) Create(u *model.User) error {
	// The initial username should be formatted, and the username should be unique
	if us.IsUsernameExists(u.Username) {
		return errors.New("UsernameExists")
	}
	u.Username = us.formatUsername(u.Username)
	var err error
	u.Password, err = utils.EncryptPassword(u.Password)
	if err != nil {
		return err
	}
	res := DB.Create(u).Error
	return res
}

// GetUuidByToken 根据token和user取uuid
func (us *UserService) GetUuidByToken(u *model.User, token string) string {
	ut := &model.UserToken{}
	err := DB.Where("user_id = ? and token = ?", u.Id, token).First(ut).Error
	if err != nil {
		return ""
	}
	return ut.DeviceUuid
}

// Logout 退出登录 -> 删除token, 解绑uuid
func (us *UserService) Logout(u *model.User, token string) error {
	uuid := us.GetUuidByToken(u, token)
	err := DB.Where("user_id = ? and token = ?", u.Id, token).Delete(&model.UserToken{}).Error
	if err != nil {
		return err
	}
	if uuid != "" {
		AllService.PeerService.UuidUnbindUserId(uuid, u.Id)
	}
	return nil
}

// Delete 删除用户和oauth信息
func (us *UserService) Delete(u *model.User) error {
	if u == nil || u.Id == 0 {
		return errors.New("ItemNotFound")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		admins, err := lockEnabledAdmins(tx)
		if err != nil {
			return err
		}
		var current model.User
		if err := tx.First(&current, u.Id).Error; err != nil {
			return err
		}
		if current.Status == model.COMMON_STATUS_ENABLE && us.IsAdmin(&current) && len(admins) <= 1 {
			return errors.New("The last enabled admin cannot be deleted")
		}
		for _, row := range []interface{}{&model.UserToken{}, &model.UserThird{}, &model.AddressBook{}, &model.Tag{}, &model.AddressBookCollectionRule{}, &model.AddressBookCollection{}, &model.ShareRecord{}} {
			if err := tx.Where("user_id = ?", u.Id).Delete(row).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("type = ? AND to_id = ?", model.ShareAddressBookRuleTypePersonal, u.Id).Delete(&model.AddressBookCollectionRule{}).Error; err != nil {
			return err
		}
		// Retain audit events, but remove account/device/IP/session identifiers.
		if err := tx.Model(&model.LoginLog{}).Where("user_id = ?", u.Id).Updates(map[string]interface{}{"user_id": 0, "user_token_id": 0, "device_id": "", "uuid": "", "ip": "", "is_deleted": model.IsDeletedYes}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Peer{}).Where("user_id = ?", u.Id).Update("user_id", 0).Error; err != nil {
			return err
		}
		return tx.Delete(&current).Error
	})
}

// Update 更新
func lockEnabledAdmins(tx *gorm.DB) ([]model.User, error) {
	var admins []model.User
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("is_admin = ? AND status = ?", true, model.COMMON_STATUS_ENABLE).Order("id").Find(&admins).Error
	return admins, err
}

func (us *UserService) Update(u *model.User) error {
	return us.updateUser(u, false)
}

func (us *UserService) UpdateAdmin(u *model.User) error {
	return us.updateUser(u, true)
}

func (us *UserService) updateUser(u *model.User, full bool) error {
	if u == nil || u.Id == 0 {
		return errors.New("ItemNotFound")
	}
	if u.Status != model.COMMON_STATUS_ENABLE && u.Status != model.COMMON_STATUS_DISABLED {
		return errors.New("invalid user status")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		admins, err := lockEnabledAdmins(tx)
		if err != nil {
			return err
		}
		var current model.User
		if err := tx.First(&current, u.Id).Error; err != nil {
			return err
		}
		demoted := u.IsAdmin != nil && !*u.IsAdmin
		if current.Status == model.COMMON_STATUS_ENABLE && us.IsAdmin(&current) && len(admins) <= 1 && (demoted || u.Status != model.COMMON_STATUS_ENABLE) {
			return errors.New("The last enabled admin cannot be disabled or demoted")
		}
		query := tx.Model(&model.User{}).Where("id = ?", u.Id)
		if full {
			fields := []string{"username", "email", "nickname", "group_id", "status", "remark"}
			if u.IsAdmin != nil {
				fields = append(fields, "is_admin")
			}
			query = query.Select(fields)
		}
		if err := query.Updates(u).Error; err != nil {
			return err
		}
		if u.Status == model.COMMON_STATUS_DISABLED {
			return tx.Where("user_id = ?", u.Id).Delete(&model.UserToken{}).Error
		}
		return nil
	})
}

// FlushToken 清空token
func (us *UserService) FlushToken(u *model.User) error {
	return DB.Where("user_id = ?", u.Id).Delete(&model.UserToken{}).Error
}

// FlushTokenByUuid 清空token
func (us *UserService) FlushTokenByUuid(uuid string) error {
	return DB.Where("device_uuid = ?", uuid).Delete(&model.UserToken{}).Error
}

// FlushTokenByUuids 清空token
func (us *UserService) FlushTokenByUuids(uuids []string) error {
	return DB.Where("device_uuid in (?)", uuids).Delete(&model.UserToken{}).Error
}

// UpdatePassword 更新密码
func (us *UserService) UpdatePassword(u *model.User, password string) error {
	var err error
	u.Password, err = utils.EncryptPassword(password)
	if err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(u).Update("password", u.Password).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", u.Id).Delete(&model.UserToken{}).Error
	})
}

// IsAdmin 是否管理员
func (us *UserService) IsAdmin(u *model.User) bool {
	return u != nil && u.IsAdmin != nil && *u.IsAdmin
}

// RouteNames
func (us *UserService) RouteNames(u *model.User) []string {
	if us.IsAdmin(u) {
		return model.AdminRouteNames
	}
	return model.UserRouteNames
}

// InfoByOauthId 根据oauth的name和openId取用户信息
func (us *UserService) InfoByOauthId(op string, openId string) *model.User {
	ut := AllService.OauthService.UserThirdInfo(op, openId)
	if ut.Id == 0 {
		return nil
	}
	u := us.InfoById(ut.UserId)
	if u.Id == 0 {
		return nil
	}
	return u
}

// RegisterByOauth 注册
func (us *UserService) RegisterByOauth(oauthUser *model.OauthUser, op string) (error, *model.User) {
	if oauthUser == nil || oauthUser.OpenId == "" {
		return errors.New("invalid OAuth identity"), nil
	}
	Lock.Lock("registerByOauth")
	defer Lock.UnLock("registerByOauth")
	ut := AllService.OauthService.UserThirdInfo(op, oauthUser.OpenId)
	if ut.Id != 0 {
		return nil, us.InfoById(ut.UserId)
	}
	err, oauthType := AllService.OauthService.GetTypeByOp(op)
	if err != nil {
		return err, nil
	}
	// Matching email is not proof of identity. Existing accounts must bind explicitly.
	if oauthUser.Email != "" && us.InfoByEmail(strings.ToLower(oauthUser.Email)).Id != 0 {
		return errors.New("OauthBindRequired"), nil
	}
	ut = &model.UserThird{}
	ut.FromOauthUser(0, oauthUser, oauthType, op)
	// The initial username should be formatted
	username := us.formatUsername(oauthUser.Username)
	usernameUnique := us.GenerateUsernameByOauth(username)
	user := &model.User{
		Username: usernameUnique,
		GroupId:  1,
	}
	oauthUser.ToUser(user, false)
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		ut.UserId = user.Id
		return tx.Create(ut).Error
	})
	if err != nil {
		return err, nil
	}
	return nil, user
}

// GenerateUsernameByOauth 生成用户名
func (us *UserService) GenerateUsernameByOauth(name string) string {
	for us.IsUsernameExists(name) {
		name += strconv.Itoa(rand.Intn(10)) // Append a random digit (0-9)
	}
	return name
}

// UserThirdsByUserId
func (us *UserService) UserThirdsByUserId(userId uint) (res []*model.UserThird) {
	DB.Where("user_id = ?", userId).Find(&res)
	return res
}

func (us *UserService) UserThirdInfo(userId uint, op string) *model.UserThird {
	ut := &model.UserThird{}
	DB.Where("user_id = ? and op = ?", userId, op).First(ut)
	return ut
}

// FindLatestUserIdFromLoginLogByUuid 根据uuid和设备id查找最后登录的用户id
func (us *UserService) FindLatestUserIdFromLoginLogByUuid(uuid string, deviceId string) uint {
	llog := &model.LoginLog{}
	DB.Where("uuid = ? and device_id = ?", uuid, deviceId).Order("id desc").First(llog)
	return llog.UserId
}

// IsPasswordEmptyById 根据用户id判断密码是否为空，主要用于第三方登录的自动注册
func (us *UserService) IsPasswordEmptyById(id uint) bool {
	u := &model.User{}
	if DB.Where("id = ?", id).First(u).Error != nil {
		return false
	}
	return u.Password == ""
}

// IsPasswordEmptyByUsername 根据用户id判断密码是否为空，主要用于第三方登录的自动注册
func (us *UserService) IsPasswordEmptyByUsername(username string) bool {
	u := &model.User{}
	if DB.Where("username = ?", username).First(u).Error != nil {
		return false
	}
	return u.Password == ""
}

// IsPasswordEmptyByUser 判断密码是否为空，主要用于第三方登录的自动注册
func (us *UserService) IsPasswordEmptyByUser(u *model.User) bool {
	return us.IsPasswordEmptyById(u.Id)
}

// Register 注册, 如果用户名已存在则返回nil
func (us *UserService) Register(username string, email string, password string, status model.StatusCode) *model.User {
	u := &model.User{
		Username: username,
		Email:    email,
		Password: password,
		GroupId:  1,
		Status:   status,
	}
	err := us.Create(u)
	if err != nil {
		return nil
	}
	return u
}

func (us *UserService) TokenList(page uint, size uint, f func(tx *gorm.DB)) *model.UserTokenList {
	res := &model.UserTokenList{}
	res.Page = int64(page)
	res.PageSize = int64(size)
	tx := DB.Model(&model.UserToken{})
	if f != nil {
		f(tx)
	}
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, size))
	tx.Find(&res.UserTokens)
	return res
}

func (us *UserService) TokenInfoById(id uint) *model.UserToken {
	ut := &model.UserToken{}
	DB.Where("id = ?", id).First(ut)
	return ut
}

func (us *UserService) DeleteToken(l *model.UserToken) error {
	return DB.Delete(l).Error
}

// Helper functions, used for formatting username
func (us *UserService) formatUsername(username string) string {
	username = strings.ReplaceAll(username, " ", "")
	username = strings.ToLower(username)
	return username
}

// Helper functions, getUserCount
func (us *UserService) getUserCount() int64 {
	var count int64
	DB.Model(&model.User{}).Count(&count)
	return count
}

// helper functions, getAdminUserCount
func (us *UserService) getAdminUserCount() int64 {
	var count int64
	DB.Model(&model.User{}).Where("is_admin = ?", true).Count(&count)
	return count
}

func (us *UserService) BatchDeleteUserToken(ids []uint) error {
	return DB.Where("id in ?", ids).Delete(&model.UserToken{}).Error
}

func (us *UserService) VerifyJWT(token string) (uint, error) {
	return Jwt.ParseToken(token)
}

// IsUsernameExists 判断用户名是否存在, it will check the internal database and LDAP(if enabled)
func (us *UserService) IsUsernameExists(username string) bool {
	return us.IsUsernameExistsLocal(username) || AllService.LdapService.IsUsernameExists(username)
}

func (us *UserService) IsUsernameExistsLocal(username string) bool {
	u := &model.User{}
	DB.Where("username = ?", username).First(u)
	return u.Id != 0
}

func (us *UserService) IsEmailExistsLdap(email string) bool {
	return AllService.LdapService.IsEmailExists(email)
}
