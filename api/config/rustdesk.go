package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

const (
	DefaultIdServerPort    = 21116
	DefaultRelayServerPort = 21117
)

type Rustdesk struct {
	IdServer        string `mapstructure:"id-server"`
	IdServerPort    int    `mapstructure:"-"`
	RelayServer     string `mapstructure:"relay-server"`
	RelayServerPort int    `mapstructure:"-"`
	ApiServer       string `mapstructure:"api-server"`
	Key             string `mapstructure:"key"`
	KeyFile         string `mapstructure:"key-file"`
	Personal        int    `mapstructure:"personal"`
	//webclient-magic-queryonline
	WebclientMagicQueryonline int    `mapstructure:"webclient-magic-queryonline"`
	WsHost                    string `mapstructure:"ws-host"`
}

func (rd *Rustdesk) LoadKeyFile() error {
	if rd.Key == "" && rd.KeyFile != "" {
		b, err := os.ReadFile(rd.KeyFile)
		if err != nil {
			return fmt.Errorf("read RustDesk public key: %w", err)
		}
		rd.Key = string(b)
	}
	rd.Key = strings.TrimSpace(rd.Key)
	if rd.Key != "" {
		decoded, err := base64.StdEncoding.DecodeString(rd.Key)
		if err != nil || len(decoded) != 32 {
			return fmt.Errorf("RustDesk public key must be base64-encoded 32 bytes")
		}
	} else if rd.KeyFile != "" {
		return fmt.Errorf("RustDesk public key file is empty")
	}
	return nil
}
