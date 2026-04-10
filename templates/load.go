package templates

import (
	"fmt"
	"io/fs"
	"strings"
)

// LoadText reads an embedded template file and returns its content as a
// trimmed string. This is the public counterpart of the unexported
// loadTemplateText helper in internal/copilot/prompt.go — exposed here
// so any package can load invocation prompts without duplicating the
// pattern.
func LoadText(path string) (string, error) {
	data, err := fs.ReadFile(TemplateFS, path)
	if err != nil {
		return "", fmt.Errorf("load embedded template %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}
