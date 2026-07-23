package repl

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandleNotesRejectsExtraArgumentsAndInvalidIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"list", []string{"list", "extra"}, "Usage: notes list"},
		{"add", []string{"add", "title", "content", "extra"}, "Usage: notes add <title> <content>"},
		{"get", []string{"get", "1", "extra"}, "Usage: notes get <id>"},
		{"delete", []string{"delete", "1", "extra"}, "Usage: notes delete <id>"},
		{"zero", []string{"get", "0"}, "Invalid ID '0'"},
		{"overflow", []string{"delete", "18446744073709551616"}, "Invalid ID '18446744073709551616'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			handleNotes(&Context{Out: &out}, tc.args)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("output = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestNotesHelpExplainsQuoting(t *testing.T) {
	var out bytes.Buffer
	handleNotes(&Context{Out: &out}, nil)
	if !strings.Contains(out.String(), `notes add "Release notes" "Text with spaces"`) {
		t.Fatalf("notes help does not explain quoting: %q", out.String())
	}
}
