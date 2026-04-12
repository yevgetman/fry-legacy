package engine

import (
	"fmt"
	"strings"
)

// Model represents a valid model identifier with its capability ranking.
// Rank 1 is the most capable; higher numbers are less capable.
// Models with the same rank are considered equivalent in capability.
type Model struct {
	ID   string
	Rank int
}

// ModelTier represents a capability tier used for automatic model selection.
// The tier maps to a concrete model ID per engine via the tier mapping tables.
// To update models when new ones release, change the mapping tables only.
type ModelTier string

const (
	TierFrontier ModelTier = "frontier" // Most capable, highest cost
	TierStandard ModelTier = "standard" // Strong all-rounder
	TierMini     ModelTier = "mini"     // Fast, cost-efficient
	TierLabor    ModelTier = "labor"    // Cheapest, no reasoning needed
)

// SessionType identifies the kind of agent session being launched.
type SessionType string

const (
	SessionSprint            SessionType = "sprint"
	SessionHeal              SessionType = "heal"
	SessionAudit             SessionType = "audit"
	SessionAuditFix          SessionType = "audit-fix"
	SessionAuditVerify       SessionType = "audit-verify"
	SessionReview            SessionType = "review"
	SessionReplan            SessionType = "replan"
	SessionBuildAudit        SessionType = "build-audit"
	SessionBuildSummary      SessionType = "build-summary"
	SessionCompaction        SessionType = "compaction"
	SessionContinue          SessionType = "continue"
	SessionProjectOverview   SessionType = "project-overview"
	SessionPrepare           SessionType = "prepare"
	SessionTriage            SessionType = "triage"
	SessionObserver          SessionType = "observer"
	SessionExperienceSummary SessionType = "experience-summary"
	SessionCodebaseScan      SessionType = "codebase-scan"
	SessionCodebaseMemory    SessionType = "codebase-memory"
	SessionCopilot           SessionType = "copilot"
)

// Tier-to-model mapping tables.
// These are the ONLY places where concrete model IDs are associated with tiers.
// When a new frontier model is released, update these tables and all session
// rules automatically pick up the change.
var claudeTierModels = map[ModelTier]string{
	TierFrontier: "opus[1m]",
	TierStandard: "sonnet",
	TierMini:     "haiku",
	TierLabor:    "haiku",
}

var codexTierModels = map[ModelTier]string{
	TierFrontier: "gpt-5.4",
	TierStandard: "gpt-5.3-codex",
	TierMini:     "gpt-5.4-mini",
	TierLabor:    "gpt-5-codex-mini",
}

var ollamaTierModels = map[ModelTier]string{
	TierFrontier: "llama3",
	TierStandard: "llama3",
	TierMini:     "llama3",
	TierLabor:    "llama3",
}

// ClaudeModels lists valid model identifiers for the Claude engine, ordered by capability.
var ClaudeModels = []Model{
	// Rank 1 — Frontier (opus-class, current generation)
	{ID: "claude-opus-4-6", Rank: 1},
	{ID: "opus", Rank: 1},
	{ID: "opus[1m]", Rank: 1},

	// Rank 2 — Strong (sonnet-class, current generation)
	{ID: "claude-sonnet-4-6", Rank: 2},
	{ID: "sonnet", Rank: 2},
	{ID: "sonnet[1m]", Rank: 2},
	{ID: "default", Rank: 2},
	{ID: "opusplan", Rank: 2},

	// Rank 3 — Previous-gen frontier
	{ID: "claude-3-opus-20240229", Rank: 3},

	// Rank 4 — Previous-gen strong
	{ID: "claude-3-5-sonnet-20241022", Rank: 4},

	// Rank 5 — Fast (haiku-class, current generation)
	{ID: "claude-haiku-4-5-20251001", Rank: 5},
	{ID: "haiku", Rank: 5},

	// Rank 6 — Previous-gen fast
	{ID: "claude-3-5-haiku-20241022", Rank: 6},

	// Rank 7 — Legacy
	{ID: "claude-3-sonnet-20240229", Rank: 7},
	{ID: "claude-3-haiku-20240307", Rank: 8},
}

