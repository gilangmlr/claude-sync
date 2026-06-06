package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gilangmlr/claude-sync/internal/config"
	"github.com/gilangmlr/claude-sync/internal/sync"
)

const (
	autoStampFile = ".last-auto-push"
	autoLogFile   = "auto-push.log"
)

// autoCmd is the entry point wired into Claude Code hooks (Stop / SessionEnd).
// It is intentionally silent and never returns an error so a misconfigured or
// uninstalled state can't surface as a hook failure in the editor.
func autoCmd() *cobra.Command {
	var force bool
	var debounceSeconds int

	cmd := &cobra.Command{
		Use:   "auto",
		Short: "Debounced background push for editor hooks",
		Long: `Run an automatic, debounced push in the background.

Designed to be called from a Claude Code hook (Stop / SessionEnd). It exits
quietly and does nothing when claude-sync is not configured, when the debounce
window has not elapsed, or when there are no pending changes. Otherwise it
records the attempt and launches 'claude-sync push -q' as a detached background
process, so the editor session is never blocked and the push survives the
session ending.

The debounce window defaults to 300s and can be overridden with --debounce or
the CLAUDE_SYNC_DEBOUNCE_SECONDS environment variable. --force bypasses the
window (used by the SessionEnd hook so the final state is always uploaded).

Examples:
  claude-sync auto              # debounced push (Stop hook)
  claude-sync auto --force      # unconditional push (SessionEnd hook)
  claude-sync auto --debounce 60`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			debounce := resolveAutoDebounce(cmd, debounceSeconds)

			// Do nothing unless configured. Never error out of a hook.
			cfg, err := config.Load()
			if err != nil {
				return nil
			}

			stampPath := filepath.Join(config.ConfigDirPath(), autoStampFile)
			now := time.Now()
			if !sync.ShouldAutoPush(force, readAutoStamp(stampPath), now, debounce) {
				return nil
			}

			// Skip when nothing is pending: keeps the remote/log quiet and avoids
			// resetting the debounce window for a no-op.
			syncer, err := sync.NewSyncer(cfg, true)
			if err != nil {
				return nil
			}
			changes, err := syncer.Status(context.Background())
			if err != nil || len(changes) == 0 {
				return nil
			}

			// Record the attempt up front so concurrent hooks honour the window
			// even before the background push completes.
			writeAutoStamp(stampPath, now)

			// Launch the push detached so the hook returns immediately.
			_ = launchDetachedPush()
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Bypass the debounce window (always push)")
	cmd.Flags().IntVar(&debounceSeconds, "debounce", 300, "Minimum seconds between automatic pushes")
	return cmd
}

// resolveAutoDebounce applies precedence: --debounce flag > env var > default.
func resolveAutoDebounce(cmd *cobra.Command, debounceSeconds int) time.Duration {
	if cmd.Flags().Changed("debounce") {
		return time.Duration(debounceSeconds) * time.Second
	}
	if env := os.Getenv("CLAUDE_SYNC_DEBOUNCE_SECONDS"); env != "" {
		if n, err := strconv.Atoi(env); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return sync.DefaultAutoPushDebounce
}

// readAutoStamp returns the time of the last recorded auto-push attempt, or the
// zero time if the stamp is missing or unparsable.
func readAutoStamp(path string) time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

func writeAutoStamp(path string, t time.Time) {
	_ = os.WriteFile(path, []byte(strconv.FormatInt(t.Unix(), 10)), 0o600)
}

// launchDetachedPush starts `claude-sync push -q` as an independent background
// process whose stdio is redirected to the auto-push log.
func launchDetachedPush() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(config.ConfigDirPath(), autoLogFile)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close() // child receives its own dup'd fd; closing ours is fine.

	c := exec.Command(exe, "push", "-q")
	c.Stdout = logf
	c.Stderr = logf
	c.Stdin = nil
	c.SysProcAttr = detachedSysProcAttr()
	return c.Start() // intentionally not Wait-ed: the push runs independently.
}
