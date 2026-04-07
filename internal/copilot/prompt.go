package copilot

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/templates"
)

const (
	templateIdentityPath  = "copilot/identity.md"
	templateAuthorityPath = "copilot/authority.md"
	templateBootstrapPath = "copilot/bootstrap.md"
	templateSummaryPath   = "copilot/summary.md"
)

// BootstrapData is the rendering context for templates/copilot/bootstrap.md.
// All fields are required — there are no defaults. Templates use these to
// substitute build-specific values into the prompt.
type BootstrapData struct {
	Identity        string // rendered identity layer (templates/copilot/identity.md)
	Authority       string // rendered authority layer (templates/copilot/authority.md)
	BuildDir        string
	FrySourceDir    string
	Engine          string
	EpicName        string
	EffortLevel     string
	TotalSprints    int
	StartedAt       string
	Interval        string // human form, e.g. "10m"
	IntervalMinutes int
	SessionID       string
	NowISO          string
	RunID           string
}

// SummaryData is the rendering context for templates/copilot/summary.md.
// Used in the rare case the session is restarted from scratch and needs
// to recover its state from disk before writing the final summary.
type SummaryData struct {
	Identity     string
	BuildDir     string
	FrySourceDir string
	StartedAt    string
	Outcome      string
	NowISO       string
}

// loadTemplateText reads a template file from the embedded TemplateFS.
// Returns the file's content as a string. Errors include the path so
// callers can diagnose missing template issues quickly.
func loadTemplateText(path string) (string, error) {
	data, err := fs.ReadFile(templates.TemplateFS, path)
	if err != nil {
		return "", fmt.Errorf("load embedded template %s: %w", path, err)
	}
	return string(data), nil
}

// RenderBootstrapPrompt renders the full bootstrap prompt by:
//
//  1. Loading identity.md and authority.md from embedded templates and
//     injecting them into the BootstrapData as the .Identity and
//     .Authority fields.
//  2. Loading bootstrap.md as a Go text/template.
//  3. Executing the template with the prepared BootstrapData.
//
// The result is the full text the bootstrap subprocess will receive as
// its initial prompt.
//
// If data.NowISO is empty, it is populated with the current UTC time.
// If data.IntervalMinutes is zero but data.Interval parses as a duration,
// it is set from the duration.
func RenderBootstrapPrompt(data BootstrapData) (string, error) {
	// Load identity + authority layers if not pre-populated.
	if data.Identity == "" {
		text, err := loadTemplateText(templateIdentityPath)
		if err != nil {
			return "", err
		}
		data.Identity = text
	}
	if data.Authority == "" {
		text, err := loadTemplateText(templateAuthorityPath)
		if err != nil {
			return "", err
		}
		data.Authority = text
	}
	if data.NowISO == "" {
		data.NowISO = time.Now().UTC().Format(time.RFC3339)
	}
	if data.IntervalMinutes == 0 && data.Interval != "" {
		if d, err := time.ParseDuration(data.Interval); err == nil {
			data.IntervalMinutes = int(d.Minutes())
		}
	}

	tmplText, err := loadTemplateText(templateBootstrapPath)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("copilot-bootstrap").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("parse bootstrap template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute bootstrap template: %w", err)
	}
	return buf.String(), nil
}

// RenderSummaryPrompt renders the (rarely-used) recovery summary prompt.
// Used only when a copilot session has been restarted from scratch and
// needs to reconstruct its view of the build before writing the final
// summary file.
func RenderSummaryPrompt(data SummaryData) (string, error) {
	if data.Identity == "" {
		text, err := loadTemplateText(templateIdentityPath)
		if err != nil {
			return "", err
		}
		data.Identity = text
	}
	if data.NowISO == "" {
		data.NowISO = time.Now().UTC().Format(time.RFC3339)
	}

	tmplText, err := loadTemplateText(templateSummaryPath)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("copilot-summary").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("parse summary template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute summary template: %w", err)
	}
	return buf.String(), nil
}

// WriteBootstrapPromptFile renders the bootstrap prompt and writes it to
// .fry/copilot/prompts/bootstrap.md under projectDir. The bootstrap
// subprocess reads this file by path so the agent can reference its own
// prompt during reasoning.
func WriteBootstrapPromptFile(projectDir string, data BootstrapData) (string, error) {
	prompt, err := RenderBootstrapPrompt(data)
	if err != nil {
		return "", err
	}
	path := filepath.Join(projectDir, config.CopilotBootstrapPromptFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write bootstrap prompt: create dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(prompt), 0o644); err != nil {
		return "", fmt.Errorf("write bootstrap prompt: %w", err)
	}
	return prompt, nil
}

// WriteSummaryPromptFile renders the summary prompt and writes it to
// .fry/copilot/prompts/summary.md under projectDir.
func WriteSummaryPromptFile(projectDir string, data SummaryData) (string, error) {
	prompt, err := RenderSummaryPrompt(data)
	if err != nil {
		return "", err
	}
	path := filepath.Join(projectDir, config.CopilotSummaryPromptFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write summary prompt: create dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(prompt), 0o644); err != nil {
		return "", fmt.Errorf("write summary prompt: %w", err)
	}
	return prompt, nil
}
