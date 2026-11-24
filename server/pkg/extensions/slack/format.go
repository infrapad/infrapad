package slack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This file holds the lightweight Slack-to-Markdown conversions used by
// the expander: precise timestamp parsing, permalink handling, text
// normalization, and structured attachment conversion.
// They are deliberately partial; this is not a complete Slack formatter.

// parseSlackTimestamp parses a Slack message timestamp ("1788873542.372739")
// with integer arithmetic, preserving microsecond precision, and returns the
// UTC event time. An invalid timestamp is an error rather than being
// silently dropped or arbitrarily positioned.
func parseSlackTimestamp(ts string) (time.Time, error) {
	sec, frac, found := strings.Cut(ts, ".")
	if !found || sec == "" || frac == "" || len(frac) > 9 {
		return time.Time{}, fmt.Errorf("invalid Slack message timestamp %q", ts)
	}
	s, err := strconv.ParseInt(sec, 10, 64)
	if err != nil || s < 0 {
		return time.Time{}, fmt.Errorf("invalid Slack message timestamp %q", ts)
	}
	for _, c := range frac {
		if c < '0' || c > '9' {
			return time.Time{}, fmt.Errorf("invalid Slack message timestamp %q", ts)
		}
	}
	ns, _ := strconv.ParseInt(frac+strings.Repeat("0", 9-len(frac)), 10, 64)
	return time.Unix(s, ns).UTC(), nil
}

// threadURLPattern matches absolute Slack thread permalinks:
// https://<workspace>/archives/<channelID>/p<16-digit timestamp>.
// The workspace hostname is not hard-coded.
var threadURLPattern = regexp.MustCompile(`^(https?://[^/\s]+)/archives/([A-Z0-9]+)/p([0-9]{16})$`)

// threadLocation holds the workspace and channel coordinates of a thread
// permalink, used to derive message permalinks.
type threadLocation struct {
	baseURL string // e.g. "https://team.slack.com"
	channel string // Slack channel ID
}

// parseThreadURL validates a thread permalink and returns its location.
func parseThreadURL(url string) (threadLocation, error) {
	m := threadURLPattern.FindStringSubmatch(url)
	if m == nil {
		return threadLocation{}, fmt.Errorf("invalid Slack thread permalink %q", url)
	}
	return threadLocation{baseURL: m[1], channel: m[2]}, nil
}

// permalink derives the Slack permalink of a message with the given Slack
// timestamp from the thread's workspace and channel.
func (l threadLocation) permalink(ts string) string {
	return l.baseURL + "/archives/" + l.channel + "/p" + strings.ReplaceAll(ts, ".", "")
}

// slackLinkPattern matches Slack mrkdwn links: <url> and <url|label>.
// User mentions are expected to be resolved into ReadableText already and
// are not matched because their content has no URL scheme.
var slackLinkPattern = regexp.MustCompile(`<(https?://[^|>\s]+|mailto:[^|>\s]+)(\|([^>]*))?>`)

// normalizeSlackText applies light Slack formatting normalization:
//   - Slack links are converted into Markdown links,
//   - Slack escapes (&lt;, &gt;, &amp;) are decoded,
//   - inline triple-backtick fences are moved onto separate lines,
//   - Slack bullets, bold, and hard line breaks become Markdown equivalents.
//
// Code is excluded from formatting conversion. Emoji shortcodes and other
// unsupported Slack constructs remain as supplied.
func normalizeSlackText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return normalizeSlackFormatting(normalizeFences(decodeSlackEscapes(s)))
}

// decodeSlackEscapes decodes the entities Slack escapes in message text.
// &amp; is decoded last so escaped entities survive as literals.
func decodeSlackEscapes(s string) string {
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	return strings.ReplaceAll(s, "&amp;", "&")
}

// normalizeFences rewrites lines so that every triple-backtick fence is
// placed on a line of its own, with nothing after it: the text after ``` is
// reserved for the code type, and Slack inline fences carry none. Fences
// buried inside a line would otherwise leave the code fence open and let it
// swallow following content.
func normalizeFences(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "```") {
			continue
		}
		lines[i] = splitInlineFences(line)
	}
	return strings.Join(lines, "\n")
}

// splitInlineFences puts every ``` occurrence in line onto its own line.
// Content before a fence keeps its place on the preceding line; content
// after it starts the next line, leaving the fence's info string blank.
func splitInlineFences(line string) string {
	parts := strings.Split(line, "```")
	for i := range parts[:len(parts)-1] {
		parts[i] = strings.TrimRight(parts[i], " \t")
	}
	return strings.Trim(strings.Join(parts, "\n```\n"), "\n")
}

// unknownAuthor labels messages without any resolvable author.
const unknownAuthor = "Unknown author"

// noReadableContent is the visible placeholder body of entirely unreadable
// messages, preserving their identity instead of failing the payload.
const noReadableContent = "No readable message content"

// previewTimestampFormat is the readable UTC format for preview timestamps.
const previewTimestampFormat = "2006-01-02 15:04:05 MST"

// messageAuthor resolves the display author of a source message: the
// resolved user name when available, the raw user ID next, and an explicit
// unknown-author label last.
func messageAuthor(message SourceMessage) string {
	switch {
	case message.UserName != "":
		return message.UserName
	case message.User != "":
		return message.User
	default:
		return unknownAuthor
	}
}

