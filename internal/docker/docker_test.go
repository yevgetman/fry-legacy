package docker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectComposeCommand(t *testing.T) {
	t.Parallel()

	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			switch file {
			case "docker":
				return "/usr/bin/docker", nil
			case "docker-compose":
				return "", errors.New("missing")
			default:
				return "", errors.New("missing")
			}
		},
		execCommandContext: func(_ context.Context, name string, args ...string) *exec.Cmd {
			return exec.Command("bash", "-c", "exit 0")
		},
	}

	cmd, err := detectComposeCommand(context.Background(), deps)
	require.NoError(t, err)
	assert.Equal(t, "docker compose", cmd)
}

func TestDetectComposeCommand_FallbackToDockerCompose(t *testing.T) {
	t.Parallel()

	callCount := 0
	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			switch file {
			case "docker":
				return "/usr/bin/docker", nil
			case "docker-compose":
				return "/usr/local/bin/docker-compose", nil
			default:
				return "", errors.New("missing")
			}
		},
		execCommandContext: func(_ context.Context, name string, args ...string) *exec.Cmd {
			callCount++
			if callCount == 1 {
				// "docker compose version" fails
				return exec.Command("bash", "-c", "exit 1")
			}
			// "docker-compose version" succeeds
			return exec.Command("bash", "-c", "exit 0")
		},
	}

	cmd, err := detectComposeCommand(context.Background(), deps)
	require.NoError(t, err)
	assert.Equal(t, "docker-compose", cmd)
}

func TestDetectComposeCommand_NeitherAvailable(t *testing.T) {
	t.Parallel()

	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			return "", errors.New("not found")
		},
		execCommandContext: func(_ context.Context, name string, args ...string) *exec.Cmd {
			return exec.Command("bash", "-c", "exit 1")
		},
	}

	_, err := detectComposeCommand(context.Background(), deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker compose not found")
}

func TestComposeFileExists(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	assert.False(t, ComposeFileExists(projectDir))

	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte("services:\n"), 0o644))
	assert.True(t, ComposeFileExists(projectDir))

	require.NoError(t, os.Remove(filepath.Join(projectDir, "docker-compose.yml")))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "compose.yml"), []byte("services:\n"), 0o644))
	assert.True(t, ComposeFileExists(projectDir))
}

func TestComposeFileExists_DirectoryNotFile(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	// Create a directory with the same name — should not count
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "docker-compose.yml"), 0o755))
	assert.False(t, ComposeFileExists(projectDir))
}

func TestEnsureDockerUp_NoComposeFile(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	// No compose file → should return nil immediately
	err := EnsureDockerUp(context.Background(), projectDir, "", 0)
	require.NoError(t, err)
}

// P3: containersAlreadyRunning

func TestContainersAlreadyRunning(t *testing.T) {
	t.Parallel()

	assert.False(t, containersAlreadyRunning(""))
	assert.False(t, containersAlreadyRunning("HEADER"))
	assert.False(t, containersAlreadyRunning("HEADER\n"))
	assert.False(t, containersAlreadyRunning("HEADER\n  \n"))
	assert.True(t, containersAlreadyRunning("HEADER\ncontainer1  running"))
	assert.True(t, containersAlreadyRunning("NAME\napp  Up 5 minutes"))
	assert.False(t, containersAlreadyRunning("NAME\napp  Exited (1)"))
	assert.False(t, containersAlreadyRunning("NAME\napp  Restarting (1)"))
	// Mixed state: one container Up, one still Starting — returns true because any ready = skip startup.
	// This documents a known asymmetry with composeHealthy (which requires all containers ready).
	assert.True(t, containersAlreadyRunning("NAME\napp1  Up 5 minutes\napp2  Starting"))
}

// P3: composeHealthy

func TestComposeHealthy(t *testing.T) {
	t.Parallel()

	assert.True(t, composeHealthy("NAME  STATUS\napp  Up 5 minutes (healthy)"))
	assert.True(t, composeHealthy("NAME  STATUS\napp  running(healthy)"))
	assert.False(t, composeHealthy("NAME  STATUS\napp  Up 5 minutes (starting)"))
	assert.False(t, composeHealthy("NAME  STATUS\napp  Up 5 minutes (unhealthy)"))
	assert.False(t, composeHealthy("NAME  STATUS\napp  Exited (1)"))
	assert.False(t, composeHealthy("NAME  STATUS\napp  Created"))
	assert.False(t, composeHealthy(""))
}

