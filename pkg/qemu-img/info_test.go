package qemuimg

import (
	"strings"
	"testing"
)

func TestInfoSuccess(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:    "qemu-img",
		args:   []string{"info", "--output=json", "/tmp/a.qcow2"},
		stdout: `{"filename":"/tmp/a.qcow2","format":"qcow2","virtual-size":1024}`,
	}}}
	q := &qimg{exec: exec}

	info, err := q.Info("/tmp/a.qcow2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Filename != "/tmp/a.qcow2" || info.Format != "qcow2" || info.VirtualSize != 1024 {
		t.Fatalf("unexpected parsed info: %#v", info)
	}
	exec.assertDone(t)
}

func TestInfoParseError(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:    "qemu-img",
		args:   []string{"info", "--output=json", "/tmp/a.qcow2"},
		stdout: `not-json`,
	}}}
	q := &qimg{exec: exec}

	_, err := q.Info("/tmp/a.qcow2")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to parse info JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}

func TestInfoBackingChainSuccess(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:  "qemu-img",
		args: []string{"info", "--backing-chain", "--output=json", "/tmp/top.qcow2"},
		stdout: `[
			{"filename":"/tmp/top.qcow2","format":"qcow2"},
			{"filename":"/tmp/base.raw","format":"raw"}
		]`,
	}}}
	q := &qimg{exec: exec}

	infos, err := q.InfoBackingChain("/tmp/top.qcow2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("unexpected backing chain length: %d", len(infos))
	}
	if infos[0].Filename != "/tmp/top.qcow2" || infos[1].Format != "raw" {
		t.Fatalf("unexpected parsed backing chain: %#v", infos)
	}
	exec.assertDone(t)
}

func TestInfoBackingChainParseError(t *testing.T) {
	exec := &scriptedExecutor{calls: []execCall{{
		cmd:    "qemu-img",
		args:   []string{"info", "--backing-chain", "--output=json", "/tmp/top.qcow2"},
		stdout: `nope`,
	}}}
	q := &qimg{exec: exec}

	_, err := q.InfoBackingChain("/tmp/top.qcow2")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to parse backing-chain JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
	exec.assertDone(t)
}
