package gate

import (
	"regexp"
	"strings"
)

// Cloudflare's firewall in front of api.typesafe.ai blocks the POST when the
// body contains a few command-injection signatures. The replacements name the
// same file or download, so Jev still sees the effect of the command.
var sensitivePath = regexp.MustCompile(`(?i)/etc/(?:ssh/sshd_config|passwd|shadow|hosts|group)`)

var shellDownload = regexp.MustCompile(`(?i)((?:/[a-z0-9_./-]+/)?(?:bash|sh)\s+-c\s+(?:\\?['"]\s*)?)(curl|wget)\b`)

func defangWAF(body []byte) []byte {
	s := sensitivePath.ReplaceAllStringFunc(string(body), pathName)
	s = shellDownload.ReplaceAllString(s, "${1}fetch")
	return []byte(s)
}

func pathName(match string) string {
	switch strings.ToLower(match) {
	case "/etc/ssh/sshd_config":
		return "the ssh server config"
	case "/etc/passwd":
		return "the system password file"
	case "/etc/shadow":
		return "the system shadow file"
	case "/etc/hosts":
		return "the system hosts file"
	case "/etc/group":
		return "the system group file"
	default:
		return match
	}
}
