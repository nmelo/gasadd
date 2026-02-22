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

// maxSendKeysChunk is the safe maximum bytes per tmux send-keys call.
// tmux has an internal limit (~16326 chars); we stay well below it.
const maxSendKeysChunk = 4096

// AddMessage sends a message to a tmux window's queue without interrupting.
// Unlike NudgeWindow, this does NOT send Escape (which would interrupt vim/Claude).
// Target format: "session:window" (e.g., "main:1" or "main:editor")
//
// When bracketedPaste is true (use for Claude Code windows), the message is
// wrapped in terminal bracketed-paste markers (ESC[200~ ... ESC[201~). This
// signals to Claude Code that the input is an intentional paste, preventing the
// [Pasted text #N] confirmation dialog that otherwise appears for large or fast
// input. The dialog was causing the original Enter to be consumed as a
// confirmation rather than a submit, leaving messages stuck in the input box.
//
// When bracketedPaste is false (use for --any / non-Claude windows), the old
// approach is used with a fallback paste-indicator check.
func AddMessage(target, message string, bracketedPaste bool) error {
	lock := getAddLock(target)
	lock.Lock()
	defer lock.Unlock()

	// Exit copy-mode if target is scrolled up (send-keys hangs in copy-mode)
	ExitCopyMode(target)

	// Normalize newlines to the two-character literal \n sequence.
	// Raw \n bytes sent via send-keys -l are interpreted as "insert newline" by
	// Claude Code's multiline input, leaving the message stuck in the input box
	// with a trailing blank line that never submits. Encoding them as \n (text)
	// preserves the structure while keeping the input as a single submittable line.
	message = strings.ReplaceAll(message, "\r\n", `\n`)
	message = strings.ReplaceAll(message, "\r", "")
	message = strings.ReplaceAll(message, "\n", `\n`)

	if bracketedPaste {
		// ESC[200~ signals start of bracketed paste. Claude Code receives this and
		// treats subsequent characters as paste content rather than keyboard input,
		// bypassing its paste-detection heuristic entirely.
		if _, err := run("send-keys", "-t", target, "-l", "\x1b[200~"); err != nil {
			return err
		}
	}

	// Send in chunks to stay under tmux's send-keys size limit (~16326 chars).
	// Long messages (agent logs, code, detailed status) easily exceed this limit
	// and fail with "command too long" without chunking.
	remaining := message
	for len(remaining) > 0 {
		chunk := remaining
		if len(chunk) > maxSendKeysChunk {
			chunk = remaining[:maxSendKeysChunk]
		}
		remaining = remaining[len(chunk):]

		if _, err := run("send-keys", "-t", target, "-l", chunk); err != nil {
			return err
		}
		if len(remaining) > 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	if bracketedPaste {
		// ESC[201~ closes the bracketed paste. Claude Code now has the full
		// message in its input buffer, ready to submit with Enter.
		if _, err := run("send-keys", "-t", target, "-l", "\x1b[201~"); err != nil {
			return err
		}
	}

	// Pause to let the paste end-marker be processed before sending Enter.
	// With bracketed paste, Claude Code needs a moment to exit paste mode
	// and render the text in the input buffer before it can accept Enter.
	time.Sleep(200 * time.Millisecond)

	// Send Enter to submit the message (no Escape, no interruption)
	if _, err := run("send-keys", "-t", target, "Enter"); err != nil {
		return fmt.Errorf("failed to send Enter: %w", err)
	}

	// Poll for up to 1.5s to ensure the message was actually submitted.
	// Two failure modes both require a second Enter:
	//   1. Paste indicator ([Pasted text #N]): the first Enter confirms the
	//      paste reference instead of submitting it.
	//   2. Text still in input: bracketed paste end-marker wasn't processed
	//      before Enter arrived, so Enter was treated as a literal char.
	// We retry with short polls rather than a fixed delay to return quickly
	// on the common case where the first Enter worked.
	for _, wait := range [4]time.Duration{300 * time.Millisecond, 400 * time.Millisecond, 400 * time.Millisecond, 400 * time.Millisecond} {
		time.Sleep(wait)
		if !hasStuckInput(target) {
			break // prompt is empty - message was submitted successfully
		}
		_, _ = run("send-keys", "-t", target, "Enter")
	}

	return nil
}

// pasteIndicatorPattern matches Claude Code's paste reference placeholder.
// Handles formats like "[Pasted text #1]" and "[Pasted text #9 +2 lines]".
var pasteIndicatorPattern = regexp.MustCompile(`\[Pasted text #\d+[^\]]*\]`)

// hasStuckInput returns true when the message did not submit cleanly and
// a second Enter is needed. Covers two cases:
//
//  1. Paste indicator: "[Pasted text #N]" is visible near the prompt - the first
//     Enter confirmed the paste reference rather than submitting the message.
//
//  2. Text still in prompt: bracketed paste end-marker wasn't processed before
//     Enter arrived, so Enter was treated as a literal char and the text is
//     still sitting in the input box waiting to be submitted.
func hasStuckInput(target string) bool {
	out, err := run("capture-pane", "-t", target, "-p", "-e")
	if err != nil {
		return false
	}
	lines := strings.Split(out, "\n")
	startIdx := len(lines) - 8
	if startIdx < 0 {
		startIdx = 0
	}
	for _, line := range lines[startIdx:] {
		// Case 1: paste indicator placeholder
		if pasteIndicatorPattern.MatchString(line) {
			return true
		}
		// Case 2: prompt has non-empty content (text stuck in input)
		if matches := promptPattern.FindStringSubmatch(line); matches != nil {
			content := strings.TrimSpace(matches[1])
			if content == "" {
				continue
			}
			// Ignore styled autocomplete suggestions (ghost text has embedded ANSI codes)
			if strings.ContainsRune(content, '\x1b') {
				continue
			}
			return true
		}
	}
	return false
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

// IsInCopyMode checks if the target pane is in copy-mode (scrolled up)
func IsInCopyMode(target string) bool {
	out, err := run("display-message", "-t", target, "-p", "#{pane_in_mode}")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "1"
}

// ExitCopyMode sends 'q' to exit copy-mode if the pane is in it
func ExitCopyMode(target string) {
	if IsInCopyMode(target) {
		_, _ = run("send-keys", "-t", target, "q")
		time.Sleep(50 * time.Millisecond)
	}
}

// promptPattern matches Claude Code prompt lines (with possible ANSI codes before ❯)
var promptPattern = regexp.MustCompile(`❯\s?(.*)$`)

// ansiPattern matches ANSI/VT100 escape sequences for stripping
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;:]*[A-Za-z]|\x1b[^[]`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}


// ReadyState describes whether Claude Code is ready to receive queued input.
type ReadyState int

const (
	ReadyForInput ReadyState = iota // ❯ prompt visible, no pending text - safe to send
	PendingInput                    // ❯ prompt visible, text already in the input box
	NotAtPrompt                     // ❯ prompt not visible - busy, generating, or in a form
)

// CheckInputReady returns the current input state of a Claude Code window.
//
// Finds the last ❯ prompt in the entire visible terminal area, then verifies it
// is the CURRENT prompt by checking that only blank lines, dividers, and Claude
// Code status bar lines appear below it. If non-status content appears below the
// prompt (e.g. an AskUserQuestion form UI), this returns NotAtPrompt.
//
// This approach works for both freshly started sessions (where the prompt sits
// near the top of the visible area with blank space below) and busy sessions
// (where the prompt sits near the bottom after scrolled output).
func CheckInputReady(target string) (ReadyState, string) {
	out, err := run("capture-pane", "-t", target, "-p", "-e")
	if err != nil {
		return NotAtPrompt, ""
	}

	lines := strings.Split(out, "\n")

	// Find the last ❯ in the visible terminal area.
	lastPromptIdx := -1
	for i, line := range lines {
		if promptPattern.MatchString(line) {
			lastPromptIdx = i
		}
	}

	if lastPromptIdx < 0 {
		return NotAtPrompt, ""
	}

	// Verify this is the CURRENT prompt: everything below it must be blank,
	// a divider (─────), or a Claude Code status bar line (contains │ or ⏵⏵).
	// If any other content appears below, it's a form UI - not the input prompt.
	// Lines are stripped of ANSI escape codes before checking since Claude Code
	// renders dividers and status bars with color/style codes.
	for _, line := range lines[lastPromptIdx+1:] {
		t := strings.TrimSpace(stripANSI(line))
		if t == "" {
			continue
		}
		if strings.TrimLeft(t, "─ ") == "" { // divider: only ─ and spaces remain
			continue
		}
		if strings.Contains(t, "│") { // status bar: uses │ as field separator
			continue
		}
		if strings.HasPrefix(t, "⏵⏵") || strings.HasPrefix(t, "▶▶") { // mode line
			continue
		}
		// Non-status content below the prompt = Claude is busy or showing a form
		return NotAtPrompt, ""
	}

	// Reached here: the ❯ prompt is the current one and only status/blank
	// lines appear below it. The prompt is ready to receive input regardless
	// of whether it shows ghost text or real pending content.
	//
	// We do NOT block on non-empty prompt content here: ghost suggestions
	// (which have no ANSI codes and look identical to typed text in the
	// terminal buffer) cannot be reliably distinguished from real pending
	// input by capture-pane. Sending a message clears any ghost text and
	// submits cleanly; if there IS real pending input, the retry loop in
	// AddMessage will detect the stuck text and send extra Enters.
	return ReadyForInput, ""
}

// HasPendingInput checks if the target pane has text after the prompt (user is typing).
// Returns true if there's pending input, along with the input text.
// Ignores autocomplete suggestions which are styled with ANSI escape codes.
//
// Deprecated: prefer CheckInputReady which also detects when Claude is busy or in a form.
func HasPendingInput(target string) (bool, string) {
	state, text := CheckInputReady(target)
	return state == PendingInput, text
}
