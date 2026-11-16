//go:build !windows

package service

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestControlSocketFramingAndResponseLimit(t *testing.T) {
	for _, size := range []int{4096, 65537} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			dir, err := os.MkdirTemp("", "control-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, "cmd.sock")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			received := make(chan string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					received <- "accept failed"
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				request, err := io.ReadAll(conn)
				if err != nil {
					received <- "read failed"
					return
				}
				received <- string(request)
				_, _ = io.WriteString(conn, strings.Repeat("x", size))
			}()
			result, err := (&ServerCmdService{}).SendCmd(path, "ml", "Y")
			if request := <-received; request != "ml Y" {
				t.Fatalf("bad framing %q", request)
			}
			if size <= 65536 && (err != nil || len(result) != size) {
				t.Fatalf("response truncated: %d %v", len(result), err)
			}
			if size > 65536 && err == nil {
				t.Fatal("oversized response accepted")
			}
		})
	}
}

func TestControlRejectsInvalidCommands(t *testing.T) {
	for _, tc := range []struct{ socket, cmd, arg string }{
		{"", "ml", "Y"}, {"/nonexistent", "ml", "Y\nml N"}, {"/nonexistent", "", ""}, {"/nonexistent", "x", strings.Repeat("a", 1001)},
	} {
		if _, err := (&ServerCmdService{}).SendCmd(tc.socket, tc.cmd, tc.arg); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
}
