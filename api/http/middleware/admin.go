package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/service"
)

// BackendUserAuth 后台权限验证中间件
func BackendUserAuth() gin.HandlerFunc {
	return func(c *gin.Context) {

		token := c.GetHeader("api-token")
		uid, err := service.AllService.UserService.VerifyJWT(token)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"code": 401, "msg": "Unauthorized"})
			return
		}
		user, _ := service.AllService.UserService.InfoByAccessToken(token)
		if user.Id == 0 || user.Id != uid {
			c.AbortWithStatusJSON(401, gin.H{"code": 401, "msg": "Unauthorized"})
			return
		}

		if !service.AllService.UserService.CheckUserEnable(user) {
			c.JSON(401, gin.H{
				"error": "Unauthorized",
			})
			c.Abort()
			return
		}

		c.Set("curUser", user)
		c.Set("token", token)

		c.Next()
	}
}
