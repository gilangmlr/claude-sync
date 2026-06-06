package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gilangmlr/claude-sync/internal/autohooks"
	"github.com/gilangmlr/claude-sync/internal/config"
	"github.com/gilangmlr/claude-sync/internal/sync"
)

const (
	autoStampFile   = ".last-auto-push"
	autoLogFile     = "auto-push.log"
	autoDisableFile = "auto.disabled"
)

// autoCmd is the parent command. Invoked bare (no subcommand) it runs the
// debounced background push wired into Claude Code hooks (Stop / SessionEnd).
// Subcommands manage the feature: status / enable / disable.
func autoCmd() *cobra.Command {
	var force bool
	var debounceSeconds int

	cmd := &cobra.Command{
		Use:   "auto",
		Short: "Debounced background push for editor hooks (status/enable/disable)",
		Long: `Run an automatic, debounced push in the background.

Invoked with no subcommand it is the hook entry point (Stop / SessionEnd): it
exits quietly and does nothing when auto-sync is disabled, when claude-sync is
not configured, when the debounce window has not elapsed, or when there are no
pending changes. Otherwise it records the attempt and launches 'claude-sync
push -q' as a detached background process, so the editor session is never
blocked and the push survives the session ending.

The debounce window defaults to 300s and can be overridden with --debounce or
the CLAUDE_SYNC_DEBOUNCE_SECONDS environment variable. --force bypasses the
window (used by the SessionEnd hook).

Subcommands:
  status    Show whether auto-sync is enabled, configured, last push, pending
  enable    Install the Stop/SessionEnd hooks and turn auto-sync on
  disable   Remove the hooks and turn auto-sync off

Examples:
  claude-sync auto              # debounced push (Stop hook)
  claude-sync auto --force      # unconditional push (SessionEnd hook)
  claude-sync auto status
  claude-sync auto enable       # default scope: project (.claude/settings.json)
  claude-sync auto disable --scope user`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Disabled wins over everything, including --force.
			if isAutoDisabled() {
				return nil
			}

			debounce := resolveAutoDebounce(cmd, debounceSeconds)

			cfg, err := config.Load()
			if err != nil {
				return nil // not configured: never error out of a hook
			}

			stampPath := filepath.Join(config.ConfigDirPath(), autoStampFile)
			now := time.Now()
			if !sync.ShouldAutoPush(force, readAutoStamp(stampPath), now, debounce) {
				return nil
			}

			syncer, err := sync.NewSyncer(cfg, true)
			if err != nil {
				return nil
			}
			changes, err := syncer.Status(context.Background())
			if err != nil || len(changes) == 0 {
				return nil
			}

			// Record the attempt up front so concurrent hooks honour the window.
			writeAutoStamp(stampPath, now)
			_ = launchDetachedPush()
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Bypass the debounce window (always push)")
	cmd.Flags().IntVar(&debounceSeconds, "debounce", 300, "Minimum seconds between automatic pushes")

	cmd.AddCommand(autoStatusCmd(), autoEnableCmd(), autoDisableCmd())
	return cmd
}

func autoStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show auto-sync status",
		RunE: func(cmd *cobra.Command, args []string) error {
			enabled := !isAutoDisabled()
			fmt.Printf("  auto-sync:  %s\n", onOff(enabled))

			_, cfgErr := config.Load()
			configured := cfgErr == nil
			fmt.Printf("  configured: %s\n", yesNo(configured))

			fmt.Printf("  debounce:   %ds\n", int(resolveAutoDebounce(cmd, 300)/time.Second))

			stampPath := filepath.Join(config.ConfigDirPath(), autoStampFile)
			if last := readAutoStamp(stampPath); !last.IsZero() {
				fmt.Printf("  last push:  %s (%s)\n", last.Format("2006-01-02 15:04"), humanizeAgo(time.Since(last)))
			} else {
				fmt.Printf("  last push:  never\n")
			}

			if configured {
				if cfg, err := config.Load(); err == nil {
					if syncer, err := sync.NewSyncer(cfg, true); err == nil {
						if changes, err := syncer.Status(context.Background()); err == nil {
							fmt.Printf("  pending:    %d file(s)\n", len(changes))
						}
					}
				}
			}

			fmt.Printf("  hooks:      %s\n", describeHooks())
			return nil
		},
	}
}

func autoEnableCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Install the auto-push hooks and turn auto-sync on",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settingsPathForScope(scope)
			if err != nil {
				return err
			}
			changed, err := mutateSettings(path, autohooks.Ensure)
			if err != nil {
				return err
			}
			if changed {
				fmt.Printf("  wrote Stop + SessionEnd hooks to %s\n", prettyPath(path))
			} else {
				fmt.Printf("  hooks already present in %s\n", prettyPath(path))
			}
			if err := setAutoDisabled(false); err != nil {
				return err
			}
			fmt.Printf("  auto-sync enabled\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "project", "Settings scope: project | user")
	return cmd
}

func autoDisableCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Remove the auto-push hooks and turn auto-sync off",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settingsPathForScope(scope)
			if err != nil {
				return err
			}
			changed, err := mutateSettings(path, autohooks.Remove)
			if err != nil {
				return err
			}
			if changed {
				fmt.Printf("  removed claude-sync hooks from %s\n", prettyPath(path))
			} else {
				fmt.Printf("  no claude-sync hooks in %s\n", prettyPath(path))
			}
			if err := setAutoDisabled(true); err != nil {
				return err
			}
			fmt.Printf("  auto-sync disabled (hooks will no-op until re-enabled)\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "project", "Settings scope: project | user")
	return cmd
}

// --- enabled-state marker ---

func autoDisablePath() string {
	return filepath.Join(config.ConfigDirPath(), autoDisableFile)
}

func isAutoDisabled() bool {
	_, err := os.Stat(autoDisablePath())
	return err == nil
}

func setAutoDisabled(disabled bool) error {
	path := autoDisablePath()
	if disabled {
		if err := os.MkdirAll(config.ConfigDirPath(), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("auto-sync disabled\n"), 0o600)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// --- settings.json IO ---

// mutateSettings reads path (treating a missing file as empty), applies fn, and
// writes back only if fn reports a change. Returns whether it changed.
func mutateSettings(path string, fn func(map[string]any) bool) (bool, error) {
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &settings); err != nil {
				return false, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}

	if !fn(settings) {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep > & < literal in hook command strings
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil { // Encode appends a trailing newline
		return false, err
	}
	return true, os.WriteFile(path, buf.Bytes(), 0o644)
}

func settingsPathForScope(scope string) (string, error) {
	switch scope {
	case "project", "":
		return filepath.Join(".claude", "settings.json"), nil
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	default:
		return "", fmt.Errorf("invalid --scope %q (want project|user)", scope)
	}
}

// describeHooks reports which scopes currently have the auto hooks installed.
func describeHooks() string {
	var found []string
	for _, sc := range []string{"project", "user"} {
		if path, err := settingsPathForScope(sc); err == nil && settingsHasHooks(path) {
			found = append(found, fmt.Sprintf("%s (%s)", sc, prettyPath(path)))
		}
	}
	if len(found) == 0 {
		return "not installed (run: claude-sync auto enable)"
	}
	return strings.Join(found, ", ")
}

func settingsHasHooks(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	return autohooks.Detect(settings)
}

// --- shared helpers ---

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

func onOff(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func prettyPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func humanizeAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
