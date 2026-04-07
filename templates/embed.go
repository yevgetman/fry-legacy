package templates

import "embed"

// TemplateFS embeds the markdown reference files shipped with fry,
// including the identity layer files under identity/ and the copilot
// prompt templates under copilot/.
//
//go:embed *.md identity/*.md identity/identity.json copilot/*.md
var TemplateFS embed.FS
