package qemuimg

import (
	"errors"
	"strings"
	"testing"
)

func TestConvertValidationErrors(t *testing.T) {
	q := &qimg{exec: &scriptedExecutor{}}

	if err := q.Convert("", "/tmp/out.raw", FormatRaw); err == nil || !strings.Contains(err.Error(), "source path is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := q.Convert("/tmp/in.qcow2", "", FormatRaw); err == nil || !strings.Contains(err.Error(), "destination path is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := q.Convert("/tmp/in.qcow2", "/tmp/in.qcow2", FormatRaw); err == nil || !strings.Contains(err.Error(), "source and destination paths are the same") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := q.Convert("/tmp/in.qcow2", "/tmp/out.raw", DiskFormat("badfmt")); err == nil || !strings.Contains(err.Error(), "invalid output format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConvertRejectsUnsupportedSourceFormat(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:    "qemu-img",
		args:   []string{"info", "--output=json", "/tmp/in.img"},
		stdout: `{"format":"unknownfmt"}`,
	}}}
	q := &qimg{exec: exec}

	err := q.Convert("/tmp/in.img", "/tmp/out.raw", FormatRaw)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}

func TestConvertRejectsWhenAlreadyTargetFormat(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:    "qemu-img",
		args:   []string{"info", "--output=json", "/tmp/in.qcow2"},
		stdout: `{"format":"qcow2"}`,
	}}}
	q := &qimg{exec: exec}

	err := q.Convert("/tmp/in.qcow2", "/tmp/out.qcow2", FormatQCOW2)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "already qcow2") {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}

func TestConvertSuccess(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{
		{
			cmd:    "qemu-img",
			args:   []string{"info", "--output=json", "/tmp/in.qcow2"},
			stdout: `{"format":"qcow2"}`,
		},
		{
			cmd:  "qemu-img",
			args: []string{"convert", "-f", "qcow2", "-O", "raw", "/tmp/in.qcow2", "/tmp/out.raw"},
		},
	}}
	q := &qimg{exec: exec}

	if err := q.Convert("/tmp/in.qcow2", "/tmp/out.raw", FormatRaw); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}

func TestConvertPropagatesConvertFailure(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{
		{
			cmd:    "qemu-img",
			args:   []string{"info", "--output=json", "/tmp/in.qcow2"},
			stdout: `{"format":"qcow2"}`,
		},
		{
			cmd:    "qemu-img",
			args:   []string{"convert", "-f", "qcow2", "-O", "raw", "/tmp/in.qcow2", "/tmp/out.raw"},
			stderr: "permission denied\n",
			err:    errors.New("exit status 1"),
		},
	}}
	q := &qimg{exec: exec}

	err := q.Convert("/tmp/in.qcow2", "/tmp/out.raw", FormatRaw)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "convert \"/tmp/in.qcow2\" -> \"/tmp/out.raw\"") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}
