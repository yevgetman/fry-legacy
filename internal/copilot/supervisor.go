package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yevgetman/fry/internal/config"
)

// SupervisorPollInterval is how often the supervisor checks for manifest
// changes when no scheduler is running. Kept short so `fry copilot start`
// feels responsive.
const SupervisorPollInterval = 5 * time.Second

// Supervisor watches for copilot manifest creation and deletion. It runs
// as a goroutine inside fry main for the lifetime of the build.
//
// When no scheduler is running and a manifest appears (either from
// --copilot bootstrap or from `fry copilot start`), the supervisor
// starts the tick scheduler. When a stop-requested signal appears, the
// supervisor stops the scheduler. This decouples copilot lifecycle from
// the `fry run` flag — copilots can be started and stopped mid-build.
type Supervisor struct {
	projectDir string

	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once

	mu        sync.Mutex
	scheduler *TickScheduler
}

// StartSupervisor launches the supervisor goroutine. Call Stop() to
// shut it down (which also stops any running scheduler).
func StartSupervisor(projectDir string) *Supervisor {
	sv := &Supervisor{
		projectDir: projectDir,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	go sv.run()
	return sv
}

// Stop signals the supervisor and any running scheduler to exit. Safe
// to call multiple times.
func (sv *Supervisor) Stop() {
	sv.once.Do(func() {
		close(sv.stopCh)
		// Stop any running scheduler.
		sv.mu.Lock()
		sched := sv.scheduler
		sv.mu.Unlock()
		if sched != nil {
			sched.Stop()
		}
		<-sv.doneCh
	})
}

// SetScheduler allows fry main to hand off an already-running scheduler
// (from the --copilot bootstrap path) to the supervisor. The supervisor
// will then manage its lifecycle (stop on signal, etc.). If a previous
// scheduler is running, it is stopped first. The old scheduler is
// stopped under the lock to prevent poll() from racing with the swap.
func (sv *Supervisor) SetScheduler(sched *TickScheduler) {
	sv.mu.Lock()
	old := sv.scheduler
	sv.scheduler = sched
	// Stop the old scheduler under the lock so poll() cannot observe a
	// half-swapped state. TickScheduler.Stop() is safe to call under
	// an external lock — it only closes a channel and waits.
	if old != nil && old != sched {
		old.Stop()
	}
	sv.mu.Unlock()
}

// Relocate updates the supervisor's project directory. Used when fry
// main redirects to a worktree after prepare/triage — the supervisor
// must look for manifests in the new location.
func (sv *Supervisor) Relocate(newDir string) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.projectDir = newDir
}

// run is the supervisor goroutine.
func (sv *Supervisor) run() {
	defer close(sv.doneCh)

	ticker := time.NewTicker(SupervisorPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sv.stopCh:
			return
		case <-ticker.C:
			sv.poll()
		}
	}
}

// poll checks the copilot directory state and starts or stops the
// scheduler as needed.
func (sv *Supervisor) poll() {
	sv.mu.Lock()
	defer sv.mu.Unlock()

	// If a scheduler is running, check if it's been stopped externally
	// (e.g., the scheduler detected stop-requested and exited its run
	// loop). Clean up the reference if so.
	if sv.scheduler != nil {
		select {
		case <-sv.scheduler.doneCh:
			// Scheduler exited on its own (stop signal or restart failure).
			sv.scheduler = nil
		default:
			// Scheduler still running — nothing to do.
			return
		}
	}

	// No scheduler running. Check if a manifest exists AND no stop is
	// requested. If both conditions hold, start the scheduler.
	stopPath := filepath.Join(sv.projectDir, config.CopilotStopRequestedFile)
	if _, err := os.Stat(stopPath); err == nil {
		// Stop requested — don't start a scheduler.
		return
	}

	manifest, err := ReadManifest(sv.projectDir)
	if err != nil || manifest == nil || manifest.SessionID == "" {
		// No manifest or unreadable — nothing to start.
		return
	}

	// Re-check stop signal after the manifest read to close the race
	// window where stop-requested appears between the first check and
	// the manifest read.
	if _, err := os.Stat(stopPath); err == nil {
		return
	}

	// Manifest exists without stop signal. Start a scheduler.
	intervalDur, _ := time.ParseDuration(manifest.Interval)
	if intervalDur <= 0 {
		intervalDur = time.Duration(config.CopilotDefaultIntervalMinutes) * time.Minute
	}

	sv.scheduler = StartTickScheduler(SchedulerOpts{
		ProjectDir:   sv.projectDir,
		SessionID:    manifest.SessionID,
		Engine:       manifest.Engine,
		Model:        manifest.Model,
		Interval:     intervalDur,
		BuildDir:     manifest.BuildDir,
		FrySourceDir: manifest.FrySourceDir,
		EpicName:     manifest.EpicName,
		EffortLevel:  manifest.EffortLevel,
		TotalSprints: manifest.TotalSprints,
		RunID:        manifest.RunID,
		BuildPID:     manifest.BuildPID,
	})

	_ = AppendEventsText(sv.projectDir, fmt.Sprintf(
		"%s  Supervisor started scheduler for session %s.",
		time.Now().UTC().Format(time.RFC3339), manifest.SessionID))
}
