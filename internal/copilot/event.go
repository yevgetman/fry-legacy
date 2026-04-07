package copilot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/observer"
)

// Event mirrors observer.Event so the copilot package has its own type
// surface, but it serializes identically. EmitEvent always writes to BOTH
// the copilot-local stream (.fry/copilot/events.jsonl) AND the canonical
// observer stream (.fry/observer/events.jsonl) so that `fry monitor` and
// `fry events --follow` see copilot activity natively.
type Event struct {
	Timestamp string            `json:"ts"`
	Type      observer.EventType `json:"type"`
	Sprint    int               `json:"sprint,omitempty"`
	Data      map[string]string `json:"data,omitempty"`
}

// EmitEvent writes evt to .fry/copilot/events.jsonl AND mirrors it into the
// canonical observer event stream so fry monitor / fry events tooling sees
// it. Errors writing to either stream are returned but the function tries
// hard to deliver to both — a failure on the copilot stream does not stop
// it from also attempting the observer mirror.
func EmitEvent(projectDir string, evt Event) error {
	if evt.Timestamp == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	// 1. Write to copilot-local stream.
	copilotErr := appendJSONLEvent(projectDir, config.CopilotEventsJSONLFile, evt)

	// 2. Mirror to observer stream so monitor/events tooling sees it.
	mirrorEvent := observer.Event{
		Timestamp: evt.Timestamp,
		Type:      evt.Type,
		Sprint:    evt.Sprint,
		Data:      evt.Data,
	}
	mirrorErr := observer.EmitEvent(projectDir, mirrorEvent)

	if copilotErr != nil {
		return copilotErr
	}
	return mirrorErr
}

// appendJSONLEvent serialises evt and appends it as a JSONL line to the
// file at projectDir/relPath. Creates parent directories as needed.
func appendJSONLEvent(projectDir, relPath string, evt Event) error {
	dir := filepath.Join(projectDir, config.CopilotDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("emit copilot event: create dir: %w", err)
	}
	line, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("emit copilot event: marshal: %w", err)
	}
	line = append(line, '\n')
	full := filepath.Join(projectDir, relPath)
	f, err := os.OpenFile(full, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("emit copilot event: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("emit copilot event: write: %w", err)
	}
	return nil
}

// AppendEventsText appends a human-readable line to .fry/copilot/events.txt.
// This is the meetingly3-style narrative log that the copilot agent writes
// in a free-form way; this helper exists so fry's main process can also
// append to it (e.g., on bootstrap).
func AppendEventsText(projectDir, line string) error {
	dir := filepath.Join(projectDir, config.CopilotDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("append events.txt: create dir: %w", err)
	}
	full := filepath.Join(projectDir, config.CopilotEventsTextFile)
	f, err := os.OpenFile(full, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("append events.txt: open: %w", err)
	}
	defer f.Close()
	if len(line) == 0 || line[len(line)-1] != '\n' {
		line += "\n"
	}
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("append events.txt: write: %w", err)
	}
	return nil
}

// ReadEvents reads all events from .fry/copilot/events.jsonl. Returns
// (nil, nil) if the file does not exist.
func ReadEvents(projectDir string) ([]Event, error) {
	path := filepath.Join(projectDir, config.CopilotEventsJSONLFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read copilot events: open: %w", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	// Allow long event lines (data payloads with paths can be large).
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("read copilot events: parse line: %w", err)
		}
		events = append(events, evt)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read copilot events: scan: %w", err)
	}
	return events, nil
}

// CountEventsByType returns a map of event type → count for the given event
// list. Convenience helper used by `fry copilot status` to render counts.
func CountEventsByType(events []Event) map[observer.EventType]int {
	counts := make(map[observer.EventType]int)
	for _, e := range events {
		counts[e.Type]++
	}
	return counts
}
