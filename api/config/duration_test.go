package config

import (
	"github.com/spf13/viper"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func initDurations(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	viper.Reset()
	t.Cleanup(viper.Reset)
	c := &Config{}
	Init(c, path)
	return c
}

func TestInitRejectsBareDuration(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("bare duration accepted")
		}
	}()
	initDurations(t, "app:\n  token-expire: 360000\n")
}
func TestInitDuration(t *testing.T) {
	c := initDurations(t, "app:\n  token-expire: 24h\n")
	if c.App.TokenExpire != 24*time.Hour {
		t.Fatal(c.App.TokenExpire)
	}
}
func TestInitEnvDuration(t *testing.T) {
	t.Setenv("RUSTDESK_API_APP_TOKEN_EXPIRE", "2h")
	c := initDurations(t, "app:\n  token-expire: 24h\n")
	if c.App.TokenExpire != 2*time.Hour {
		t.Fatal(c.App.TokenExpire)
	}
}