// CodexModels lists valid model identifiers for the Codex engine, ordered by capability.
var CodexModels = []Model{
	// Rank 1 — Latest frontier
	{ID: "gpt-5.4", Rank: 1},

	// Rank 2 — Frontier codex-optimized
	{ID: "gpt-5.3-codex", Rank: 2},

	// Ranks 3-4 — Previous frontier
	{ID: "gpt-5.2-codex", Rank: 3},
	{ID: "gpt-5.2", Rank: 4},

	// Ranks 5-7 — Deep reasoning
	{ID: "gpt-5.1-codex-max", Rank: 5},
	{ID: "gpt-5.1-codex", Rank: 6},
	{ID: "gpt-5.1", Rank: 7},

	// Rank 8 — Current-gen mini (fast, efficient)
	{ID: "gpt-5.4-mini", Rank: 8},

	// Ranks 9-10 — Older generation
	{ID: "gpt-5-codex", Rank: 9},
	{ID: "gpt-5", Rank: 10},

	// Ranks 11-12 — Legacy mini (speed/cost optimized)
	{ID: "gpt-5.1-codex-mini", Rank: 11},
	{ID: "gpt-5-codex-mini", Rank: 12},
}

// OllamaModels lists well-known model identifiers for the Ollama engine.
// Ollama accepts any locally pulled model name; this list is not exhaustive.
var OllamaModels = []Model{
	{ID: "llama3", Rank: 1},
	{ID: "llama3:70b", Rank: 1},
	{ID: "mistral", Rank: 2},
	{ID: "codellama", Rank: 2},
	{ID: "phi3", Rank: 3},
	{ID: "gemma", Rank: 3},
}

// claudeModelSet, codexModelSet, and ollamaModelSet are lookup maps for validation.
var (
	claudeModelSet = buildModelSet(ClaudeModels)
	codexModelSet  = buildModelSet(CodexModels)
	ollamaModelSet = buildModelSet(OllamaModels)
)

func buildModelSet(models []Model) map[string]int {
	m := make(map[string]int, len(models))
	for _, model := range models {
		m[model.ID] = model.Rank
	}
	return m
}

// IsModelValidForEngine reports whether model is recognised for the given
// engine. Ollama accepts any non-empty model name because its model list is not
// statically enumerable.
func IsModelValidForEngine(engineName, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	switch engineName {
	case "claude":
		_, ok := claudeModelSet[model]
		return ok
	case "codex":
		_, ok := codexModelSet[model]
		return ok
	case "ollama":
		return true
	default:
		return false
	}
}

// TierModel returns the concrete model ID for a tier and engine.
func TierModel(engineName string, tier ModelTier) string {
	switch engineName {
	case "claude":
		return claudeTierModels[tier]
	case "codex":
		return codexTierModels[tier]
	case "ollama":
		return ollamaTierModels[tier]
	default:
		return ""
	}
}

