package slack

import (
	"testing"

	"github.com/infrapad/infrapad/server/internal/utils"
)

func TestPresentMessageAttachments(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "links only, no preview metadata",
			body: utils.Dedent(`
				See these sources.

				> Attachment: **[Incident](https://example.com/incident)** · PagerDuty
				> Incident in PagerDuty
				> **Status:** Resolved
				> **Urgency:** High
				> Updated: 2026-11-16 12:22:42 UTC

				> Attachment: **[Runbook](https://example.com/runbook)** · Docs
				> Troubleshooting instructions`),
			want: "See these sources.\n\n- [Incident](https://example.com/incident)\n\n- [Runbook](https://example.com/runbook)",
		},
		{
			name: "escaped title and parentheses in destination",
			body: "> Attachment: **[\\[#123\\] Incident](https://example.com/incident_(123))** · PagerDuty\n> **Status:** Resolved",
			want: "- [\\[#123\\] Incident](https://example.com/incident_(123))",
		},
		{
			name: "URL used as title",
			body: "> Attachment: **[https://example.com](https://example.com)**\n> Preview",
			want: "- [https://example.com](https://example.com)",
		},
		{
			name: "no link",
			body: "> Attachment: **Example**\n> Fallback only preview",
			want: "",
		},
		{
			name: "unsafe link",
			body: "> Attachment: **[Unsafe](javascript:alert(1))**\n> Preview",
			want: "",
		},
		{
			name: "ordinary quotes are preserved",
			body: "> A quoted message\n> More text\n\nAfter the quote.",
			want: "> A quoted message\n> More text\n\nAfter the quote.",
		},
		{
			name: "code samples are preserved",
			body: "```markdown\n> Attachment: **[Example](https://example.com)**\n> Status\n```\n\n" +
				"~~~~\n> Attachment: **Example**\n~~~~",
			want: "```markdown\n> Attachment: **[Example](https://example.com)**\n> Status\n```\n\n" +
				"~~~~\n> Attachment: **Example**\n~~~~",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := presentMessageBody(tc.body); got != tc.want {
				t.Fatalf("presentation body: got %q, want %q", got, tc.want)
			}
		})
	}
}
