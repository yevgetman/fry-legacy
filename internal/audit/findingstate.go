package audit

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type findingClassification struct {
	Resolved    []Finding
	Persisting  []Finding
	NewFindings []Finding
}

func decorateFindings(findings []Finding, cycle int) []Finding {
	if len(findings) == 0 {
		return findings
	}
	decorated := make([]Finding, len(findings))
	for i := range findings {
		decorated[i] = findings[i]
		decorated[i].Category = decorated[i].categoryOrDefault()
		if decorated[i].OriginCycle == 0 {
			decorated[i].OriginCycle = cycle
		}
		decorated[i].LastSeenCycle = cycle
		decorated[i].AffectedFiles = targetFilesForFinding(decorated[i])
	}
	return decorated
}

func targetFilesForFinding(f Finding) []string {
	target := parseFindingTarget(f.Location)
	if target.Path == "" {
		return nil
	}
	return []string{target.Path}
}

type findingTargetRef struct {
	Path string
	Line int
}

// parseFindingTarget extracts a file path (and optional line number) from a
// finding's Location field. Audit agents emit locations in many shapes:
//
//	apps/web/package.json
//	apps/web/package.json:14
//	.github/workflows/ci.yml:25-28
//	src/util.go:42,55-60
//	Sprint diff, apps/web entry (mode 160000)   ← descriptive prose, not a path
//
// The parser strips trailing line numbers and line ranges, then validates that
// the residual looks like a real file path. If it does not (contains spaces,
// parentheses, or commas — none of which appear in legitimate POSIX paths fry
// would build against), the function returns an empty ref so callers don't try
// to read a phantom file. The previous implementation only stripped a single
// trailing integer, leaving `file.go:25-28` mangled and `Sprint diff,
// apps/web entry (mode 160000)` accepted as a literal path — both of which
// produced "cannot inline target file" warnings during the audit fix loop.
func parseFindingTarget(location string) findingTargetRef {
	location = strings.TrimSpace(location)
	if location == "" {
		return findingTargetRef{}
	}

	parts := strings.Split(location, ":")
	ref := findingTargetRef{}
	end := len(parts)

	// Strip trailing line-number-like suffixes. Accepts plain integers (`25`),
	// ranges (`25-28`), and comma-separated lists (`25,30-35`).
	for end > 0 {
		last := strings.TrimSpace(parts[end-1])
		if !looksLikeLineRef(last) {
			break
		}
		// Capture the first integer we see as the canonical Line, but only
		// once — we don't want a deeper number to override a shallower one.
		if ref.Line == 0 {
			if n := firstInt(last); n > 0 {
				ref.Line = n
			}
		}
		end--
	}

	path := strings.TrimSpace(strings.Join(parts[:end], ":"))
	if path == "" {
		return findingTargetRef{}
	}
	if !looksLikeFilePath(path) {
		// Descriptive prose in the Location field. Skip — the fix prompt
		// will still include the finding's description and recommended fix,
		// just not an inlined file copy.
		return findingTargetRef{}
	}
	ref.Path = filepath.Clean(path)
	return ref
}

// looksLikeLineRef returns true if s is a single integer, a range like
// "25-28" or "25–28" (en-dash), or a comma-separated list of either.
func looksLikeLineRef(s string) bool {
	if s == "" {
		return false
	}
	for _, group := range strings.Split(s, ",") {
		group = strings.TrimSpace(group)
		if group == "" {
			return false
		}
		// Normalize en-dash to hyphen for the dash-split.
		group = strings.ReplaceAll(group, "\u2013", "-")
		segments := strings.Split(group, "-")
		if len(segments) > 2 {
			return false
		}
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				return false
			}
			if _, err := strconv.Atoi(seg); err != nil {
				return false
			}
		}
	}
	return true
}

// firstInt returns the first integer parsed out of a line-ref string.
// Returns 0 if no integer is present.
func firstInt(s string) int {
	s = strings.ReplaceAll(s, "\u2013", "-")
	for _, group := range strings.Split(s, ",") {
		group = strings.TrimSpace(group)
		for _, seg := range strings.Split(group, "-") {
			seg = strings.TrimSpace(seg)
			if n, err := strconv.Atoi(seg); err == nil {
				return n
			}
		}
	}
	return 0
}

// looksLikeFilePath returns true if s is plausibly a POSIX file path. It
// rejects strings containing spaces, parentheses, or commas — characters
// that don't appear in the kinds of paths fry builds against and reliably
// distinguish prose ("Sprint diff, apps/web entry (mode 160000)") from real
// targets (".github/workflows/ci.yml").
func looksLikeFilePath(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r == '(', r == ')', r == ',', r == '"', r == '\'':
			return false
		case unicode.IsSpace(r):
			return false
		}
	}
	return true
}

func classifyFindings(known, current []Finding) findingClassification {
	if len(known) == 0 {
		return findingClassification{NewFindings: append([]Finding(nil), current...)}
	}

	var result findingClassification
	currentUsed := make([]bool, len(current))

	for _, knownFinding := range known {
		if idx := findExactCurrentFinding(knownFinding, current, currentUsed); idx >= 0 {
			currentUsed[idx] = true
			result.Persisting = append(result.Persisting, mergeExactPersistingFinding(knownFinding, current[idx]))
			continue
		}
		result.Resolved = append(result.Resolved, knownFinding)
	}

	seenNew := make(map[string]struct{})
	for i, currentFinding := range current {
		if currentUsed[i] {
			continue
		}
		key := currentFinding.key()
		if _, ok := seenNew[key]; ok {
			continue
		}
		seenNew[key] = struct{}{}
		result.NewFindings = append(result.NewFindings, currentFinding)
	}

	return result
}

func findExactCurrentFinding(known Finding, current []Finding, used []bool) int {
	knownKey := known.key()
	for i, currentFinding := range current {
		if used[i] {
			continue
		}
		if currentFinding.key() == knownKey {
			return i
		}
	}
	return -1
}

func mergeExactPersistingFinding(previous, current Finding) Finding {
	merged := current
	merged.OriginCycle = previous.OriginCycle
	merged.LastSeenCycle = current.LastSeenCycle
	if len(merged.AffectedFiles) == 0 {
		merged.AffectedFiles = append([]string(nil), previous.AffectedFiles...)
	}
	return merged
}