func TestEnsureDockerUp_ContainersAlreadyRunning(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte("services:\n"), 0o644))

	callCount := 0
	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			if file == "docker" {
				return "/usr/bin/docker", nil
			}
			return "", errors.New("not found")
		},
		execCommandContext: func(_ context.Context, _ string, args ...string) *exec.Cmd {
			callCount++
			if callCount == 1 {
				return exec.Command("bash", "-c", "exit 0")
			}
			return exec.Command("bash", "-c", `printf "NAME  STATUS\napp  Up 5 minutes"`)
		},
		sleep: func(d time.Duration) {},
		now:   time.Now,
	}

	err := ensureDockerUp(context.Background(), projectDir, "", 10, deps)
	require.NoError(t, err)
}

func TestEnsureDockerUp_StartupWithReadyCommand(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte("services:\n"), 0o644))

	callCount := 0
	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			if file == "docker" {
				return "/usr/bin/docker", nil
			}
			return "", errors.New("not found")
		},
		execCommandContext: func(_ context.Context, _ string, args ...string) *exec.Cmd {
			callCount++
			switch callCount {
			case 1:
				return exec.Command("bash", "-c", "exit 0") // detect compose
			case 2:
				return exec.Command("bash", "-c", `echo "NAME  STATUS"`) // ps → not running
			case 3:
				return exec.Command("bash", "-c", "exit 0") // up -d
			default:
				return exec.Command("bash", "-c", "exit 0") // readyCmd → success
			}
		},
		sleep: func(d time.Duration) {},
		now:   time.Now,
	}

	err := ensureDockerUp(context.Background(), projectDir, "ready", 10, deps)
	require.NoError(t, err)
}

func TestEnsureDockerUp_StartupWithHealthCheckPolling(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte("services:\n"), 0o644))

	callCount := 0
	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			if file == "docker" {
				return "/usr/bin/docker", nil
			}
			return "", errors.New("not found")
		},
		execCommandContext: func(_ context.Context, _ string, args ...string) *exec.Cmd {
			callCount++
			switch callCount {
			case 1:
				return exec.Command("bash", "-c", "exit 0") // detect compose
			case 2:
				return exec.Command("bash", "-c", `echo "NAME  STATUS"`) // ps → not running
			case 3:
				return exec.Command("bash", "-c", "exit 0") // up -d
			case 4:
				return exec.Command("bash", "-c", `printf "NAME  STATUS\napp  Starting"`) // ps poll 1 → not healthy
			default:
				return exec.Command("bash", "-c", `printf "NAME  STATUS\napp  Up 5 minutes (healthy)"`) // ps poll 2 → healthy
			}
		},
		sleep: func(d time.Duration) {},
		now:   time.Now,
	}

	err := ensureDockerUp(context.Background(), projectDir, "", 10, deps)
	require.NoError(t, err)
}

