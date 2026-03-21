package qemuimg

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRunCapturesStdout(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:    "qemu-img",
		args:   []string{"--version"},
		stdout: "qemu-img version 9.0\n",
	}}}
	q := &qimg{exec: exec}

	out, err := q.run(nil, nil, "qemu-img", "--version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "qemu-img version 9.0\n" {
		t.Fatalf("unexpected output: %q", out)
	}
	exec.assertDone(t)
}

func TestRunWithProvidedOutputWriter(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:    "qemu-img",
		args:   []string{"--version"},
		stdout: "ok\n",
	}}}
	q := &qimg{exec: exec}

	var outBuf bytes.Buffer
	out, err := q.run(nil, &outBuf, "qemu-img", "--version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty returned output when writer is provided, got %q", out)
	}
	if outBuf.String() != "ok\n" {
		t.Fatalf("unexpected writer output: %q", outBuf.String())
	}
	exec.assertDone(t)
}

func TestRunSudoPrefix(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:  "sudo",
		args: []string{"qemu-img", "--version"},
	}}}
	q := &qimg{exec: exec, sudo: true}

	if _, err := q.run(nil, nil, "qemu-img", "--version"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}

func TestRunErrorIncludesStderr(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:    "qemu-img",
		args:   []string{"--version"},
		stderr: "boom details\n",
		err:    errors.New("exit status 1"),
	}}}
	q := &qimg{exec: exec}

	_, err := q.run(nil, nil, "qemu-img", "--version")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "qemuimg: exit status 1 (boom details)") {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}

func TestCheckTools(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:  "qemu-img",
		args: []string{"--version"},
	}}}
	q := &qimg{exec: exec}

	if err := q.CheckTools(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}
