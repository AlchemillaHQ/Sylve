package cmd

import (
	"context"
	"strings"
	"testing"
)

func TestNotesCommandsRejectNonPositiveIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"get zero", []string{"get", "--id", "0"}},
		{"get negative", []string{"get", "--id=-1"}},
		{"delete zero", []string{"delete", "--id", "0"}},
		{"delete negative", []string{"delete", "--id=-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newNotesCommand()
			err := command.Run(context.Background(), append([]string{"notes"}, tc.args...))
			if err == nil || !strings.Contains(err.Error(), "--id must be greater than zero") {
				t.Fatalf("command error = %v, want invalid ID error", err)
			}
		})
	}
}
