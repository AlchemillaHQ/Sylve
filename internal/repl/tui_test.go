// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUIModelsAcceptBracketedPaste(t *testing.T) {
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("vms list"), Paste: true}

	local, _ := initialTUI(nil).Update(paste)
	if got := local.(tuiModel); got.input != "vms list" || got.cursorPos != len("vms list") {
		t.Fatalf("unexpected local input state: %#v", got)
	}

	remote, _ := (remoteModel{}).Update(paste)
	if got := remote.(remoteModel); got.input != "vms list" || got.cursorPos != len("vms list") {
		t.Fatalf("unexpected remote input state: %#v", got)
	}
}

func TestInsertInputRunesNormalizesPastedCommands(t *testing.T) {
	input, cursorPos := insertInputRunes("beforeafter", len("before"), []rune(" vms\r\nlist\t\x03"))
	if input != "before vms  list after" {
		t.Fatalf("expected normalized input, got %q", input)
	}
	if cursorPos != len("before vms  list ") {
		t.Fatalf("expected cursor after pasted input, got %d", cursorPos)
	}
}

func TestInputCursorUsesRuneBoundaries(t *testing.T) {
	input := "a界b"
	afterA := len("a")
	afterWideRune := len("a界")

	if got := inputCursorAfter(input, afterA); got != afterWideRune {
		t.Fatalf("expected cursor after wide rune at %d, got %d", afterWideRune, got)
	}
	if got := inputCursorBefore(input, afterWideRune); got != afterA {
		t.Fatalf("expected cursor before wide rune at %d, got %d", afterA, got)
	}
}
