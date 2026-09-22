package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Card is a live tool-permission prompt whose single-shot allow option can be
// pressed by number. Always-allow and session-wide rows are never selected.
type Card struct {
	Key      string
	Label    string
	Proposed string
	Tail     string
}

var optionLine = regexp.MustCompile(`^\s*[❯›>●▶]?\s*(\d)\s*[.)]\s+(\S.*?)\s*$`)

// ParseCard reports whether the tail of screen is a permission card with a
// single-shot allow option. Numbered answers to an ordinary question are not
// a card: "Yes" counts only next to permission wording, and "Allow once"
// counts on its own. The returned key is that option's digit.
func ParseCard(screen string) (Card, bool) {
	tail := Tail(screen, 40, 4000)
	if tail == "" {
		return Card{}, false
	}
	lines := strings.Split(tail, "\n")
	var opts []option
	first := -1
	for i, line := range lines {
		m := optionLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > 9 {
			continue
		}
		if first < 0 {
			first = i
		}
		opts = append(opts, option{N: n, Label: strings.TrimSpace(m[2])})
	}
	if len(opts) == 0 {
		return parseUnnumbered(lines, tail)
	}
	return cardFromOptions(opts, lines, tail, first)
}

type option struct {
	N     int
	Label string
}

func isSingleAllow(label string) bool {
	s := normLabel(label)
	if s == "" || deniesSingle(s) {
		return false
	}
	switch s {
	case "yes", "allow", "allow once", "approve", "approve once", "proceed":
		return true
	default:
		return false
	}
}

func explicitAllow(label string) bool {
	switch normLabel(label) {
	case "allow", "allow once", "approve", "approve once":
		return true
	default:
		return false
	}
}

func normLabel(label string) string {
	s := strings.ToLower(strings.TrimSpace(label))
	return strings.Trim(s, " .!")
}

func deniesSingle(s string) bool {
	for _, p := range []string{
		"always", "don't ask", "dont ask", "do not ask", "session",
		"remember", "never", "all edit", "reject", " and ", ",",
	} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// parseUnnumbered handles a Grok card that shows choice rows without digits.
// 1-9 still select those rows in order, so the single-shot row's position is the key.
func parseUnnumbered(lines []string, tail string) (Card, bool) {
	end := len(lines) - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	var labels []string
	first := end + 1
	for i := end; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			break
		}
		label := stripMarker(line)
		if !optionVocabulary(label) {
			break
		}
		labels = append(labels, label)
		first = i
	}
	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}
	if len(labels) == 0 || len(labels) > 9 {
		return Card{}, false
	}
	var opts []option
	for i, label := range labels {
		opts = append(opts, option{N: i + 1, Label: label})
	}
	return cardFromOptions(opts, lines, tail, first)
}

func cardFromOptions(opts []option, lines []string, tail string, first int) (Card, bool) {
	single := lowestSingle(opts)
	if single == nil {
		return Card{}, false
	}
	// A bare Yes is a permission card when a sibling row is session-wide or
	// "don't ask again". A plain Yes/No question has no such row.
	if !explicitAllow(single.Label) && !permissionContext(tail) && !hasStickySibling(opts) {
		return Card{}, false
	}
	return Card{
		Key:      strconv.Itoa(single.N),
		Label:    single.Label,
		Proposed: proposedText(lines, first),
		Tail:     tail,
	}, true
}

func hasStickySibling(opts []option) bool {
	for _, o := range opts {
		if deniesSingle(normLabel(o.Label)) {
			return true
		}
	}
	return false
}

func lowestSingle(opts []option) *option {
	var single *option
	for i := range opts {
		if !isSingleAllow(opts[i].Label) {
			continue
		}
		if single == nil || opts[i].N < single.N {
			single = &opts[i]
		}
	}
	return single
}

func stripMarker(line string) string {
	line = strings.TrimSpace(line)
	for _, m := range []string{"❯", "›", ">", "●", "▶"} {
		if strings.HasPrefix(line, m) {
			return strings.TrimSpace(strings.TrimPrefix(line, m))
		}
	}
	return line
}

func optionVocabulary(label string) bool {
	s := normLabel(stripMarker(label))
	if s == "" {
		return false
	}
	for _, p := range []string{
		"allow once", "allow", "always allow", "always", "reject",
		"yes", "no", "approve once", "approve", "proceed",
		"don't allow", "do not allow", "never allow", "never",
	} {
		if s == p || strings.HasPrefix(s, p+" ") || strings.HasPrefix(s, p+",") || strings.HasPrefix(s, p+":") {
			return true
		}
	}
	return false
}

func permissionContext(tail string) bool {
	s := strings.ToLower(tail)
	for _, p := range []string{
		"do you want to proceed",
		"do you want to make this edit",
		"do you want to allow",
		"allow this command",
		"allow this tool",
		"allow this edit",
		"allow command",
		"allow the edit",
		"bash command",
		"permission request",
		"needs approval",
		"needs your approval",
		"waiting for approval",
		"wants to run",
		"run this command",
		"execute this command",
		"tool permission",
		"approve this",
	} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func proposedText(lines []string, firstOption int) string {
	if firstOption < 0 {
		return ""
	}
	var kept []string
	for i := firstOption - 1; i >= 0 && len(kept) < 8; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || chromeLine(line) {
			if len(kept) > 0 {
				break
			}
			continue
		}
		kept = append(kept, line)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	text := strings.Join(kept, "\n")
	if utf8.RuneCountInString(text) > 500 {
		r := []rune(text)
		text = string(r[len(r)-500:])
	}
	return strings.TrimSpace(text)
}

func chromeLine(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(l, "do you want"),
		l == "bash command",
		l == "bash",
		l == "edit file",
		l == "permission",
		strings.Contains(l, "permission prompt"):
		return true
	}
	stripped := strings.Trim(l, "╭╮╰╯─│┌┐└┘├┤┬┴┼═║╔╗╚╝ ▏")
	return stripped == ""
}

// Tail keeps the last maxLines of screen, then the last maxRunes.
func Tail(screen string, maxLines, maxRunes int) string {
	screen = strings.TrimSpace(screen)
	if screen == "" {
		return ""
	}
	if maxLines <= 0 {
		maxLines = 40
	}
	lines := strings.Split(screen, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	r := []rune(text)
	return string(r[len(r)-maxRunes:])
}

func fingerprint(screen string) string {
	sum := sha256.Sum256([]byte(Tail(screen, 40, 4000)))
	return hex.EncodeToString(sum[:])
}
