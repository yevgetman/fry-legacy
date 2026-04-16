package codereview

import (
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
	tokenmetrics "github.com/yevgetman/fry/internal/metrics"
)

// Finding represents a single structured code review finding.
type Finding struct {
	Location       string
	Description    string
	Severity       string
	Category       string
	RecommendedFix string
	AffectedFiles  []string
}

// ReviewOpts configures a single sprint code review.
type ReviewOpts struct {
	ProjectDir string
	Sprint     *epic.Sprint
	Epic       *epic.Epic
	Engine     engine.Engine
	Complexity ComplexityTier
	GitDiff    string
	DiffFn     func() (string, error)
	ProgressFn func(ReviewProgress)
	Mode       string
}

// ConvergenceStatus indicates whether the agent's review loop converged or hit
// the iteration limit.
type ConvergenceStatus string

const (
	ConvergenceConverged      ConvergenceStatus = "CONVERGED"
	ConvergenceIterationLimit ConvergenceStatus = "ITERATION_LIMIT"
)

// ReviewPass captures what happened during a single review pass.
type ReviewPass struct {
	PassNumber  int            `json:"pass_number"`
	FoundCounts map[string]int `json:"found_counts"` // severity -> count found this pass
	FixedCounts map[string]int `json:"fixed_counts"` // severity -> count fixed this pass
}

// ReviewMetadata is the structured metadata the agent writes to the review
// output file. It is nil when the agent omits the metadata section.
type ReviewMetadata struct {
	IterationsCompleted int               `json:"iterations_completed"`
	Convergence         ConvergenceStatus `json:"convergence"`
	History             []ReviewPass      `json:"history,omitempty"`
}

// ReviewResult is the outcome of RunCodeReview.
type ReviewResult struct {
	Passed          bool
	Blocking        bool              // true when CRITICAL or HIGH issues remain
	Iterations      int               // agent-reported iteration count, falls back to 1 if metadata absent
	ConvergedCleanly bool             // true if the agent reported CONVERGED status
	MaxSeverity     string            // highest severity among remaining findings
	SeverityCounts  map[string]int    // count of findings per severity level
	Findings        []Finding         // remaining findings after review (deduplicated)
	Complexity      ComplexityTier
	Metrics         *ReviewMetrics
	Metadata        *ReviewMetadata   // parsed from agent output, nil if absent
	Recovered       bool              // true if review output was recovered from transcript
}

// ReviewProgress describes the live state of a sprint review for status updates.
type ReviewProgress struct {
	Stage      string
	Findings   map[string]int
	Complexity ComplexityTier
}

// CallMetric captures the observable outcome of one review call.
type CallMetric struct {
	SessionType engine.SessionType      `json:"session_type"`
	PromptBytes int                     `json:"prompt_bytes"`
	OutputBytes int                     `json:"output_bytes"`
	DurationMs  int64                   `json:"duration_ms"`
	Model       string                  `json:"model,omitempty"`
	Tokens      tokenmetrics.TokenUsage `json:"tokens"`
}

// ReviewMetrics accumulates telemetry for one RunCodeReview invocation.
type ReviewMetrics struct {
	Call              *CallMetric    `json:"call,omitempty"`
	ContentComplexity ComplexityTier `json:"content_complexity,omitempty"`
	FinalFindingCount int            `json:"final_finding_count"`
}

func (m *ReviewMetrics) TotalCalls() int {
	if m == nil || m.Call == nil {
		return 0
	}
	return 1
}

func (m *ReviewMetrics) TotalDurationMs() int64 {
	if m == nil || m.Call == nil {
		return 0
	}
	return m.Call.DurationMs
}
