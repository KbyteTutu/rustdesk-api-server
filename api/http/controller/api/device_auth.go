package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
)

// Device telemetry requires the login session's exact device identity, not a
// caller-supplied UUID alone. An existing peer must also belong to this user.
func authorizeDevice(c *gin.Context, id, uuid string) bool {
	value, _ := c.Get("deviceSession")
	session, ok := value.(*model.UserToken)
	user := service.AllService.UserService.CurUser(c)
	if !ok || session == nil || user == nil || id == "" || uuid == "" || session.DeviceId != id || session.DeviceUuid != uuid || session.UserId != user.Id {
		c.AbortWithStatusJSON(403, gin.H{"error": "Forbidden"})
		return false
	}
	peer := service.AllService.PeerService.FindById(id)
	if peer != nil && peer.RowId != 0 && (peer.UserId != user.Id || peer.Uuid != uuid) {
		c.AbortWithStatusJSON(403, gin.H{"error": "Forbidden"})
		return false
	}
	return true
}
