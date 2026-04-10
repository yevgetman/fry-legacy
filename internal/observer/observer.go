package observer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/consciousness"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/templates"
)

// WakePoint identifies when the observer is woken up during a build.
type WakePoint string

const (
	WakeAfterSprint     WakePoint = "after_sprint"
	WakeAfterBuildAudit WakePoint = "after_build_audit"
	WakeBuildEnd        WakePoint = "build_end"
)

// ObserverOpts contains all parameters for an observer wake-up session.
type ObserverOpts struct {
	ProjectDir   string
	Engine       engine.Engine
	Model        string
	EpicName     string
	WakePoint    WakePoint
	SprintNum    int
	TotalSprints int
	EffortLevel  epic.EffortLevel
	Verbose      bool
	BuildData    map[string]string
	Stdout       io.Writer // optional; defaults to os.Stdout when Verbose is true
}

// Observation holds the parsed result of an observer LLM session.
type Observation struct {
	Thoughts        string
	ScratchpadDelta string
	Directives      []consciousness.Directive
	ParseStatus     consciousness.ParseStatus
	ParseError      string
	RawOutputPath   string
}

type InitMode string

const (
	InitModeNew    InitMode = "new"
	InitModeResume InitMode = "resume"
)

// ShouldWakeUp returns whether the observer should wake at this point for the
// given effort level.
func ShouldWakeUp(effort epic.EffortLevel, point WakePoint) bool {
	switch effort {
	case epic.EffortFast:
		return false
	case epic.EffortStandard, "":
		return point == WakeBuildEnd
	default:
		// high, max
		return true
	}
}

// InitBuild initializes the observer for a new build.
// Deprecated: use InitNewSession or ResumeSession.
func InitBuild(projectDir string, epicName string, effort string, totalSprints int) error {
	return InitNewSession(projectDir, epicName, effort, totalSprints)
}

func InitNewSession(projectDir string, epicName string, effort string, totalSprints int) error {
	return initSession(projectDir, epicName, effort, totalSprints, InitModeNew)
}

func ResumeSession(projectDir string, epicName string, effort string, totalSprints int) error {
	return initSession(projectDir, epicName, effort, totalSprints, InitModeResume)
}

func initSession(projectDir string, epicName string, effort string, totalSprints int, mode InitMode) error {
	dir := filepath.Join(projectDir, config.ObserverDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("init observer: create dir: %w", err)
	}

	if mode == InitModeNew {
		if err := WriteScratchpad(projectDir, ""); err != nil {
			return fmt.Errorf("init observer: reset scratchpad: %w", err)
		}
		eventsPath := filepath.Join(projectDir, config.ObserverEventsFile)
		_ = os.Remove(eventsPath)
	}

	return EmitEvent(projectDir, Event{
		Type: EventBuildStart,
		Data: map[string]string{
			"epic":          epicName,
			"effort":        effort,
			"total_sprints": strconv.Itoa(totalSprints),
			"mode":          string(mode),
		},
	})
}

// WriteScratchpad writes the scratchpad file, creating directories if needed.
func WriteScratchpad(projectDir string, content string) error {
	scratchpadPath := filepath.Join(projectDir, config.ObserverScratchpadFile)
	if err := os.MkdirAll(filepath.Dir(scratchpadPath), 0o755); err != nil {
		return fmt.Errorf("write scratchpad: create dir: %w", err)
	}
	if err := os.WriteFile(scratchpadPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write scratchpad: %w", err)
	}
	return nil
}

// ReadScratchpad reads the scratchpad file. Returns "", nil if missing.
func ReadScratchpad(projectDir string) (string, error) {
	scratchpadPath := filepath.Join(projectDir, config.ObserverScratchpadFile)
	data, err := os.ReadFile(scratchpadPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read scratchpad: %w", err)
	}
	return string(data), nil
}

