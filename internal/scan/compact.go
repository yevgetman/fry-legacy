package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
)

// CompactMemories reduces the memory count when it exceeds MaxMemoryCount.
// Sends all memories to a cheap LLM to merge redundant ones and drop
// low-confidence unconfirmed entries. Target count: CompactedMemoryCount.
func CompactMemories(ctx context.Context, projectDir string, eng engine.Engine, model, effortLevel string) error {
	if eng == nil {
		return fmt.Errorf("compact memories: engine is required")
	}

	memoriesDir := filepath.Join(projectDir, config.CodebaseMemoriesDir)
	memories, err := LoadMemories(memoriesDir)
	if err != nil {
		return fmt.Errorf("compact memories: load: %w", err)
	}

	if len(memories) <= config.MaxMemoryCount {
		return nil // No compaction needed.
	}

	prompt := buildCompactionPrompt(memories)

	output, _, runErr := eng.Run(ctx, prompt, engine.RunOpts{
		Model:       model,
		SessionType: engine.SessionCodebaseMemory,
		EffortLevel: effortLevel,
		WorkDir:     projectDir,
	})
	if runErr != nil && strings.TrimSpace(output) == "" {
		return fmt.Errorf("compact memories: engine run: %w", runErr)
	}

	compacted := parseMemoryOutput(output, "compaction", 0)
	if len(compacted) == 0 {
		return fmt.Errorf("compact memories: LLM produced no output")
	}
	// Enforce target count in case the LLM output exceeds it.
	if len(compacted) > config.CompactedMemoryCount {
		compacted = compacted[:config.CompactedMemoryCount]
	}

	// Preserve reinforcement counts from originals where possible.
	for i := range compacted {
		if idx := findDuplicate(memories, compacted[i].Body); idx >= 0 {
			compacted[i].Reinforced = memories[idx].Reinforced
			compacted[i].Date = memories[idx].Date
			compacted[i].Source = memories[idx].Source
			compacted[i].Sprint = memories[idx].Sprint
		}
	}

	// Remove all existing memory files.
	entries, err := os.ReadDir(memoriesDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("compact memories: list dir: %w", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			_ = os.Remove(filepath.Join(memoriesDir, e.Name()))
		}
	}

	// Write compacted set with fresh numbering.
	for i, m := range compacted {
		if m.Date == "" {
			m.Date = "compacted"
		}
		if m.Source == "" {
			m.Source = "compaction"
		}
		m.Filename = fmt.Sprintf("%03d-%s-%s.md", i+1, m.Date, slugify(m.Body, 40))
		if writeErr := writeMemoryFile(filepath.Join(memoriesDir, m.Filename), m); writeErr != nil {
			continue
		}
	}

	return nil
}

// NeedsCompaction returns true if the memory count exceeds the threshold.
func NeedsCompaction(projectDir string) bool {
	memoriesDir := filepath.Join(projectDir, config.CodebaseMemoriesDir)
	memories, err := LoadMemories(memoriesDir)
	if err != nil {
		return false
	}
	return len(memories) > config.MaxMemoryCount
}

func buildCompactionPrompt(memories []Memory) string {
	var b strings.Builder

	b.WriteString("# Memory Compaction\n\n")
	b.WriteString(fmt.Sprintf("You have %d codebase memories. Compact them to the %d most valuable.\n\n", len(memories), config.CompactedMemoryCount))

	b.WriteString("## Rules\n\n")
	b.WriteString("1. Merge redundant memories that say the same thing differently\n")
	b.WriteString("2. Keep high-confidence and reinforced memories preferentially\n")
	b.WriteString("3. Drop low-confidence memories that were never reinforced (reinforced: 0)\n")
	b.WriteString("4. Preserve the original wording where possible — don't paraphrase unnecessarily\n")
	b.WriteString("5. When merging, pick the more specific/detailed version\n")
	b.WriteString(fmt.Sprintf("6. Output exactly %d memories (or fewer if the originals are very redundant)\n\n", config.CompactedMemoryCount))

	b.WriteString("## Current Memories\n\n")
	for i, m := range memories {
		b.WriteString(fmt.Sprintf("### Memory %d [confidence: %s, reinforced: %d]\n", i+1, m.Confidence, m.Reinforced))
		b.WriteString(m.Body)
		b.WriteString("\n\n")
	}

	b.WriteString("## Output Format\n\n")
	b.WriteString("Output each surviving memory using this exact format:\n\n")
	b.WriteString("```\n")
	b.WriteString("MEMORY: <confidence: high|medium|low>\n")
	b.WriteString("<the learning>\n")
	b.WriteString("---\n")
	b.WriteString("```\n\n")
	b.WriteString("Output ONLY these blocks. No preamble, no summary.\n")

	return b.String()
}
