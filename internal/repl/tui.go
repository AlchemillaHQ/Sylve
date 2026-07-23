// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const inputLineHeight = 1

type sysInfoMsg struct {
	hostname string
	cpuUsage float64
	ramTotal uint64
	ramUsed  uint64
}

type tuiModel struct {
	viewport    viewport.Model
	messages    []string
	ctx         *Context
	history     []string
	historyPath string
	histIdx     int
	input       string
	cursorPos   int
	ready       bool
	width       int
	height      int
	hostname    string
	cpuUsage    string
	ramUsage    string
}

func startTUI(ctx *Context) {
	m := initialTUI(ctx)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(outputWriter(ctx), "TUI error: %v\n", err)
	}
}

func initialTUI(ctx *Context) tuiModel {
	historyPath := ""
	if ctx != nil {
		historyPath = ctx.HistoryPath
	}
	m := tuiModel{
		messages: []string{
			welcomeStyle.Render("Connected to Sylve Console. Type `help`."),
		},
		ctx:         ctx,
		history:     loadReplHistory(historyPath),
		historyPath: historyPath,
		histIdx:     -1,
	}
	if hostname, err := os.Hostname(); err == nil {
		m.hostname = hostname
	}
	if ctx != nil && ctx.Info != nil {
		if info, err := ctx.Info.GetNodeInfo(); err == nil {
			m.cpuUsage = fmt.Sprintf("%.0f%%", info.CPUUsage)
			m.ramUsage = fmt.Sprintf("%.0f%%", info.RAMUsage)
			if info.Hostname != "" {
				m.hostname = info.Hostname
			}
		}
	}
	return m
}

func (m tuiModel) Init() tea.Cmd {
	return m.refreshSysInfo()
}

func (m tuiModel) refreshSysInfo() tea.Cmd {
	if m.ctx == nil || m.ctx.Info == nil {
		return nil
	}
	return tea.Tick(10*time.Second, func(t time.Time) tea.Msg {
		info, err := m.ctx.Info.GetNodeInfo()
		if err != nil {
			return sysInfoMsg{}
		}
		return sysInfoMsg{
			hostname: info.Hostname,
			cpuUsage: info.CPUUsage,
			ramTotal: info.RAMTotal,
			ramUsed:  uint64(float64(info.RAMTotal) * info.RAMUsage / 100.0),
		}
	})
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpHeight := m.height - headerHeight - inputLineHeight

		if !m.ready {
			m.viewport = viewport.New(m.width, vpHeight)
			m.viewport.YPosition = headerHeight
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = vpHeight
		}

		m.viewport.SetContent(m.renderContent())
		return m, nil

	case sysInfoMsg:
		if msg.hostname != "" {
			m.hostname = msg.hostname
		}
		m.cpuUsage = fmt.Sprintf("%.0f%%", msg.cpuUsage)
		if msg.ramTotal > 0 {
			usedGB := float64(msg.ramUsed) / 1024 / 1024 / 1024
			totalGB := float64(msg.ramTotal) / 1024 / 1024 / 1024
			m.ramUsage = fmt.Sprintf("%.1f/%.0fGB", usedGB, totalGB)
		}
		return m, m.refreshSysInfo()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			return m, tea.Quit

		case "enter":
			line := strings.TrimSpace(m.input)
			if line == "" {
				return m, nil
			}

			m.history = recordReplHistory(m.historyPath, m.history, line)
			m.histIdx = -1

			prompt := promptStyle.Render("sylve> ")
			m.messages = append(m.messages, prompt+line)

			output, cont := executeCommand(m.ctx, line)

			m.input = ""
			m.cursorPos = 0

			if output != "" {
				m.messages = append(m.messages, output)
			}

			m.viewport.SetContent(m.renderContent())
			m.viewport.GotoBottom()

			if !cont {
				return m, tea.Quit
			}

			return m, nil

		case "backspace":
			if m.cursorPos > 0 {
				start := inputCursorBefore(m.input, m.cursorPos)
				m.input = m.input[:start] + m.input[m.cursorPos:]
				m.cursorPos = start
			}

		case "delete":
			if m.cursorPos < len(m.input) {
				end := inputCursorAfter(m.input, m.cursorPos)
				m.input = m.input[:m.cursorPos] + m.input[end:]
			}

		case "left":
			if m.cursorPos > 0 {
				m.cursorPos = inputCursorBefore(m.input, m.cursorPos)
			}

		case "right":
			if m.cursorPos < len(m.input) {
				m.cursorPos = inputCursorAfter(m.input, m.cursorPos)
			}

		case "home":
			m.cursorPos = 0

		case "end":
			m.cursorPos = len(m.input)

		case "up":
			if len(m.history) > 0 {
				if m.histIdx == -1 {
					m.histIdx = len(m.history) - 1
				} else if m.histIdx > 0 {
					m.histIdx--
				}
				m.input = m.history[m.histIdx]
				m.cursorPos = len(m.input)
			}

		case "down":
			if m.histIdx >= 0 {
				m.histIdx++
				if m.histIdx >= len(m.history) {
					m.histIdx = -1
					m.input = ""
				} else {
					m.input = m.history[m.histIdx]
				}
				m.cursorPos = len(m.input)
			}

		case "ctrl+u":
			m.input = ""
			m.cursorPos = 0

		case "ctrl+l":
			m.messages = nil
			m.viewport.SetContent("")
			m.viewport.GotoTop()

		case "ctrl+w":
			if idx := strings.LastIndex(m.input[:m.cursorPos], " "); idx >= 0 {
				m.input = m.input[:idx] + m.input[m.cursorPos:]
				m.cursorPos = idx
			} else {
				m.input = m.input[m.cursorPos:]
				m.cursorPos = 0
			}

		default:
			if !msg.Alt && (msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace) {
				m.input, m.cursorPos = insertInputRunes(m.input, m.cursorPos, msg.Runes)
			}
		}

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