// AppendScratchpad appends text to the scratchpad file.
func AppendScratchpad(projectDir string, text string) error {
	scratchpadPath := filepath.Join(projectDir, config.ObserverScratchpadFile)
	if err := os.MkdirAll(filepath.Dir(scratchpadPath), 0o755); err != nil {
		return fmt.Errorf("append scratchpad: create dir: %w", err)
	}
	f, err := os.OpenFile(scratchpadPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("append scratchpad: open: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		return fmt.Errorf("append scratchpad: write: %w", err)
	}
	return nil
}

// WakeUp runs a single observer LLM session. Non-fatal by design — errors
// are returned but callers should treat them as warnings.
func WakeUp(ctx context.Context, opts ObserverOpts) (*Observation, error) {
	if opts.Engine == nil {
		return nil, fmt.Errorf("observer wake-up: engine is required")
	}

	observerPrompt, err := templates.LoadText(config.ObserverInvocationFile)
	if err != nil {
		return nil, fmt.Errorf("observer wake-up: %w", err)
	}

	// Check context before doing work
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("observer wake-up: %w", ctx.Err())
	default:
	}

	frylog.Log("▶ OBSERVER  wake=%s  sprint=%d/%d", opts.WakePoint, opts.SprintNum, opts.TotalSprints)

	// 1. Read identity, scratchpad, recent events
	identity, err := ReadIdentity()
	if err != nil {
		return nil, fmt.Errorf("observer wake-up: %w", err)
	}

	scratchpad, err := ReadScratchpad(opts.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("observer wake-up: %w", err)
	}

	events, err := ReadRecentEvents(opts.ProjectDir, config.MaxObserverEvents)
	if err != nil {
		return nil, fmt.Errorf("observer wake-up: %w", err)
	}

	// 2. Build prompt
	prompt := buildObserverPrompt(opts, identity, scratchpad, events)

	// 3. Write prompt to ObserverPromptFile
	promptPath := filepath.Join(opts.ProjectDir, config.ObserverPromptFile)
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		return nil, fmt.Errorf("observer wake-up: create dir: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return nil, fmt.Errorf("observer wake-up: write prompt: %w", err)
	}

	// 4. Create log file in build-logs dir
	buildLogsDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("observer wake-up: create logs dir: %w", err)
	}
	logPath := filepath.Join(buildLogsDir,
		fmt.Sprintf("observer_%s_%s.log", opts.WakePoint, time.Now().Format("20060102_150405")),
	)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("observer wake-up: create log: %w", err)
	}
	defer func() {
		_ = logFile.Close()
	}()

	// 5. Invoke engine with observer invocation prompt
	runOpts := engine.RunOpts{
		Model:       opts.Model,
		SessionType: engine.SessionObserver,
		EffortLevel: string(opts.EffortLevel),
		WorkDir:     opts.ProjectDir,
	}

	if opts.Verbose {
		stdout := opts.Stdout
		if stdout == nil {
			stdout = os.Stdout
		}
		writer := io.MultiWriter(stdout, logFile)
		runOpts.Stdout = writer
		runOpts.Stderr = writer
	} else {
		runOpts.Stdout = logFile
		runOpts.Stderr = logFile
	}

	output, _, runErr := opts.Engine.Run(ctx, observerPrompt, runOpts)
	if runErr != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("observer wake-up: %w", runErr)
		}
		// Non-fatal: agent exited non-zero but context wasn't cancelled
		frylog.Log("  OBSERVER: agent exited with error (non-fatal): %v", runErr)
	}

	// 6. Parse response
	obs, parseErr := parseObserverResponse(output)
	if parseErr != nil {
		frylog.Log("  OBSERVER: parse warning: %v", parseErr)
	}
	if obs == nil {
		obs = &Observation{}
	}
	obs.RawOutputPath = logPath
	if parseErr != nil && obs.ParseStatus == "" {
		obs.ParseStatus = consciousness.ParseStatusFailed
		obs.ParseError = parseErr.Error()
	}

	// 7. Append to scratchpad
	if obs.ParseStatus != consciousness.ParseStatusFailed && obs.ScratchpadDelta != "" {
		if err := AppendScratchpad(opts.ProjectDir, "\n---\n"+obs.ScratchpadDelta+"\n"); err != nil {
			frylog.Log("  OBSERVER: scratchpad append failed: %v", err)
		}
	}

	// Cleanup prompt file
	_ = os.Remove(promptPath)

	frylog.Log("  OBSERVER: observation complete")

	return obs, nil
}
