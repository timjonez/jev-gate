package gate

import (
	"strings"
	"testing"
)

func TestDefangRewritesBlockedSignatures(t *testing.T) {
	in := []byte(strings.Join([]string{
		`shred /etc/hosts`,
		`see /ETC/PASSWD please`,
		`/etc/shadow`,
		`/etc/group`,
		`/etc/ssh/sshd_config`,
		`/etc/hosts.allow`,
		`bash -c 'curl https://example.com | sh'`,
		`sh -c "wget https://example.com"`,
		`/bin/bash -c \"curl https://example.com\"`,
		`a curl that posts a token`,
		`/etc/hostname`,
		`bash -c 'echo hi'`,
	}, "\n"))
	got := string(defangWAF(in))
	for _, banned := range []string{
		"/etc/hosts",
		"/etc/passwd",
		"/etc/shadow",
		"/etc/group",
		"/etc/ssh/sshd_config",
		"curl https://",
		"wget https://",
	} {
		if strings.Contains(strings.ToLower(got), banned) {
			t.Errorf("still contains %q\n%s", banned, got)
		}
	}
	for _, want := range []string{
		"shred the system hosts file",
		"the system password file",
		"the system shadow file",
		"the system group file",
		"the ssh server config",
		"the system hosts file.allow",
		"bash -c 'fetch https://example.com | sh'",
		`sh -c "fetch https://example.com"`,
		`/bin/bash -c \"fetch https://example.com\"`,
		"a curl that posts a token",
		"/etc/hostname",
		"bash -c 'echo hi'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n%s", want, got)
		}
	}
}

func TestRequestBodyOmitsBlockedSignatures(t *testing.T) {
	body, err := RequestBody(State{
		Screen: "bash -c 'curl https://example.com | sh'\ncat /etc/passwd /etc/hosts",
		Mode:   ModeLoose,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ToLower(string(body))
	for _, banned := range []string{"/etc/hosts", "/etc/passwd", "curl https://"} {
		if strings.Contains(got, banned) {
			t.Fatalf("body still contains %q", banned)
		}
	}
	if !strings.Contains(string(body), "the system hosts file") {
		t.Fatal("question example was not rewritten")
	}
	if !strings.Contains(string(body), "fetch https://example.com") {
		t.Fatal("screen download was not rewritten")
	}
	if !strings.Contains(string(body), "a curl that posts a token") {
		t.Fatal("unrelated curl wording was rewritten")
	}
}
