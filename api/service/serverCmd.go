package service

import (
	"fmt"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"io"
	"net"
	"strings"
	"time"
)

type ServerCmdService struct{}

// List
func (is *ServerCmdService) List(page, pageSize uint) (res *model.ServerCmdList) {
	res = &model.ServerCmdList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.ServerCmd{})
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, pageSize))
	tx.Find(&res.ServerCmds)
	return
}

// Info
func (is *ServerCmdService) Info(id uint) *model.ServerCmd {
	u := &model.ServerCmd{}
	DB.Where("id = ?", id).First(u)
	return u
}

// Delete
func (is *ServerCmdService) Delete(u *model.ServerCmd) error {
	return DB.Delete(u).Error
}

// Create
func (is *ServerCmdService) Create(u *model.ServerCmd) error {
	res := DB.Create(u).Error
	return res
}

// SendCmd 发送命令
func (is *ServerCmdService) SendCmd(socket string, cmd string, arg string) (string, error) {
	if socket == "" {
		return "", fmt.Errorf("control socket is not configured")
	}
	if cmd == "" || strings.ContainsAny(cmd+arg, "\r\n\x00") || len(cmd)+len(arg) > 1000 {
		return "", fmt.Errorf("invalid control command")
	}
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return "", err
	}
	if _, err := io.WriteString(conn, cmd+" "+arg); err != nil {
		return "", err
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		return "", err
	}
	response, err := io.ReadAll(io.LimitReader(conn, 65537))
	if err != nil {
		return "", err
	}
	if len(response) > 65536 {
		return "", fmt.Errorf("control response too large")
	}
	return string(response), nil
}

func (is *ServerCmdService) Update(f *model.ServerCmd) error {
	return DB.Model(f).Updates(f).Error
}
