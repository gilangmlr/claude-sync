#!/bin/bash
# Auto-push claude-sync changes from a Claude Code hook.
#
# Two modes:
#   (default)  Debounced push. Only pushes if DEBOUNCE_SECONDS have elapsed
#              since the last auto-push, so the frequent Stop hook (fires every
#              time Claude finishes a response) does not spam the remote. This
#              is how the "push after idle" behaviour is approximated --
#              Claude Code has no native Idle event, so the Stop event paired
#              with a debounce window gives "at most one push per window while
#              actively working".
#   --force    Unconditional push, used by the SessionEnd hook so the final
#              state of a session is always uploaded.
#
# The push runs in the background and never blocks the session. Nothing happens
# if claude-sync is not installed/configured or there are no pending changes.

DEBOUNCE_SECONDS="${CLAUDE_SYNC_DEBOUNCE_SECONDS:-300}"
STATE_DIR="$HOME/.claude-sync"
CONFIG_FILE="$STATE_DIR/config.yaml"
KEY_FILE="$STATE_DIR/age-key.txt"
STAMP_FILE="$STATE_DIR/.last-auto-push"
LOG_FILE="${CLAUDE_SYNC_LOG_FILE:-$STATE_DIR/auto-push.log}"

force=0
if [ "${1:-}" = "--force" ]; then
    force=1
fi

# Do nothing unless claude-sync is installed and configured.
command -v claude-sync >/dev/null 2>&1 || exit 0
[ -f "$CONFIG_FILE" ] && [ -f "$KEY_FILE" ] || exit 0

now=$(date +%s)

# Respect the debounce window unless forced.
if [ "$force" -ne 1 ] && [ -f "$STAMP_FILE" ]; then
    last=$(cat "$STAMP_FILE" 2>/dev/null)
    case "$last" in
        ''|*[!0-9]*) last=0 ;;
    esac
    if [ "$((now - last))" -lt "$DEBOUNCE_SECONDS" ]; then
        exit 0
    fi
fi

# Skip when there is nothing to push (keeps the remote and log quiet).
status=$(claude-sync status -q 2>/dev/null)
if [ -z "$status" ]; then
    exit 0
fi

# Record the attempt up front so concurrent hooks honour the debounce window.
echo "$now" >"$STAMP_FILE"

# Push in the background so the hook returns immediately.
nohup claude-sync push -q >>"$LOG_FILE" 2>&1 &

exit 0
