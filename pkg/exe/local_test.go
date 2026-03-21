package exe

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewLocalExecutor(t *testing.T) {
	exec := NewLocalExecutor()
	if exec == nil {
		t.Fatal("expected non-nil executor")
	}

	if _, ok := exec.(*localExec); !ok {
		t.Fatalf("expected *localExec, got %T", exec)
	}
}

func TestLocalExecutorRunWithStdoutAndStderr(t *testing.T) {
	exec := NewLocalExecutor()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := exec.Run(nil, &stdout, &stderr, "sh", "-c", `printf "out"; printf "err" >&2`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := stdout.String(); got != "out" {
		t.Fatalf("expected stdout %q, got %q", "out", got)
	}

	if got := stderr.String(); got != "err" {
		t.Fatalf("expected stderr %q, got %q", "err", got)
	}
}

func TestLocalExecutorRunWithStdin(t *testing.T) {
	exec := NewLocalExecutor()

	var stdout bytes.Buffer
	err := exec.Run(strings.NewReader("hello from stdin"), &stdout, nil, "cat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := stdout.String(); got != "hello from stdin" {
		t.Fatalf("expected stdout %q, got %q", "hello from stdin", got)
	}
}

func TestLocalExecutorRunWithNilStreams(t *testing.T) {
	exec := NewLocalExecutor()

	err := exec.Run(nil, nil, nil, "sh", "-c", "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLocalExecutorRunCommandError(t *testing.T) {
	exec := NewLocalExecutor()

	var stderr bytes.Buffer
	err := exec.Run(nil, nil, &stderr, "sh", "-c", `printf "boom" >&2; exit 7`)
	if err == nil {
		t.Fatal("expected command failure error")
	}

	if got := stderr.String(); got != "boom" {
		t.Fatalf("expected stderr %q, got %q", "boom", got)
	}
}