// messageContent assembles the canonical content of a source message: the
// normalized Markdown body (readable text, falling back to the raw text)
// and the structured attachments derived from its typed previews.
// Previews without an HTTP(S) source URL have no stable attachment identity;
// their readable rendering is appended to the body instead, so the evidence
// is kept without inventing positional or text-derived keys.
// Entirely unreadable messages get a visible placeholder body.
func messageContent(message SourceMessage) (string, []Attachment) {
	text := message.ReadableText
	if text == "" {
		text = message.Text
	}
	var parts []string
	if body := normalizeSlackText(text); body != "" {
		parts = append(parts, body)
	}
	var attachments []Attachment
	for _, preview := range message.Previews {
		if attachment, ok := previewAttachment(preview); ok {
			attachments = append(attachments, attachment)
		} else if rendered := renderUnlinkedPreview(preview); rendered != "" {
			parts = append(parts, rendered)
		}
	}
	if len(parts) == 0 && len(attachments) == 0 {
		return noReadableContent, nil
	}
	return strings.Join(parts, "\n\n"), attachments
}

// previewAttachment converts a typed preview with an HTTP(S) source URL
// into a structured attachment: the URL-derived key, a Markdown link, and
// the remaining preview evidence as Markdown details.
func previewAttachment(preview AttachmentPreview) (Attachment, bool) {
	source := previewSourceURL(preview)
	if source == "" {
		return Attachment{}, false
	}
	label := escapeMarkdownLabel(preview.Title)
	if label == "" {
		label = escapeMarkdownLabel(source)
	}
	var details []string
	if preview.ProductName != "" {
		details = append(details, "**Product:** "+preview.ProductName)
	}
	if preview.TitleLink != "" && preview.TitleLink != source {
		details = append(details, "**Link:** "+preview.TitleLink)
	}
	details = append(details, previewEvidence(preview)...)
	return Attachment{
		Key:     AttachmentKey(source),
		Link:    fmt.Sprintf("[%s](%s)", label, source),
		Details: strings.Join(details, "\n\n"),
	}, true
}

// previewSourceURL returns the identity URL of a preview: the Work Object
// URL, falling back to the title link. Only HTTP(S) URLs qualify.
func previewSourceURL(preview AttachmentPreview) string {
	for _, candidate := range []string{preview.URL, preview.TitleLink} {
		if httpURLPattern.MatchString(candidate) {
			return candidate
		}
	}
	return ""
}

var httpURLPattern = regexp.MustCompile(`^(?i)https?://[^/\s][^\s]*$`)

// previewEvidence renders the preview text, labeled fields, and update time.
func previewEvidence(preview AttachmentPreview) []string {
	var lines []string
	if preview.Text != "" {
		lines = append(lines, preview.Text)
	}
	for _, field := range preview.Fields {
		lines = append(lines, fmt.Sprintf("**%s:** %s", field.Label, field.Value))
	}
	if !preview.UpdatedAt.IsZero() {
		lines = append(lines, "**Updated:** "+preview.UpdatedAt.UTC().Format(previewTimestampFormat))
	}
	return lines
}

// renderUnlinkedPreview renders a preview without a source URL as a
// blockquote appended to the message body. It returns an empty string for
// a preview with no readable piece at all.
func renderUnlinkedPreview(preview AttachmentPreview) string {
	var lines []string
	heading := escapeMarkdownLabel(preview.Title)
	if heading == "" {
		heading = preview.ProductName
	}
	if heading != "" {
		line := fmt.Sprintf("Attachment: **%s**", heading)
		if preview.ProductName != "" && heading != preview.ProductName {
			line += " · " + preview.ProductName
		}
		lines = append(lines, line)
	}
	lines = append(lines, previewEvidence(preview)...)
	if len(lines) == 0 {
		return ""
	}
	return blockquote(strings.Join(lines, "\n\n"))
}

// AttachmentKey derives the stable, message-scoped key of an attachment
// from its source URL. It is deterministic, independent of preview text
// and attachment order, and uses only the identity-key alphabet
// [A-Za-z0-9._:-]. Equal URLs deliberately produce equal keys.
func AttachmentKey(sourceURL string) string {
	sum := sha256.Sum256([]byte(sourceURL))
	return attachmentKeyPrefix + hex.EncodeToString(sum[:attachmentKeyBytes])
}

const (
	attachmentKeyPrefix = "url:"
	attachmentKeyBytes  = 12
)

// messageMarkdown composes the full Markdown evidence of a message: the
// canonical body followed by each attachment's link and details. It is used
// for exports and for agent messages, which have no structured attachments.
func messageMarkdown(body string, attachments []Attachment) string {
	var parts []string
	if body != "" {
		parts = append(parts, body)
	}
	for _, attachment := range attachments {
		parts = append(parts, attachmentMarkdown(attachment))
	}
	return strings.Join(parts, "\n\n")
}

// attachmentMarkdown renders one attachment as a blockquote headed by its
// link and followed by its complete details.
func attachmentMarkdown(attachment Attachment) string {
	text := "Attachment: " + attachment.Link
	if attachment.Details != "" {
		text += "\n\n" + attachment.Details
	}
	return blockquote(text)
}

// blockquote prefixes every line of text with a Markdown quote marker.
func blockquote(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

// escapeMarkdownLabel escapes square brackets so labels containing them
// (e.g. PagerDuty incident titles) stay inside Markdown link text.
func escapeMarkdownLabel(s string) string {
	s = strings.ReplaceAll(s, "[", "\\[")
	return strings.ReplaceAll(s, "]", "\\]")
}
