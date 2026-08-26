// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/cmd"
	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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

func TestRenderHeaderIsFixedWidthAndCentered(t *testing.T) {
	status := consoleprotocol.StatusSnapshot{
		Version:     "0.2.3",
		CPUUsage:    pointerTo(12.0),
		RAMUsed:     pointerTo(uint64(7 << 30)),
		RAMTotal:    pointerTo(uint64(16 << 30)),
		Uptime:      pointerTo(int64(3*86400 + 4*3600)),
		ZFSHealth:   pointerTo("ONLINE"),
		VMRunning:   pointerTo(2),
		VMTotal:     pointerTo(5),
		JailRunning: pointerTo(4),
		JailTotal:   pointerTo(7),
		ActiveTasks: pointerTo(int64(1)),
	}

	for _, width := range []int{8, 24, 40, 80, 120, 180} {
		header := renderHeader(width, "a-very-long-hostname", status)
		if strings.Contains(header, "\n") {
			t.Fatalf("width %d rendered more than one line: %q", width, header)
		}
		if got := lipgloss.Width(header); got != width {
			t.Fatalf("rendered width = %d, want %d for terminal width %d", got, width, width)
		}

		plain := ansiEscapePattern.ReplaceAllString(header, "")
		if width > lipgloss.Width(headerTitle) {
			index := strings.Index(plain, headerTitle)
			if index < 0 {
				t.Fatalf("width %d is missing title: %q", width, plain)
			}
			if got, want := lipgloss.Width(plain[:index]), (width-lipgloss.Width(headerTitle))/2; got != want {
				t.Fatalf("title starts at column %d, want %d for width %d", got, want, width)
			}
		}
	}
}

func TestRenderHeaderShowsFullWideStatus(t *testing.T) {
	status := consoleprotocol.StatusSnapshot{
		CPUUsage:    pointerTo(12.0),
		RAMUsed:     pointerTo(uint64(7 << 30)),
		RAMTotal:    pointerTo(uint64(16 << 30)),
		Uptime:      pointerTo(int64(3*86400 + 4*3600)),
		ZFSHealth:   pointerTo("DEGRADED"),
		VMRunning:   pointerTo(2),
		VMTotal:     pointerTo(5),
		JailRunning: pointerTo(4),
		JailTotal:   pointerTo(7),
		ActiveTasks: pointerTo(int64(1)),
	}
	plain := ansiEscapePattern.ReplaceAllString(renderHeader(180, "node-a", status), "")
	for _, want := range []string{"CPU 12%", "RAM 7/16G", "UP 3d4h", "ZFS DEG", "VM 2/5", "J 4/7", "T 1", "v" + cmd.Version} {
		if !strings.Contains(plain, want) {
			t.Fatalf("wide header %q does not contain %q", plain, want)
		}
	}
}

func TestRenderHeaderUsesCompactTokensBeforeDroppingStatus(t *testing.T) {
	status := consoleprotocol.StatusSnapshot{
		CPUUsage:    pointerTo(12.0),
		RAMUsed:     pointerTo(uint64(7 << 30)),
		RAMTotal:    pointerTo(uint64(16 << 30)),
		Uptime:      pointerTo(int64(3*86400 + 4*3600)),
		ZFSHealth:   pointerTo("DEGRADED"),
		VMRunning:   pointerTo(2),
		VMTotal:     pointerTo(5),
		JailRunning: pointerTo(4),
		JailTotal:   pointerTo(7),
		ActiveTasks: pointerTo(int64(1)),
	}
	plain := ansiEscapePattern.ReplaceAllString(renderHeader(91, "node-a", status), "")
	for _, want := range []string{"C12%", "M44%", "U3d4h", "ZDEG", "V2/5", "J4/7", "T1", "v" + cmd.Version} {
		if !strings.Contains(plain, want) {
			t.Fatalf("compact header %q does not contain %q", plain, want)
		}
	}
}

func TestTUIModelsApplyStatusSnapshots(t *testing.T) {
	snapshot := consoleprotocol.StatusSnapshot{Hostname: "daemon-host", CPUUsage: pointerTo(42.0)}
	localModel, _ := initialTUI(nil).Update(statusMsg{snapshot: snapshot})
	local := localModel.(tuiModel)
	if local.hostname != "daemon-host" || local.status.CPUUsage == nil || *local.status.CPUUsage != 42 {
		t.Fatalf("local status was not applied: %#v", local)
	}

	updatedRemote, _ := (remoteModel{}).Update(statusMsg{snapshot: snapshot})
	remote := updatedRemote.(remoteModel)
	if remote.hostname != "daemon-host" || remote.status.CPUUsage == nil || *remote.status.CPUUsage != 42 {
		t.Fatalf("remote status was not applied: %#v", remote)
	}
}

func TestTUIModelsMarkRetainedStatusStaleOnRefreshError(t *testing.T) {
	snapshot := consoleprotocol.StatusSnapshot{CPUUsage: pointerTo(42.0)}
	localModel, _ := (tuiModel{status: snapshot}).Update(statusMsg{err: errors.New("offline")})
	local := localModel.(tuiModel)
	if !local.status.Stale || local.status.CPUUsage == nil || *local.status.CPUUsage != 42 {
		t.Fatalf("local retained status = %#v", local.status)
	}

	updatedRemote, _ := (remoteModel{status: snapshot}).Update(statusMsg{err: errors.New("offline")})
	remote := updatedRemote.(remoteModel)
	if !remote.status.Stale || remote.status.CPUUsage == nil || *remote.status.CPUUsage != 42 {
		t.Fatalf("remote retained status = %#v", remote.status)
	}
}
