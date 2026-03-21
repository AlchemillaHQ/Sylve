package qemuimg

import (
	"fmt"
	"io"
	"reflect"
	"testing"
)

type execCall struct {
	cmd    string
	args   []string
	stdout string
	stderr string
	err    error
}

type scriptedExecutor struct {
	calls []execCall
	idx   int
}

func (s *scriptedExecutor) Run(_ io.Reader, stdout io.Writer, stderr io.Writer, cmd string, args ...string) error {
	if s.idx >= len(s.calls) {
		return fmt.Errorf("unexpected call: %s %v", cmd, args)
	}

	call := s.calls[s.idx]
	s.idx++

	if cmd != call.cmd {
		return fmt.Errorf("unexpected command: got %q want %q", cmd, call.cmd)
	}
	if !reflect.DeepEqual(args, call.args) {
		return fmt.Errorf("unexpected args: got %#v want %#v", args, call.args)
	}

	if call.stdout != "" && stdout != nil {
		_, _ = io.WriteString(stdout, call.stdout)
	}
	if call.stderr != "" && stderr != nil {
		_, _ = io.WriteString(stderr, call.stderr)
	}

	return call.err
}

func (s *scriptedExecutor) assertDone(t *testing.T) {
	t.Helper()
	if s.idx != len(s.calls) {
		t.Fatalf("not all scripted calls were used: used=%d total=%d", s.idx, len(s.calls))
	}
}
