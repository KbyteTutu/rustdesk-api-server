package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditF15PublicKeyContract(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	for _, tc := range []struct {
		name, value string
		ok          bool
	}{{"valid", valid, true}, {"trimmed", "\n " + valid + "\n", true}, {"bad-base64", "not a key", false}, {"short", base64.StdEncoding.EncodeToString([]byte("short")), false}} {
		t.Run(tc.name, func(t *testing.T) {
			rd := Rustdesk{Key: tc.value}
			err := rd.LoadKeyFile()
			if (err == nil) != tc.ok {
				t.Fatalf("error=%v", err)
			}
			if tc.ok && rd.Key != valid {
				t.Fatal("key not normalized")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "public.key")
	if err := (&Rustdesk{KeyFile: path}).LoadKeyFile(); err == nil {
		t.Fatal("missing configured file accepted")
	}
	for _, tc := range []struct {
		contents string
		ok       bool
	}{{"", false}, {"\n", false}, {valid + "\n", true}} {
		if err := os.WriteFile(path, []byte(tc.contents), 0600); err != nil {
			t.Fatal(err)
		}
		rd := Rustdesk{KeyFile: path}
		if err := rd.LoadKeyFile(); (err == nil) != tc.ok {
			t.Fatalf("file contents %q error=%v", tc.contents, err)
		}
	}
}