func insertInputRunes(input string, cursorPos int, runes []rune) (string, int) {
	if cursorPos < 0 {
		cursorPos = 0
	}
	if cursorPos > len(input) {
		cursorPos = len(input)
	}

	var text strings.Builder
	for _, r := range runes {
		switch {
		case unicode.IsSpace(r):
			// The REPL accepts one command per prompt, so pasted line breaks stay inline.
			text.WriteByte(' ')
		case !unicode.IsControl(r):
			text.WriteRune(r)
		}
	}

	inserted := text.String()
	return input[:cursorPos] + inserted + input[cursorPos:], cursorPos + len(inserted)
}

func inputCursorBefore(input string, cursorPos int) int {
	if cursorPos <= 0 {
		return 0
	}
	if cursorPos > len(input) {
		cursorPos = len(input)
	}
	_, size := utf8.DecodeLastRuneInString(input[:cursorPos])
	return cursorPos - size
}

func inputCursorAfter(input string, cursorPos int) int {
	if cursorPos < 0 {
		return 0
	}
	if cursorPos >= len(input) {
		return len(input)
	}
	_, size := utf8.DecodeRuneInString(input[cursorPos:])
	return cursorPos + size
}

func (m tuiModel) renderContent() string {
	return strings.Join(m.messages, "\n")
}

func (m tuiModel) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	return fmt.Sprintf("%s\n%s\n%s",
		renderHeader(m.width, m.hostname, m.cpuUsage, m.ramUsage),
		m.viewport.View(),
		renderInput(m.width, m.input, m.cursorPos),
	)
}

func renderHeader(width int, hostname, cpu, ram string) string {
	ver := "v" + cmd.Version

	left := hostname
	if cpu != "" {
		left += "  CPU: " + cpu
	}
	if ram != "" {
		left += "  RAM: " + ram
	}

	center := "◇ Sylve"
	right := ver

	lW := lipgloss.Width(left)
	cW := lipgloss.Width(center)
	rW := lipgloss.Width(right)

	filler := width - lW - cW - rW
	if filler < 0 {
		filler = 0
	}
	leftFill := filler / 2
	rightFill := filler - leftFill

	bar := left + strings.Repeat(" ", leftFill) + center + strings.Repeat(" ", rightFill) + right

	return headerBarStyle.Width(width).Render(bar)
}

func renderInput(width int, input string, cursorPos int) string {
	prompt := promptStyle.Render("sylve> ")

	if cursorPos >= len(input) {
		text := inputStyle.Render(input)
		cursor := inputCursorStyle.Render(" ")
		remaining := width - lipgloss.Width(prompt+text) - 1
		if remaining < 0 {
			remaining = 0
		}
		return fmt.Sprintf("%s%s%s%s", prompt, text, cursor, strings.Repeat(" ", remaining))
	}

	before := input[:cursorPos]
	_, size := utf8.DecodeRuneInString(input[cursorPos:])
	at := input[cursorPos : cursorPos+size]
	after := input[cursorPos+size:]

	remaining := width - lipgloss.Width(prompt+before+after) - lipgloss.Width(at)
	if remaining < 0 {
		remaining = 0
	}

	return fmt.Sprintf("%s%s%s%s%s",
		prompt,
		inputStyle.Render(before),
		inputCursorStyle.Render(at),
		inputStyle.Render(after),
		strings.Repeat(" ", remaining),
	)
}

func executeCommand(ctx *Context, cmd string) (output string, cont bool) {
	var localCtx Context
	if ctx != nil {
		localCtx = *ctx
	}
	var buf bytes.Buffer
	localCtx.Out = &buf
	cont = ExecuteLine(&localCtx, cmd)
	output = buf.String()
	return
}

const headerHeight = 1
