package markdown

// Canonical namespaced content type names of the built-in block types.
// They mirror the server-side extension type names; the CLI keeps local
// constants to stay independent of the server implementation packages.
const (
	// MarkdownTypeName is the canonical name of the editable Markdown type.
	MarkdownTypeName = "Core/Markdown"
	// AlertsMatcherTypeName is the canonical name of the alert-matcher type.
	AlertsMatcherTypeName = "Prometheus/AlertsMatcher"
)
