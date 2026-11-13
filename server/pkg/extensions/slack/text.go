package slack

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// normalizeSlackFormatting translates Slack's line-oriented mrkdwn to
// Markdown rather than requiring Slack-specific behavior in the shared UI.
// Fences have already been moved onto separate lines by normalizeFences.
func normalizeSlackFormatting(s string) string {
	lines := strings.Split(s, "\n")
	inCode := false
	prose := make([]bool, len(lines))
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "```" {
			inCode = !inCode
			continue
		}
		// Preserve fenced and Markdown-indented code verbatim.
		if inCode || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		prose[i] = trimmed != ""
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.HasPrefix(line[indent:], "• ") {
			line = line[:indent] + "- " + strings.TrimPrefix(line[indent:], "• ")
		}
		lines[i] = normalizeSlackInline(line)
	}
	for i := 0; i+1 < len(lines); i++ {
		// Blank lines and block starts already cause a visible break. Only
		// soft paragraph/continuation breaks need Markdown's two spaces.
		if prose[i] && prose[i+1] && !slackBlockStart.MatchString(lines[i+1]) {
			lines[i] = strings.TrimRight(lines[i], " \t") + "  "
		}
	}
	return strings.Join(lines, "\n")
}

var slackBlockStart = regexp.MustCompile(`^ {0,3}(?:[-+*] |[0-9]+[.)] |#{1,6} )`)

// Inline code and link destinations must not have their asterisks converted
// to emphasis. Slack links are converted as whole tokens after emphasis is
// resolved, so bold may still enclose code or a link.
var slackInlineLiteral = regexp.MustCompile("`[^`\n]+`|<(?:https?://|mailto:)[^>\\n]+>|\\]\\([^\\n)]*\\)|https?://[^\\s<>]+")
var slackStars = regexp.MustCompile(`\*+`)

func normalizeSlackInline(line string) string {
	literals := slackInlineLiteral.FindAllStringIndex(line, -1)
	bold := slackBoldMarkers(line, literals)
	var out strings.Builder
	literal := 0
	for i := 0; i < len(line); {
		if literal < len(literals) && i == literals[literal][0] {
			end := literals[literal][1]
			token := line[i:end]
			if parts := slackLinkPattern.FindStringSubmatch(token); parts != nil && parts[0] == token {
				if parts[3] != "" {
					token = "[" + parts[3] + "](" + parts[1] + ")"
				}
			}
			out.WriteString(token)
			i = end
			literal++
			continue
		}
		if bold[i] {
			out.WriteByte('*')
		}
		out.WriteByte(line[i])
		i++
	}
	return out.String()
}

// slackBoldMarkers pairs single asterisks outside literals. Existing Markdown
// double/triple emphasis, list markers, unmatched stars, and intraword stars
// are left alone. The added asterisk at each paired marker produces **bold**.
func slackBoldMarkers(line string, literals [][]int) map[int]bool {
	bold := make(map[int]bool)
	open, literal := -1, 0
	for _, span := range slackStars.FindAllStringIndex(line, -1) {
		i := span[0]
		for literal < len(literals) && literals[literal][1] <= i {
			literal++
		}
		if span[1]-i != 1 || (literal < len(literals) && literals[literal][0] <= i) {
			continue
		}
		before, _ := utf8.DecodeLastRuneInString(line[:i])
		after, _ := utf8.DecodeRuneInString(line[i+1:])
		if open >= 0 && !unicode.IsSpace(before) && !unicode.IsLetter(after) && !unicode.IsDigit(after) {
			bold[open], bold[i] = true, true
			open = -1
		} else if !unicode.IsSpace(after) && i+1 < len(line) && !unicode.IsLetter(before) && !unicode.IsDigit(before) {
			open = i
		}
	}
	return bold
}
