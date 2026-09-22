package gate

import "testing"

func TestParseCard(t *testing.T) {
	tests := []struct {
		name     string
		screen   string
		ok       bool
		key      string
		label    string
		proposed string
	}{
		{
			name: "claude yes is option 1",
			screen: "" +
				"Bash command\n" +
				"\n" +
				"  go test ./...\n" +
				"\n" +
				"Do you want to proceed?\n" +
				"❯ 1. Yes\n" +
				"  2. Yes, and don't ask again for go test commands in this project\n" +
				"  3. No, and tell Claude what to do differently\n",
			ok:       true,
			key:      "1",
			label:    "Yes",
			proposed: "go test ./...",
		},
		{
			name: "grok allow once",
			screen: "" +
				"Bash\n" +
				"pytest -q\n" +
				"❯ 1. Allow once\n" +
				"  2. Always allow: pytest\n" +
				"  3. Reject once\n",
			ok:       true,
			key:      "1",
			label:    "Allow once",
			proposed: "pytest -q",
		},
		{
			name: "single allow is not the first row",
			screen: "" +
				"Do you want to proceed?\n" +
				"rm -rf /tmp/build\n" +
				"❯ 1. Always allow: rm -rf /tmp/build\n" +
				"  2. Allow once\n" +
				"  3. Reject once\n",
			ok:    true,
			key:   "2",
			label: "Allow once",
		},
		{
			name: "parenthesis numbering",
			screen: "" +
				"Allow command?\n" +
				"git status\n" +
				"1) Yes\n" +
				"2) No\n",
			ok:       true,
			key:      "1",
			label:    "Yes",
			proposed: "Allow command?\ngit status",
		},
		{
			name: "unnumbered grok rows",
			screen: "" +
				"Bash\n" +
				"cargo test\n" +
				"❯ Allow once\n" +
				"  Always allow: cargo test\n" +
				"  Reject once\n",
			ok:       true,
			key:      "1",
			label:    "Allow once",
			proposed: "cargo test",
		},
		{
			name: "claude create file with session-wide second row",
			screen: "" +
				"1 print(\"Hello, world!\")\n" +
				"Do you want to create hello.py?\n" +
				"❯ 1. Yes\n" +
				"  2. Yes, and switch to accept edits (auto-approve file edits and common file commands) for this session (shift+tab)\n" +
				"  3. No\n",
			ok:       true,
			key:      "1",
			label:    "Yes",
			proposed: "1 print(\"Hello, world!\")",
		},
		{
			name: "question with yes is not a command card",
			screen: "" +
				"Do you want me to refactor the handler?\n" +
				"❯ 1. Yes\n" +
				"  2. No\n",
		},
		{
			name: "design question",
			screen: "" +
				"Which database should I use?\n" +
				"❯ 1. Postgres\n" +
				"  2. SQLite\n",
		},
		{
			name: "always allow has no single-shot row",
			screen: "" +
				"Do you want to proceed?\n" +
				"git push\n" +
				"❯ 1. Always allow: git push\n" +
				"  2. Reject once\n",
		},
		{
			name:   "old approval scrolled off the tail",
			screen: "Do you want to proceed?\n❯ 1. Yes\n  2. No\n" + repeat("line of finished work\n", 45) + "Ready.",
		},
		{
			name:   "footer always-approve is not a card",
			screen: "Tip: Use @! for hidden files.\n❯\nGrok 4.6 (high) · always-approve\n[stable]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseCard(tt.screen)
			if ok != tt.ok {
				t.Fatalf("ok=%v want %v (%+v)", ok, tt.ok, got)
			}
			if !ok {
				return
			}
			if got.Key != tt.key || got.Label != tt.label {
				t.Fatalf("key=%s label=%q", got.Key, got.Label)
			}
			if tt.proposed != "" && got.Proposed != tt.proposed {
				t.Fatalf("proposed=%q", got.Proposed)
			}
		})
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
