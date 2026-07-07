package chaosbackend

import _ "embed"

//go:embed template.html
var defaultTemplateHTML string

// Config configures a Server.
type Config struct {
	// TemplateFile is a path to an external HTML template for "/".
	// If empty, an embedded default template is used.
	TemplateFile string
}
