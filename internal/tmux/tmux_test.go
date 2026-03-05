package tmux

import "testing"

func TestParseInputReady(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantState    ReadyState
		wantContent  string
	}{
		{
			name:      "empty prompt",
			input:     "some output\n❯ \n\n\n",
			wantState: ReadyForInput,
		},
		{
			name:      "ANSI-styled hint (Try ...)",
			input:     "some output\n❯ \x1b[2mTry \"edit main.go to fix the bug\"\x1b[0m\n\n",
			wantState: ReadyForInput,
		},
		{
			name:        "plain typed text",
			input:       "some output\n❯ hello world\n\n",
			wantState:   PendingInput,
			wantContent: "hello world",
		},
		{
			name:      "ANSI-only content (cursor control)",
			input:     "some output\n❯ \x1b[?25l\n\n",
			wantState: ReadyForInput,
		},
		{
			name:      "no prompt visible",
			input:     "Claude is generating output...\nsome more text\n",
			wantState: NotAtPrompt,
		},
		{
			name:      "form below prompt",
			input:     "some output\n❯ \n> Option A\n> Option B\n",
			wantState: NotAtPrompt,
		},
		{
			name:      "prompt with divider and status below",
			input:     "some output\n❯ \n───────────────\n│ cost: $0.01 │ 3.2k tokens\n",
			wantState: ReadyForInput,
		},
		{
			name:      "multiple prompts - last one matters",
			input:     "❯ old command\noutput\n❯ \n\n",
			wantState: ReadyForInput,
		},
		{
			name:      "styled non-hint text (green ANSI)",
			input:     "some output\n❯ \x1b[32msome styled text\x1b[0m\n\n",
			wantState: ReadyForInput,
		},
		{
			name:      "mode line below prompt",
			input:     "some output\n❯ \n⏵⏵ plan mode\n",
			wantState: ReadyForInput,
		},
		{
			name:      "ANSI in prompt line but plain text after strip",
			input:     "some output\n\x1b[34m❯\x1b[0m \x1b[2mhint text\x1b[0m\n\n",
			wantState: ReadyForInput,
		},
		{
			name:        "multiple prompts - last has typed text",
			input:       "❯ \noutput\n❯ hello\n\n",
			wantState:   PendingInput,
			wantContent: "hello",
		},
		{
			name:        "real typed text with cursor block (regression test)",
			input:       "some output\n❯ half typed message here\x1b[7m \x1b[0m\n\n",
			wantState:   PendingInput,
			wantContent: "half typed message here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotState, gotContent := parseInputReady(tt.input)
			if gotState != tt.wantState {
				t.Errorf("parseInputReady() state = %v, want %v", gotState, tt.wantState)
			}
			if gotContent != tt.wantContent {
				t.Errorf("parseInputReady() content = %q, want %q", gotContent, tt.wantContent)
			}
		})
	}
}
