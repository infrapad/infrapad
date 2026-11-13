package slack

import (
	"testing"

	"github.com/infrapad/infrapad/server/internal/utils"
)

func TestNormalizeSlackText(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{
			name: "agent guidance with Slack bullets and numbered steps",
			input: utils.Dedent(`
				_Switched tier._

				*Two important distinctions:*
				• Check ` + "`compliance-monkey`" + ` first.
				• Infrastructure uses *subnet pair and size*.

				*Next evidence:*
				1. *Confirm scope:* Check the cluster.
				2. *Trace ` + "`serving-42`" + `:* Inspect labels.
			`),
			want: utils.Dedent(`
				_Switched tier._

				**Two important distinctions:**
				- Check ` + "`compliance-monkey`" + ` first.
				- Infrastructure uses **subnet pair and size**.

				**Next evidence:**
				1. **Confirm scope:** Check the cluster.
				2. **Trace ` + "`serving-42`" + `:** Inspect labels.
			`),
		},
		{"hard breaks", "first\nsecond\n\nthird\nfourth", "first  \nsecond\n\nthird  \nfourth"},
		{"CRLF", "first\r\nsecond", "first  \nsecond"},
		{"existing hard break", "first  \nsecond", "first  \nsecond"},
		{"nested bullets", "• outer\n  • inner\n  • other\n• last", "- outer\n  - inner\n  - other\n- last"},
		{"list continuation", "• first\n  continued\n• second", "- first  \n  continued\n- second"},
		{"quotes", "> first\n> second", "> first  \n> second"},
		{"emphasis", "*bold* _italic_ **already bold** *more*", "**bold** _italic_ **already bold** **more**"},
		{"literal stars", "* unmatched\n2*3*4 and *.go", "* unmatched  \n2*3*4 and *.go"},
		{"asterisk list", "* *bold*\n* next", "* **bold**\n* next"},
		{"Unicode bold", "*Příliš žluťoučký*", "**Příliš žluťoučký**"},
		{"code span", "*Run `*literal*` now* and `<https://example.com|raw>`", "**Run `*literal*` now** and `<https://example.com|raw>`"},
		{"Slack link", "*See* <https://example.com/a*b*c|the doc> &amp; <https://example.com>", "**See** [the doc](https://example.com/a*b*c) & <https://example.com>"},
		{"Markdown link", "[doc](https://example.com/a*b*c) *next*", "[doc](https://example.com/a*b*c) **next**"},
		{"bare URL", "https://example.com/a*b*c *next*", "https://example.com/a*b*c **next**"},
		{"Slack escapes", "&lt;tag&gt; &amp; &amp;lt;", "<tag> & &lt;"},
		{"inline fences", "before```*literal*\n• not a list\n<https://example.com|raw>```after\nlast", "before\n```\n*literal*\n• not a list\n<https://example.com|raw>\n```\nafter  \nlast"},
		{"indented code", "    *literal*\n    • not a list\n    <https://example.com|raw>", "    *literal*\n    • not a list\n    <https://example.com|raw>"},
		{"unclosed fence", "```\n*literal*\n• not a list", "```\n*literal*\n• not a list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSlackText(tc.input); got != tc.want {
				t.Errorf("normalizeSlackText() = %q, want %q", got, tc.want)
			}
		})
	}
}
