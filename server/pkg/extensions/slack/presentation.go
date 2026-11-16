package slack

import (
	"regexp"
	"strings"
)

// presentMessageBody reduces generated attachment previews to a list of source
// links. Previews are already embedded in stored Markdown, so this happens only
// at presentation time, without rewriting records or losing export metadata.
func presentMessageBody(body string) string {
	var lines []string
	var fence string
	inPreview := false
	for _, line := range strings.Split(body, "\n") {
		if inPreview {
			if strings.HasPrefix(line, ">") {
				continue
			}
			inPreview = false
		}
		if match := presentationFence.FindStringSubmatch(line); match != nil {
			marker := match[1]
			if fence == "" {
				fence = marker
			} else if marker[0] == fence[0] && len(marker) >= len(fence) && strings.TrimSpace(match[2]) == "" {
				fence = ""
			}
		}
		if fence == "" {
			if match := attachmentHeading.FindStringSubmatch(line); match != nil {
				inPreview = true
				if link := attachmentHeadingLink.FindStringSubmatch(match[1]); link != nil {
					lines = append(lines, "- "+link[0])
				}
				continue
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// Match only the preview format produced by renderAttachmentPreview, leaving
// ordinary blockquotes and fenced code untouched. Titles can contain escaped
// square brackets and link destinations can contain parentheses.
var attachmentHeading = regexp.MustCompile(`^> Attachment: \*\*(.*)\*\*(?: · .*)?$`)
var attachmentHeadingLink = regexp.MustCompile(`^\[(.*)\]\((https?://[^\s]+)\)$`)
var presentationFence = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