// TierForSession returns the model tier for a given session type, engine, and effort level.
// Empty effort is treated as "standard".
func TierForSession(engineName, effort string, session SessionType) ModelTier {
	e := normalizeEffort(effort)

	switch session {
	case SessionSprint, SessionHeal, SessionAuditFix, SessionReview, SessionReplan:
		if e == "high" || e == "max" {
			return TierFrontier
		}
		return TierStandard

	case SessionAudit, SessionAuditVerify, SessionBuildAudit:
		return auditTier(engineName, e)

	case SessionBuildSummary:
		if e == "high" || e == "max" {
			return TierStandard
		}
		return TierMini

	case SessionCompaction:
		if e == "max" {
			return TierStandard
		}
		return TierLabor

	case SessionContinue:
		if e == "high" || e == "max" {
			return TierStandard
		}
		return TierMini

	case SessionProjectOverview:
		return TierLabor

	case SessionPrepare:
		if e == "max" {
			return TierFrontier
		}
		return TierStandard

	case SessionTriage:
		return TierMini

	case SessionObserver:
		if e == "high" || e == "max" {
			return TierStandard
		}
		return TierMini

	case SessionExperienceSummary:
		return TierMini

	case SessionCodebaseScan:
		return TierStandard

	case SessionCodebaseMemory:
		return TierStandard

	case SessionCopilot:
		// Copilot is a high-stakes intervention session: it can edit fry
		// source, run tests, commit, push, and restart builds. Use a strong
		// model at high/max effort, standard at lower effort levels.
		if e == "high" || e == "max" {
			return TierFrontier
		}
		return TierStandard

	default:
		return TierStandard
	}
}

func auditTier(engineName, effort string) ModelTier {
	if engineName == "codex" {
		switch effort {
		case "fast":
			return TierMini
		case "standard":
			return TierStandard
		default: // high, max
			return TierFrontier
		}
	}
	// claude: frontier at high/max so the reviewer is at least as capable
	// as the sprint builder; standard otherwise
	if effort == "high" || effort == "max" {
		return TierFrontier
	}
	return TierStandard
}

func normalizeEffort(effort string) string {
	e := strings.ToLower(strings.TrimSpace(effort))
	if e == "" {
		return "standard"
	}
	return e
}

// ResolveModelForSession returns the concrete model for a session based on
// engine, effort level, and session type, using the tier system.
func ResolveModelForSession(engineName, effort string, session SessionType) string {
	tier := TierForSession(engineName, effort, session)
	return TierModel(engineName, tier)
}

// ResolveModel returns the model to use for an engine call. If epicOverride is
// non-empty, it is returned directly (epic directive takes precedence). Otherwise,
// the tier system resolves the model based on session type, effort, and engine.
func ResolveModel(epicOverride, engineName, effort string, session SessionType) string {
	if epicOverride != "" {
		return epicOverride
	}
	return ResolveModelForSession(engineName, effort, session)
}

// ModelsForEngine returns the ranked model list for the given engine.
func ModelsForEngine(engineName string) ([]Model, error) {
	switch engineName {
	case "claude":
		return ClaudeModels, nil
	case "codex":
		return CodexModels, nil
	case "ollama":
		return OllamaModels, nil
	default:
		return nil, fmt.Errorf("unsupported engine: %s", engineName)
	}
}

// ModelRank returns the capability rank of a model for the given engine.
// Rank 1 is the most capable. Returns 0 if the model is not found.
func ModelRank(engineName, model string) int {
	switch engineName {
	case "claude":
		return claudeModelSet[model]
	case "codex":
		return codexModelSet[model]
	case "ollama":
		return ollamaModelSet[model]
	default:
		return 0
	}
}

// ValidateModel checks whether the given model string is valid for the specified engine.
// An empty model is always valid (the engine CLI will use its own default).
func ValidateModel(engineName, model string) error {
	if model == "" {
		return nil
	}
	switch engineName {
	case "claude":
		if _, ok := claudeModelSet[model]; !ok {
			return fmt.Errorf("invalid model %q for engine %s; valid models: %s", model, engineName, modelIDs(ClaudeModels))
		}
	case "codex":
		if _, ok := codexModelSet[model]; !ok {
			return fmt.Errorf("invalid model %q for engine %s; valid models: %s", model, engineName, modelIDs(CodexModels))
		}
	case "ollama":
		// Ollama accepts any model name — locally pulled models are user-defined.
		// Skip static validation; runtime will error if the model is unavailable.
		return nil
	default:
		return fmt.Errorf("unsupported engine: %s", engineName)
	}
	return nil
}

func modelIDs(models []Model) string {
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return fmt.Sprintf("%v", ids)
}
