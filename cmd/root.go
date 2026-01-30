package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/nmelo/gasadd/internal/tmux"
	"github.com/spf13/cobra"
)

var (
	windowFlags []string
	sessionFlag string
	patternFlag string
	anyFlag     bool
	allFlag     bool
	dryRunFlag  bool
)

var rootCmd = &cobra.Command{
	Use:   "ga [flags] [message]",
	Short: "Queue messages to Claude agents in tmux windows without interrupting",
	Long: `gasadd queues messages to Claude agents running in tmux windows.

Unlike gn (gasnudge), this does NOT interrupt the agent. It simply adds
the message to Claude's input queue and sends Enter. Claude will process
the message when it's ready.

By default, it only targets windows running Claude in the current session.
Use --any to also include non-Claude windows.

Examples:
  ga "when you're done, run tests"    # Queue to Claude windows only
  ga --any "hello"                    # Queue to all windows (including non-Claude)
  ga -w editor -w build "done"        # Queue to specific windows (must have Claude)
  ga -p "worker-*" "status update"    # Queue to windows matching pattern
  ga --dry-run "test"                 # Show what would receive the message`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAdd,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.Flags().StringArrayVarP(&windowFlags, "window", "w", nil, "Target specific window(s) by name (repeatable)")
	rootCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Target session (default: current)")
	rootCmd.Flags().StringVarP(&patternFlag, "pattern", "p", "", "Filter windows by name pattern (glob-style)")
	rootCmd.Flags().BoolVar(&anyFlag, "any", false, "Include non-Claude windows (default: Claude only)")
	rootCmd.Flags().BoolVarP(&allFlag, "all", "a", false, "Include current window (default: exclude self)")
	rootCmd.Flags().BoolVarP(&dryRunFlag, "dry-run", "n", false, "Show what would receive the message")
}

func runAdd(cmd *cobra.Command, args []string) error {
	message := strings.Join(args, " ")

	// Determine session
	var session string
	var currentWindowIndex int
	var currentPaneID string

	if tmux.IsInsideTmux() {
		var err error
		currentSession, currentWindowIdx, paneID, err := tmux.GetCurrentContext()
		if err != nil {
			return fmt.Errorf("failed to get tmux context: %w", err)
		}
		currentPaneID = paneID
		if sessionFlag != "" {
			session = sessionFlag
			currentWindowIndex = -1 // Different session, don't exclude any window
		} else {
			session = currentSession
			currentWindowIndex = currentWindowIdx
		}
	} else {
		if sessionFlag == "" {
			return fmt.Errorf("not inside tmux; use -s/--session to specify target session")
		}
		session = sessionFlag
		currentWindowIndex = -1 // No window to exclude
	}

	// Verify session exists
	if !tmux.SessionExists(session) {
		return fmt.Errorf("session %q does not exist", session)
	}

	// Get all windows
	windows, err := tmux.ListWindows(session)
	if err != nil {
		return fmt.Errorf("failed to list windows: %w", err)
	}

	// Filter windows
	var targets []tmux.Window
	for _, w := range windows {
		// Exclude current window unless --all is set
		if !allFlag && currentWindowIndex >= 0 && w.Index == currentWindowIndex {
			continue
		}

		// Filter by specific window names if provided
		if len(windowFlags) > 0 {
			found := false
			for _, name := range windowFlags {
				if w.Name == name || fmt.Sprintf("%d", w.Index) == name {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Filter by pattern if provided
		if patternFlag != "" && !tmux.MatchPattern(w.Name, patternFlag) {
			continue
		}

		// Filter to Claude-only by default (unless --any is set)
		if !anyFlag && !tmux.IsClaudeRunning(w) {
			continue
		}

		targets = append(targets, w)
	}

	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "No windows to send message to")
		return nil
	}

	// Dry run: show targets and exit
	if dryRunFlag {
		fmt.Printf("Would queue message to %d window(s) in session %q:\n", len(targets), session)
		for _, w := range targets {
			claudeStatus := ""
			if tmux.IsClaudeRunning(w) {
				claudeStatus = " [claude]"
			}
			fmt.Printf("  %d: %s (%s)%s\n", w.Index, w.Name, w.Command, claudeStatus)
		}
		fmt.Printf("\nMessage: %s\n", message)
		return nil
	}

	// Execute adds
	var succeeded, failed int
	for _, w := range targets {
		target := fmt.Sprintf("%s:%d", session, w.Index)
		if err := tmux.AddMessage(target, message); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to queue message to %s: %v\n", w.Name, err)
			failed++
		} else {
			succeeded++
		}
	}

	// Report results
	if failed > 0 {
		fmt.Printf("Queued to %d window(s), %d failed\n", succeeded, failed)
		return fmt.Errorf("%d message(s) failed", failed)
	}

	// Don't print current pane ID in output, just use it for internal logic
	_ = currentPaneID

	fmt.Printf("Queued to %d window(s)\n", succeeded)
	return nil
}
