// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"testing"
)

func TestFSMDispatcherOptionsCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterOption{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	t.Run("set keyboard_layout", func(t *testing.T) {
		raw, _ := json.Marshal(ClusterOption{
			KeyboardLayout: "de",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "options", Action: "set", Data: raw,
		}); err != nil {
			t.Fatalf("set failed: %v", err)
		}

		var opt ClusterOption
		if err := db.First(&opt, 1).Error; err != nil {
			t.Fatalf("fetch option: %v", err)
		}
		if opt.KeyboardLayout != "de" {
			t.Fatalf("layout mismatch: %q", opt.KeyboardLayout)
		}
		if opt.ID != 1 {
			t.Fatalf("ID not forced to 1: %d", opt.ID)
		}
	})

	t.Run("set empty value stored as-is", func(t *testing.T) {
		raw, _ := json.Marshal(ClusterOption{
			KeyboardLayout: "",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "options", Action: "set", Data: raw,
		}); err != nil {
			t.Fatalf("set empty failed: %v", err)
		}

		var opt ClusterOption
		db.First(&opt, 1)
		if opt.KeyboardLayout != "" {
			t.Fatalf("expected empty layout, got: %q", opt.KeyboardLayout)
		}
	})

	t.Run("unknown action is no-op", func(t *testing.T) {
		raw, _ := json.Marshal(ClusterOption{
			KeyboardLayout: "us",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "options", Action: "unknown_action", Data: raw,
		}); err != nil {
			t.Fatalf("unknown action should be no-op: %v", err)
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "options", Action: "set",
			Data: json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}
