# ga

Queue messages to Claude agents in tmux windows without interrupting their work.

Part of the [gas-tools](https://github.com/nmelo) family for multi-agent coordination in tmux.

## Installation

**Homebrew:**
```bash
brew tap nmelo/tap
brew install ga
```

**Go:**
```bash
go install github.com/nmelo/gasadd@latest
```

## Quick Start

```bash
# Queue a message to all Claude windows in current session
ga "tests passed, you can merge"

# Target a specific window
ga -w worker-1 "dependency ready"

# Preview targets without sending
ga -n "test message"
```

## Usage

```
gasadd (ga) queues messages to Claude agents in tmux windows without interrupting.

WHEN TO USE ga vs gn:
  ga  - Non-urgent: "when you're done, run tests" (queues without interrupting)
  gn  - Urgent: "stop now" (sends Escape to interrupt current work)

BEHAVIOR:
  - Only targets windows running Claude (auto-detected via process name/version)
  - Excludes the caller's own window (prevents self-messaging)
  - Sends text + Enter, letting Claude's queue handle timing
  - Does NOT send Escape (preserves ongoing work)

CLAUDE DETECTION:
  Identifies Claude by pane_current_command matching:
  - "claude" or "node" (direct process)
  - Version pattern like "2.1.25"
  - Child processes of shells (inspects via pgrep)

USE CASES FOR AGENT COORDINATION:
  - Notify workers when a dependency is ready
  - Broadcast status updates across a swarm
  - Chain tasks: "when done with X, start Y"
  - Request status without disrupting flow

EXAMPLES:
  ga "tests passed, you can merge"       # Queue to all Claude windows
  ga -w worker-1 "dependency ready"      # Target specific window
  ga -w worker-1 -w worker-2 "sync"      # Multiple windows
  ga -p "worker-*" "checkpoint"          # Glob pattern matching
  ga -s swarm "broadcast message"        # Different tmux session
  ga --any "hello"                       # Include non-Claude windows
  ga -a "note to self"                   # Include own window
  ga -n "test"                           # Dry-run: show targets

RELATED TOOLS:
  gn (gasnudge) - Interrupt agents urgently (sends Escape + Enter)
  gp (gaspeek)  - Read output from agent windows
  gm (gasmail)  - Persistent messaging via beads database

Usage:
  ga [flags] [message]

Flags:
  -a, --all                  Include current window (default: exclude self)
      --any                  Include non-Claude windows (default: Claude only)
  -n, --dry-run              Show what would receive the message
  -h, --help                 help for ga
  -p, --pattern string       Filter windows by name pattern (glob-style)
  -s, --session string       Target session (default: current)
  -w, --window stringArray   Target specific window(s) by name (repeatable)
```

## Related Tools

- [gn](https://github.com/nmelo/gasnudge) - Interrupt agents urgently
- [gp](https://github.com/nmelo/gaspeek) - Read output from agent windows
- [gm](https://github.com/nmelo/gasmail) - Persistent messaging via beads

## Credits

Claude detection and tmux interaction patterns adapted from [gastown](https://github.com/steveyegge/gastown).
