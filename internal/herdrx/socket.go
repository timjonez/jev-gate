package herdrx

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveSocket picks the Herdr Unix socket.
//
// Order: explicit socket, HERDR_SOCKET_PATH, named session
// (--session / HERDR_SESSION), then the default session socket.
func ResolveSocket(explicitSocket, session string) string {
	if explicitSocket != "" {
		return explicitSocket
	}
	if env := os.Getenv("HERDR_SOCKET_PATH"); env != "" {
		return env
	}
	if session == "" {
		session = os.Getenv("HERDR_SESSION")
	}
	base := herdrConfigDir()
	if session != "" {
		return filepath.Join(base, "sessions", session, "herdr.sock")
	}
	return filepath.Join(base, "herdr.sock")
}

// SessionKey is the durable queue partition for a Herdr session.
func SessionKey(session string) string {
	if session != "" {
		return sanitizeSessionKey(session)
	}
	if env := os.Getenv("HERDR_SESSION"); env != "" {
		return sanitizeSessionKey(env)
	}
	return "default"
}

func herdrConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "herdr")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".config", "herdr")
	}
	return filepath.Join(home, ".config", "herdr")
}

func sanitizeSessionKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		return "default"
	}
	return out
}