func TestEnsureDockerUp_TimeoutExceeded(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte("services:\n"), 0o644))

	startTime := time.Now()
	nowCallCount := 0
	callCount := 0
	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			if file == "docker" {
				return "/usr/bin/docker", nil
			}
			return "", errors.New("not found")
		},
		execCommandContext: func(_ context.Context, _ string, args ...string) *exec.Cmd {
			callCount++
			switch callCount {
			case 1:
				return exec.Command("bash", "-c", "exit 0") // detect compose
			case 2:
				return exec.Command("bash", "-c", `echo "NAME  STATUS"`) // ps → not running
			case 3:
				return exec.Command("bash", "-c", "exit 0") // up -d
			default:
				return exec.Command("bash", "-c", `printf "NAME  STATUS\napp  Starting"`) // ps poll → not healthy
			}
		},
		sleep: func(d time.Duration) {},
		now: func() time.Time {
			nowCallCount++
			if nowCallCount == 1 {
				return startTime
			}
			return startTime.Add(1000 * time.Second)
		},
	}

	err := ensureDockerUp(context.Background(), projectDir, "", 1, deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestEnsureDockerUp_ContextCancelled(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte("services:\n"), 0o644))

	callCount := 0
	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			if file == "docker" {
				return "/usr/bin/docker", nil
			}
			return "", errors.New("not found")
		},
		execCommandContext: func(_ context.Context, _ string, args ...string) *exec.Cmd {
			callCount++
			switch callCount {
			case 1:
				return exec.Command("bash", "-c", "exit 0") // detect compose
			case 2:
				return exec.Command("bash", "-c", `echo "NAME  STATUS"`) // ps → not running
			case 3:
				return exec.Command("bash", "-c", "exit 0") // up -d
			default:
				return exec.Command("bash", "-c", `printf "NAME  STATUS\napp  Starting"`) // ps poll → not healthy
			}
		},
		sleep: func(d time.Duration) {},
		now:   time.Now,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ensureDockerUp(ctx, projectDir, "", 10, deps)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestEnsureDockerUp_SurfacesPortConflictStderr(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte("services:\n"), 0o644))

	callCount := 0
	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			if file == "docker" {
				return "/usr/bin/docker", nil
			}
			return "", errors.New("not found")
		},
		execCommandContext: func(_ context.Context, _ string, args ...string) *exec.Cmd {
			callCount++
			switch callCount {
			case 1: // detectComposeCommand: docker compose version
				return exec.Command("bash", "-c", "exit 0")
			case 2: // ps: nothing running yet
				return exec.Command("bash", "-c", "exit 0")
			default: // up -d: simulate the real port-conflict stderr
				return exec.Command("bash", "-c",
					`echo "Error response from daemon: driver failed programming external connectivity on endpoint app: Bind for 0.0.0.0:5432 failed: port is already allocated" >&2; exit 1`)
			}
		},
		sleep: func(d time.Duration) {},
		now:   time.Now,
	}

	err := ensureDockerUp(context.Background(), projectDir, "", 10, deps)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "docker up:", "should retain the docker up: prefix")
	assert.Contains(t, msg, "port is already allocated", "should surface docker's actual stderr")
	assert.Contains(t, msg, "hint:", "should include the port-conflict remediation hint")
	assert.Contains(t, msg, "docker ps", "hint should mention docker ps for diagnosis")
}

func TestEnsureDockerUp_SurfacesGenericStderrWithoutHint(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), []byte("services:\n"), 0o644))

	callCount := 0
	deps := dockerDeps{
		lookPath: func(file string) (string, error) {
			if file == "docker" {
				return "/usr/bin/docker", nil
			}
			return "", errors.New("not found")
		},
		execCommandContext: func(_ context.Context, _ string, args ...string) *exec.Cmd {
			callCount++
			switch callCount {
			case 1:
				return exec.Command("bash", "-c", "exit 0")
			case 2:
				return exec.Command("bash", "-c", "exit 0")
			default:
				return exec.Command("bash", "-c",
					`echo "Error response from daemon: pull access denied for nonexistent/image" >&2; exit 1`)
			}
		},
		sleep: func(d time.Duration) {},
		now:   time.Now,
	}

	err := ensureDockerUp(context.Background(), projectDir, "", 10, deps)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "pull access denied", "should surface docker's actual stderr")
	assert.NotContains(t, msg, "hint:", "no port-conflict hint for non-port errors")
}

func TestPortConflictHint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"port already allocated", "Bind for 0.0.0.0:5432 failed: port is already allocated", true},
		{"address already in use", "listen tcp 0.0.0.0:6379: bind: address already in use", true},
		{"ports are not available", "Ports are not available: exposing port TCP 0.0.0.0:5432 -> 0.0.0.0:0: listen tcp 0.0.0.0:5432: bind", true},
		{"unrelated error", "no space left on device", false},
		{"empty", "", false},
		{"image not found", "pull access denied for nonexistent/image", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hint := portConflictHint(tc.stderr)
			if tc.want {
				assert.NotEmpty(t, hint, "expected hint for %q", tc.stderr)
				assert.Contains(t, hint, "docker ps")
			} else {
				assert.Empty(t, hint, "did not expect hint for %q", tc.stderr)
			}
		})
	}
}
