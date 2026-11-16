package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	// "golang.org/x/oauth2/google"
	"gorm.io/gorm"
	// "io"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type OauthService struct {
}

// Define a struct to parse the .well-known/openid-configuration response
type OidcEndpoint struct {
	Issuer   string `json:"issuer"`
	AuthURL  string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
	UserInfo string `json:"userinfo_endpoint"`
}

type OauthCacheItem struct {
	UserId     uint   `json:"user_id"`
	Id         string `json:"id"` //rustdesk的设备ID
	Op         string `json:"op"`
	Action     string `json:"action"`
	Uuid       string `json:"uuid"`
	DeviceName string `json:"device_name"`
	DeviceOs   string `json:"device_os"`
	DeviceType string `json:"device_type"`
	OpenId     string `json:"open_id"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Verifier   string `json:"-"`
	Nonce      string `json:"-"`
	phase      string
	expiresAt  time.Time
}

func (oci *OauthCacheItem) ToOauthUser() *model.OauthUser {
	return &model.OauthUser{
		OpenId:   oci.OpenId,
		Username: oci.Username,
		Name:     oci.Name,
		Email:    oci.Email,
	}
}

var oauthMu sync.Mutex
var oauthCache = make(map[string]*OauthCacheItem)

const (
	OauthActionTypeLogin = "login"
	OauthActionTypeBind  = "bind"
)

func (oci *OauthCacheItem) UpdateFromOauthUser(oauthUser *model.OauthUser) {
	oci.OpenId = oauthUser.OpenId
	oci.Username = oauthUser.Username
	oci.Name = oauthUser.Name
	oci.Email = oauthUser.Email
}

func liveOauth(key string) *OauthCacheItem {
	v := oauthCache[key]
	if v != nil && !time.Now().Before(v.expiresAt) {
		delete(oauthCache, key)
		return nil
	}
	return v
}

func (os *OauthService) GetOauthCache(key string) *OauthCacheItem {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	v := liveOauth(key)
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

// SetOauthCache creates a flow. Later changes must use explicit transitions.
func (os *OauthService) SetOauthCache(key string, item *OauthCacheItem, expire uint) error {
	if key == "" || item == nil || expire == 0 || expire > 300 {
		return errors.New("invalid OAuth flow")
	}
	oauthMu.Lock()
	defer oauthMu.Unlock()
	if liveOauth(key) != nil || len(oauthCache) >= 10000 {
		return errors.New("OAuth capacity exceeded")
	}
	copy := *item
	copy.phase = "pending"
	copy.expiresAt = time.Now().Add(time.Duration(expire) * time.Second)
	oauthCache[key] = &copy
	time.AfterFunc(time.Duration(expire)*time.Second, func() {
		oauthMu.Lock()
		defer oauthMu.Unlock()
		if oauthCache[key] == &copy {
			delete(oauthCache, key)
		}
	})
	return nil
}

func (os *OauthService) DeleteOauthCache(key string) {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	delete(oauthCache, key)
}

func (os *OauthService) ConsumeLogin(key string) *OauthCacheItem {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	v := liveOauth(key)
	if v == nil || v.Action != OauthActionTypeLogin || v.phase != "authorized" || v.UserId == 0 {
		return nil
	}
	delete(oauthCache, key)
	copy := *v
	return &copy
}

func (os *OauthService) AuthorizeWebLogin(key string, userId uint) bool {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	v := liveOauth(key)
	if v == nil || userId == 0 || v.Action != OauthActionTypeLogin || v.Op != model.OauthTypeWebauth || v.phase != "pending" {
		return false
	}
	v.UserId, v.phase = userId, "authorized"
	return true
}

func (os *OauthService) StartCallback(key string) *OauthCacheItem {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	v := liveOauth(key)
	if v == nil || v.Op == model.OauthTypeWebauth || v.phase != "pending" {
		return nil
	}
	v.phase = "verifying"
	copy := *v
	return &copy
}

func (os *OauthService) CompleteCallback(key string, identity *model.OauthUser, userId uint) bool {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	v := liveOauth(key)
	if v == nil || v.phase != "verifying" || v.Action != OauthActionTypeLogin || identity == nil || identity.OpenId == "" {
		return false
	}
	v.UpdateFromOauthUser(identity)
	v.UserId, v.phase = userId, "verified"
	if userId != 0 {
		v.phase = "authorized"
	}
	return true
}

// A verified login can be explicitly bound by an authenticated local account.
func (os *OauthService) BindVerifiedLogin(key string, userId uint) error {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	v := liveOauth(key)
	if v == nil || v.Action != OauthActionTypeLogin || v.phase != "verified" || v.UserId != 0 || v.OpenId == "" {
		return errors.New("invalid OAuth flow")
	}
	if err := os.BindOauthUser(userId, v.ToOauthUser(), v.Op); err != nil {
		return err
	}
	v.UserId, v.phase = userId, "authorized"
	return nil
}

func (os *OauthService) BeginAuth(op string) (error error, state, verifier, nonce, url string) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err, "", "", "", ""
	}
	state = hex.EncodeToString(random[:])
	verifier = ""
	nonce = ""
	if op == model.OauthTypeWebauth {
		url = Config.Rustdesk.ApiServer + "/_admin/#/oauth/" + state
		//url = "http://localhost:8888/_admin/#/oauth/" + code
		return nil, state, verifier, nonce, url
	}
	err, oauthInfo, oauthConfig, _ := os.GetOauthConfig(op)
	if err == nil {
		extras := make([]oauth2.AuthCodeOption, 0, 3)

		if _, err := rand.Read(random[:]); err != nil {
			return err, "", "", "", ""
		}
		nonce = hex.EncodeToString(random[:])
		extras = append(extras, oauth2.SetAuthURLParam("nonce", nonce))

		if oauthInfo.PkceEnable != nil && *oauthInfo.PkceEnable {
			extras = append(extras, oauth2.AccessTypeOffline)
			verifier = oauth2.GenerateVerifier()
			switch oauthInfo.PkceMethod {
			case model.PKCEMethodS256:
				extras = append(extras, oauth2.S256ChallengeOption(verifier))
			case model.PKCEMethodPlain:
				// oauth2 does not have a plain challenge option, so we add it manually
				extras = append(extras, oauth2.SetAuthURLParam("code_challenge_method", "plain"), oauth2.SetAuthURLParam("code_challenge", verifier))
			}
		}

		return err, state, verifier, nonce, oauthConfig.AuthCodeURL(state, extras...)
	}

	return err, state, verifier, nonce, ""
}

func (os *OauthService) FetchOidcProvider(issuer string) (error, *oidc.Provider) {

	// Get the HTTP client (with or without proxy based on configuration)
	client := getHTTPClientWithProxy()

	ctx := oidc.ClientContext(context.Background(), client)

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return err, nil
	}

	return nil, provider
}

func (os *OauthService) GithubProvider() *oidc.Provider {
	return (&oidc.ProviderConfig{
		IssuerURL:     "",
		AuthURL:       github.Endpoint.AuthURL,
		TokenURL:      github.Endpoint.TokenURL,
		DeviceAuthURL: github.Endpoint.DeviceAuthURL,
		UserInfoURL:   model.UserEndpointGithub,
		JWKSURL:       "",
		Algorithms:    nil,
	}).NewProvider(context.Background())
}

func (os *OauthService) LinuxdoProvider() *oidc.Provider {
	return (&oidc.ProviderConfig{
		IssuerURL:     "",
		AuthURL:       "https://connect.linux.do/oauth2/authorize",
		TokenURL:      "https://connect.linux.do/oauth2/token",
		DeviceAuthURL: "",
		UserInfoURL:   model.UserEndpointLinuxdo,
		JWKSURL:       "",
		Algorithms:    nil,
	}).NewProvider(context.Background())
}

// GetOauthConfig retrieves the OAuth2 configuration based on the provider name
func (os *OauthService) GetOauthConfig(op string) (err error, oauthInfo *model.Oauth, oauthConfig *oauth2.Config, provider *oidc.Provider) {
	//err, oauthInfo, oauthConfig = os.getOauthConfigGeneral(op)
	oauthInfo = os.InfoByOp(op)
	if oauthInfo.Id == 0 || oauthInfo.ClientId == "" || oauthInfo.ClientSecret == "" {
		return errors.New("ConfigNotFound"), nil, nil, nil
	}
	oauthConfig = &oauth2.Config{
		ClientID:     oauthInfo.ClientId,
		ClientSecret: oauthInfo.ClientSecret,
		RedirectURL:  Config.Rustdesk.ApiServer + "/api/oidc/callback",
	}

	// Maybe should validate the oauthConfig here
	oauthType := oauthInfo.OauthType
	err = model.ValidateOauthType(oauthType)
	if err != nil {
		return err, nil, nil, nil
	}
	switch oauthType {
	case model.OauthTypeGithub:
		oauthConfig.Endpoint = github.Endpoint
		oauthConfig.Scopes = []string{"read:user", "user:email"}
		provider = os.GithubProvider()
	case model.OauthTypeLinuxdo:
		provider = os.LinuxdoProvider()
		oauthConfig.Endpoint = provider.Endpoint()
		oauthConfig.Scopes = []string{"profile"}
	//case model.OauthTypeGoogle: //google单独出来，可以少一次FetchOidcEndpoint请求
	//	oauthConfig.Endpoint = google.Endpoint
	//	oauthConfig.Scopes = os.constructScopes(oauthInfo.Scopes)
	case model.OauthTypeOidc, model.OauthTypeGoogle:
		err, provider = os.FetchOidcProvider(oauthInfo.Issuer)
		if err != nil {
			return err, nil, nil, nil
		}
		oauthConfig.Endpoint = provider.Endpoint()
		oauthConfig.Scopes = os.constructScopes(oauthInfo.Scopes)
	default:
		return errors.New("unsupported OAuth type"), nil, nil, nil
	}
	return nil, oauthInfo, oauthConfig, provider
}

func getHTTPClientWithProxy() *http.Client {
	//add timeout 30s
	timeout := time.Duration(60) * time.Second
	if Config.Proxy.Enable {
		if Config.Proxy.Host == "" {
			Logger.Warn("Proxy is enabled but proxy host is empty.")
			return http.DefaultClient
		}
		proxyURL, err := url.Parse(Config.Proxy.Host)
		if err != nil {
			Logger.Warn("Invalid proxy URL: ", err)
			return http.DefaultClient
		}
		transport := &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
		return &http.Client{Transport: transport, Timeout: timeout}
	}
	return http.DefaultClient
}
func (os *OauthService) callbackBase(oauthConfig *oauth2.Config, provider *oidc.Provider, code string, verifier string, nonce string, userData interface{}) (err error, client *http.Client) {

	// 设置代理客户端
	httpClient := getHTTPClientWithProxy()
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)

	exchangeOpts := make([]oauth2.AuthCodeOption, 0, 1)
	if verifier != "" {
		exchangeOpts = append(exchangeOpts, oauth2.VerifierOption(verifier))
	}

	token, err := oauthConfig.Exchange(ctx, code, exchangeOpts...)

	if err != nil {
		Logger.Warn("oauthConfig.Exchange() failed: ", err)
		return errors.New("GetOauthTokenError"), nil
	}

	// 获取 ID Token， github没有id_token
	rawIDToken, ok := token.Extra("id_token").(string)
	if ok && rawIDToken != "" {
		// 验证 ID Token
		v := provider.Verifier(&oidc.Config{ClientID: oauthConfig.ClientID})
		idToken, err2 := v.Verify(ctx, rawIDToken)
		if err2 != nil {
			Logger.Warn("IdTokenVerifyError: ", err2)
			return errors.New("IdTokenVerifyError"), nil
		}
		if nonce != "" {
			// 验证 nonce
			var claims struct {
				Nonce string `json:"nonce"`
			}
			if err2 = idToken.Claims(&claims); err2 != nil {
				Logger.Warn("Failed to parse ID Token claims: ", err)
				return errors.New("IDTokenClaimsError"), nil
			}

			if claims.Nonce != nonce {
				Logger.Warn("Nonce does not match")
				return errors.New("NonceDoesNotMatch"), nil
			}
		}
	}

	// 获取用户信息
	client = oauthConfig.Client(ctx, token)
	resp, err := client.Get(provider.UserInfoEndpoint())
	if err != nil {
		Logger.Warn("failed getting user info: ", err)
		return errors.New("GetOauthUserInfoError"), nil
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			Logger.Warn("failed closing response body: ", closeErr)
		}
	}()

	// 解析用户信息
	if err = json.NewDecoder(resp.Body).Decode(userData); err != nil {
		Logger.Warn("failed decoding user info: ", err)
		return errors.New("DecodeOauthUserInfoError"), nil
	}

	return nil, client
}

// githubCallback github回调
func (os *OauthService) githubCallback(oauthConfig *oauth2.Config, provider *oidc.Provider, code, verifier, nonce string) (error, *model.OauthUser) {
	var user = &model.GithubUser{}
	err, client := os.callbackBase(oauthConfig, provider, code, verifier, nonce, user)
	if err != nil {
		return err, nil
	}
	err = os.getGithubPrimaryEmail(client, user)
	if err != nil {
		return err, nil
	}
	return nil, user.ToOauthUser()
}

// linuxdoCallback linux.do回调
func (os *OauthService) linuxdoCallback(oauthConfig *oauth2.Config, provider *oidc.Provider, code, verifier, nonce string) (error, *model.OauthUser) {
	var user = &model.LinuxdoUser{}
	err, _ := os.callbackBase(oauthConfig, provider, code, verifier, nonce, user)
	if err != nil {
		return err, nil
	}
	return nil, user.ToOauthUser()
}

// oidcCallback oidc回调, 通过code获取用户信息
func (os *OauthService) oidcCallback(oauthConfig *oauth2.Config, provider *oidc.Provider, code, verifier, nonce string) (error, *model.OauthUser) {
	var user = &model.OidcUser{}
	if err, _ := os.callbackBase(oauthConfig, provider, code, verifier, nonce, user); err != nil {
		return err, nil
	}
	return nil, user.ToOauthUser()
}

// Callback: Get user information by code and op(Oauth provider)
func (os *OauthService) Callback(code, verifier, op, nonce string) (err error, oauthUser *model.OauthUser) {
	err, oauthInfo, oauthConfig, provider := os.GetOauthConfig(op)
	// oauthType is already validated in GetOauthConfig
	if err != nil {
		return err, nil
	}
	oauthType := oauthInfo.OauthType
	switch oauthType {
	case model.OauthTypeGithub:
		err, oauthUser = os.githubCallback(oauthConfig, provider, code, verifier, nonce)
	case model.OauthTypeLinuxdo:
		err, oauthUser = os.linuxdoCallback(oauthConfig, provider, code, verifier, nonce)
	case model.OauthTypeOidc, model.OauthTypeGoogle:
		err, oauthUser = os.oidcCallback(oauthConfig, provider, code, verifier, nonce)
	default:
		return errors.New("unsupported OAuth type"), nil
	}
	return err, oauthUser
}

func (os *OauthService) UserThirdInfo(op string, openId string) *model.UserThird {
	ut := &model.UserThird{}
	DB.Where("open_id = ? and op = ?", openId, op).First(ut)
	return ut
}

// BindOauthUser: Bind third party account
func (os *OauthService) BindOauthUser(userId uint, oauthUser *model.OauthUser, op string) error {
	if oauthUser == nil || oauthUser.OpenId == "" || !AllService.UserService.CheckUserEnable(AllService.UserService.InfoById(userId)) {
		return errors.New("invalid OAuth identity")
	}
	utr := &model.UserThird{}
	err, oauthType := os.GetTypeByOp(op)
	if err != nil {
		return err
	}
	utr.FromOauthUser(userId, oauthUser, oauthType, op)
	return DB.Create(utr).Error
}

// UnBindOauthUser: Unbind third party account
func (os *OauthService) UnBindOauthUser(userId uint, op string) error {
	return os.UnBindThird(op, userId)
}

// UnBindThird: Unbind third party account
func (os *OauthService) UnBindThird(op string, userId uint) error {
	return DB.Where("user_id = ? and op = ?", userId, op).Delete(&model.UserThird{}).Error
}

// DeleteUserByUserId: When user is deleted, delete all third party bindings
func (os *OauthService) DeleteUserByUserId(userId uint) error {
	return DB.Where("user_id = ?", userId).Delete(&model.UserThird{}).Error
}

// InfoById 根据id获取Oauth信息
func (os *OauthService) InfoById(id uint) *model.Oauth {
	oauthInfo := &model.Oauth{}
	DB.Where("id = ?", id).First(oauthInfo)
	return oauthInfo
}

// InfoByOp 根据op获取Oauth信息
func (os *OauthService) InfoByOp(op string) *model.Oauth {
	oauthInfo := &model.Oauth{}
	DB.Where("op = ?", op).First(oauthInfo)
	return oauthInfo
}

// Helper function to get scopes by operation
func (os *OauthService) getScopesByOp(op string) []string {
	scopes := os.InfoByOp(op).Scopes
	return os.constructScopes(scopes)
}

// Helper function to construct scopes
func (os *OauthService) constructScopes(scopes string) []string {
	scopes = strings.TrimSpace(scopes)
	if scopes == "" {
		scopes = model.OIDC_DEFAULT_SCOPES
	}
	return strings.Split(scopes, ",")
}

func (os *OauthService) List(page, pageSize uint, where func(tx *gorm.DB)) (res *model.OauthList) {
	res = &model.OauthList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.Oauth{})
	if where != nil {
		where(tx)
	}
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, pageSize))
	tx.Find(&res.Oauths)
	return
}

// GetTypeByOp 根据op获取OauthType
func (os *OauthService) GetTypeByOp(op string) (error, string) {
	oauthInfo := &model.Oauth{}
	if DB.Where("op = ?", op).First(oauthInfo).Error != nil {
		return fmt.Errorf("OAuth provider with op '%s' not found", op), ""
	}
	return nil, oauthInfo.OauthType
}

// ValidateOauthProvider 验证Oauth提供者是否正确
func (os *OauthService) ValidateOauthProvider(op string) error {
	if !os.IsOauthProviderExist(op) {
		return fmt.Errorf("OAuth provider with op '%s' not found", op)
	}
	return nil
}

// IsOauthProviderExist 验证Oauth提供者是否存在
func (os *OauthService) IsOauthProviderExist(op string) bool {
	oauthInfo := &model.Oauth{}
	// 使用 Gorm 的 Take 方法查找符合条件的记录
	if err := DB.Where("op = ?", op).Take(oauthInfo).Error; err != nil {
		return false
	}
	return true
}

// Create 创建
func (os *OauthService) Create(oauthInfo *model.Oauth) error {
	err := oauthInfo.FormatOauthInfo()
	if err != nil {
		return err
	}
	res := DB.Create(oauthInfo).Error
	return res
}
func (os *OauthService) Delete(oauthInfo *model.Oauth) error {
	var count int64
	if err := DB.Model(&model.UserThird{}).Where("op = ?", oauthInfo.Op).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errors.New("provider has bound users")
	}
	return DB.Delete(oauthInfo).Error
}

// Update 更新
func (os *OauthService) Update(oauthInfo *model.Oauth) error {
	var current model.Oauth
	if err := DB.First(&current, oauthInfo.Id).Error; err != nil {
		return err
	}
	// Provider identity is immutable; changing its issuer can reassign existing subjects.
	if current.Op != oauthInfo.Op || current.Issuer != oauthInfo.Issuer || current.OauthType != oauthInfo.OauthType {
		return errors.New("provider identity is immutable")
	}
	if oauthInfo.AutoRegister == nil {
		oauthInfo.AutoRegister = current.AutoRegister
	}
	if oauthInfo.PkceEnable == nil {
		oauthInfo.PkceEnable = current.PkceEnable
	}
	if err := oauthInfo.FormatOauthInfo(); err != nil {
		return err
	}
	fields := []string{"scopes", "client_id", "pkce_method", "auto_register", "pkce_enable"}
	if oauthInfo.ClientSecret != "" {
		fields = append(fields, "client_secret")
	}
	return DB.Model(oauthInfo).Select(fields).Updates(oauthInfo).Error
}

// GetOauthProviders 获取所有的provider
func (os *OauthService) GetOauthProviders() []string {
	var res []string
	DB.Model(&model.Oauth{}).Pluck("op", &res)
	return res
}

// getGithubPrimaryEmail: Get the primary email of the user from Github
func (os *OauthService) getGithubPrimaryEmail(client *http.Client, githubUser *model.GithubUser) error {
	// the client is already set with the token
	resp, err := client.Get("https://api.github.com/user/emails")
	if err != nil {
		return fmt.Errorf("failed to fetch emails: %w", err)
	}
	defer resp.Body.Close()

	// check the response status code
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch emails: %s", resp.Status)
	}

	// decode the response
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// find the primary verified email
	for _, e := range emails {
		if e.Primary && e.Verified {
			githubUser.Email = e.Email
			githubUser.VerifiedEmail = e.Verified
			return nil
		}
	}

	return fmt.Errorf("no primary verified email found")
}
