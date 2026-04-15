package epic

import (
	"fmt"
	"strings"

	"github.com/yevgetman/fry/internal/config"
)

// EffortLevel controls sprint count, iteration budget, and review rigor.
type EffortLevel string

const (
	EffortFast     EffortLevel = "fast"
	EffortStandard EffortLevel = "standard"
	EffortHigh     EffortLevel = "high"
	EffortMax      EffortLevel = "max"
)

// ParseEffortLevel parses a string into an EffortLevel.
// Empty string is accepted and means auto-detect.
func ParseEffortLevel(s string) (EffortLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "fast":
		return EffortFast, nil
	case "standard":
		return EffortStandard, nil
	case "high":
		return EffortHigh, nil
	case "max":
		return EffortMax, nil
	case "":
		return "", nil // auto-detect
	default:
		return "", fmt.Errorf("invalid effort level %q: must be fast, standard, high, or max", s)
	}
}

// String returns the effort level as a display string. Empty means "auto".
func (e EffortLevel) String() string {
	if e == "" {
		return "auto"
	}
	return string(e)
}

// DefaultMaxIterations returns the default max_iterations for sprints
// at this effort level (used when @max_iterations is not set per-sprint).
func (e EffortLevel) DefaultMaxIterations() int {
	switch e {
	case EffortFast:
		return 12
	case EffortStandard:
		return 20
	case EffortHigh:
		return 25
	case EffortMax:
		return 40
	default:
		return 25 // default = high
	}
}

// MaxSprintCount returns the maximum number of sprints for this effort level.
func (e EffortLevel) MaxSprintCount() int {
	switch e {
	case EffortFast:
		return 2
	case EffortStandard:
		return 4
	case EffortHigh:
		return 10
	case EffortMax:
		return 10
	default:
		return 10
	}
}

// DefaultMaxHealAttempts returns the default number of alignment attempts for this
// effort level. Returns 0 for fast (no alignment) and max (unlimited/progress-based).
func (e EffortLevel) DefaultMaxHealAttempts() int {
	switch e {
	case EffortFast:
		return 0
	case EffortStandard:
		return config.DefaultMaxHealAttempts
	case EffortHigh:
		return config.HealAttemptsHigh
	case EffortMax:
		return 0 // unlimited, governed by progress detection
	default:
		return config.DefaultMaxHealAttempts // auto = standard default
	}
}

// DefaultMaxFailPercent returns the default sanity check failure threshold
// for this effort level.
func (e EffortLevel) DefaultMaxFailPercent() int {
	switch e {
	case EffortMax:
		return config.MaxFailPercentMax
	default:
		return config.DefaultMaxFailPercent
	}
}

// HealUsesProgressDetection returns whether this effort level uses
// progress-based alignment (exit early if stuck).
func (e EffortLevel) HealUsesProgressDetection() bool {
	return e == EffortHigh || e == EffortMax
}

// HealStuckThreshold returns the number of consecutive no-progress alignment
// attempts before the loop exits. Only meaningful when HealUsesProgressDetection
// returns true.
func (e EffortLevel) HealStuckThreshold() int {
	switch e {
	case EffortHigh:
		return config.HealStuckThresholdHigh
	case EffortMax:
		return config.HealStuckThresholdMax
	default:
		return 0
	}
}

// HealHasHardCap returns whether this effort level has a fixed upper bound on
// alignment attempts. Max effort is unlimited (progress-based), all others are capped.
func (e EffortLevel) HealHasHardCap() bool {
	return e != EffortMax
}

// deviationScopeUnlimited returns whether this effort level allows deviations
// to touch any remaining sprint in the epic (up to the safety cap).
// All effort levels except fast expand deviation scope to cover remaining sprints.
func (e EffortLevel) deviationScopeUnlimited() bool {
	return e != EffortFast
}

type Epic struct {
	Name                 string
	Engine               string
	EffortLevel          EffortLevel
	DockerFromSprint     int
	DockerReadyCmd       string
	DockerReadyTimeout   int
	RequiredTools        []string
	PreflightCmds        []string
	PreSprintCmd         string
	PreIterationCmd      string
	AgentModel           string
	AgentFlags           string
	MCPConfig            string
	VerificationFile     string
	MaxHealAttempts      int
	MaxHealAttemptsSet   bool
	MaxFailPercent       int
	MaxFailPercentSet    bool
	CompactWithAgent     bool
	ReviewBetweenSprints bool
	ReviewEngine          string
	ReviewModel           string
	ReviewAfterSprint     bool
	MaxReviewIterations   int
	MaxReviewIterationsSet bool
	MaxDeviationScope    int
	AuditAfterSprint     bool
	MaxAuditIterations    int
	MaxAuditIterationsSet bool
	AuditEngine          string
	AuditModel           string
	Sprints              []Sprint
	TotalSprints         int
}

type Sprint struct {
	Number          int
	Name            string
	MaxIterations   int
	Promise         string
	MaxHealAttempts *int
	Prompt          string
}
