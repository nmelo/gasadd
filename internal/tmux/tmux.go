package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Window represents a tmux window with its metadata
type Window struct {
	Index   int
	Name    string
	PaneID  string
	Command string // pane_current_command
}

// versionPattern matches Claude Code version numbers like "2.0.76"
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// supportedShells lists shell binaries we recognize
var supportedShells = []string{"bash", "zsh", "sh", "fish", "tcsh", "ksh"}

// addLocks serializes adds to the same target to prevent interleaving
var (
	addLocksMu sync.Mutex
	addLocks   = make(map[string]*sync.Mutex)
)

func getAddLock(target string) *sync.Mutex {
	addLocksMu.Lock()
	defer addLocksMu.Unlock()
	if addLocks[target] == nil {
		addLocks[target] = &sync.Mutex{}
	}
	return addLocks[target]
}

// run executes a tmux command and returns stdout
func run(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// ListWindows returns all windows in the specified session with their metadata
func ListWindows(session string) ([]Window, error) {
	format := "#{window_index}|#{window_name}|#{pane_id}|#{pane_current_command}"
	out, err := run("list-windows", "-t", session, "-F", format)
	if err != nil {
		return nil, err
	}

	var windows []Window
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		var idx int
		fmt.Sscanf(parts[0], "%d", &idx)
		windows = append(windows, Window{
			Index:   idx,
			Name:    parts[1],
			PaneID:  parts[2],
			Command: parts[3],
		})
	}
	return windows, nil
}

// GetCurrentContext returns the current session name and window index when inside tmux.
// Uses TMUX_PANE environment variable to identify the caller's pane.
func GetCurrentContext() (session string, windowIndex int, paneID string, err error) {
	paneID = os.Getenv("TMUX_PANE")
	if paneID == "" {
		return "", 0, "", fmt.Errorf("not running inside tmux (TMUX_PANE not set)")
	}

	// Get session:window for current pane
	out, err := run("display-message", "-p", "-t", paneID, "#{session_name}|#{window_index}")
	if err != nil {
		return "", 0, "", err
	}
	parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
	if len(parts) < 2 {
		return "", 0, "", fmt.Errorf("unexpected tmux output: %s", out)
	}
	session = parts[0]
	fmt.Sscanf(parts[1], "%d", &windowIndex)
	return session, windowIndex, paneID, nil
}

// IsInsideTmux returns true if running inside a tmux session
func IsInsideTmux() bool {
	return os.Getenv("TMUX_PANE") != ""
}

// AddMessage sends a message to a tmux window's queue without interrupting.
// Unlike NudgeWindow, this does NOT send Escape (which would interrupt vim/Claude).
// Target format: "session:window" (e.g., "main:1" or "main:editor")
func AddMessage(target, message string) error {
	lock := getAddLock(target)
	lock.Lock()
	defer lock.Unlock()

	// 1. Send text in literal mode (handles special characters)
	if _, err := run("send-keys", "-t", target, "-l", message); err != nil {
		return err
	}

	// 2. Brief pause to let text settle
	time.Sleep(100 * time.Millisecond)

	// 3. Send Enter to queue the message (no Escape, no interruption)
	if _, err := run("send-keys", "-t", target, "Enter"); err != nil {
		return fmt.Errorf("failed to send Enter: %w", err)
	}

	return nil
}

// IsClaudeRunning checks if Claude appears to be running in the window.
// Detects by pane_current_command: "node", "claude", or version pattern like "2.0.76".
// Also checks for child processes when the pane is a shell.
func IsClaudeRunning(w Window) bool {
	cmd := w.Command

	// Check for direct command matches
	if cmd == "node" || cmd == "claude" {
		return true
	}

	// Check for version pattern (e.g., "2.0.76")
	if versionPattern.MatchString(cmd) {
		return true
	}

	// If pane command is a shell, check for claude/node child processes
	for _, shell := range supportedShells {
		if cmd == shell {
			pid := getPanePID(w.PaneID)
			if pid != "" {
				return hasClaudeChild(pid)
			}
			break
		}
	}
	return false
}

// getPanePID returns the PID of the pane's main process
func getPanePID(paneID string) string {
	out, err := run("list-panes", "-t", paneID, "-F", "#{pane_pid}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// hasClaudeChild checks if a process has a child running claude/node
func hasClaudeChild(pid string) bool {
	cmd := exec.Command("pgrep", "-P", pid, "-l")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "PID name" e.g., "29677 node"
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			name := parts[1]
			if name == "node" || name == "claude" {
				return true
			}
		}
	}
	return false
}

// MatchPattern checks if a window name matches a glob-like pattern.
// Supports * as wildcard.
func MatchPattern(name, pattern string) bool {
	// Convert glob pattern to regex
	regexPattern := "^" + regexp.QuoteMeta(pattern) + "$"
	regexPattern = strings.ReplaceAll(regexPattern, `\*`, ".*")
	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

// SessionExists checks if a tmux session exists
func SessionExists(session string) bool {
	_, err := run("has-session", "-t", session)
	return err == nil
}
