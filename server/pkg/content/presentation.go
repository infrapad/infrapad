package content

// Presentation is the fixed, host-owned structured presentation of a record.
// The UI renders it with host components; Markdown is sanitized by the UI.
type Presentation struct {
	Cards []Card `json:"cards"`
}

// Card is a single presentation card.
type Card struct {
	Title    string  `json:"title,omitempty"`
	Markdown string  `json:"markdown,omitempty"`
	Fields   []Field `json:"fields,omitempty"`
	Links    []Link  `json:"links,omitempty"`
}

// Field is a labeled text value.
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Link is a labeled URL.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}
