package hcx

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveSocket picks the herdr-core Unix socket.
//
// Order: explicit socket, HC_SOCKET, HC_TERMINAL_SOCKET (set inside every hc
// terminal), then <runtime>/<session>.sock where runtime is HC_RUNTIME or
// $XDG_RUNTIME_DIR/herdr-core and session is HC_SESSION or "default".
// It returns "" when no candidate can be named.
func ResolveSocket(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, env := range []string{"HC_SOCKET", "HC_TERMINAL_SOCKET"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	session := os.Getenv("HC_SESSION")
	if session == "" {
		session = "default"
	}
	dir := os.Getenv("HC_RUNTIME")
	if dir == "" {
		if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
			dir = filepath.Join(xdg, "herdr-core")
		} else if uid := os.Getuid(); uid >= 0 {
			dir = filepath.Join("/run/user", fmt.Sprint(uid), "herdr-core")
		}
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, session+".sock")
}

// Available reports whether socket names an existing Unix socket.
func Available(socket string) bool {
	if socket == "" {
		return false
	}
	fi, err := os.Stat(socket)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}
