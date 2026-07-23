// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type remoteResponse struct {
	output string
	err    string
	close  bool
}

type remoteModel struct {
	viewport    viewport.Model
	messages    []string
	enc         *json.Encoder
	dec         *json.Decoder
	conn        net.Conn
	history     []string
	historyPath string
	histIdx     int
	input       string
	cursorPos   int
	ready       bool
	width       int
	height      int
	hostname    string
}

func newRemoteModel(conn net.Conn, historyPath string) remoteModel {
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	hostname, _ := os.Hostname()

	return remoteModel{
		messages: []string{
			welcomeStyle.Render("Connected to Sylve Console. Type `help`."),
		},
		enc:         enc,
		dec:         dec,
		conn:        conn,
		history:     loadReplHistory(historyPath),
		historyPath: historyPath,
		histIdx:     -1,
		hostname:    hostname,
	}
}

func (m remoteModel) Init() tea.Cmd {
	return nil
}

func (m remoteModel) sendCommand(cmd string) remoteResponse {
	if err := m.enc.Encode(socketRequest{Command: cmd}); err != nil {
		return remoteResponse{err: fmt.Sprintf("send error: %v", err)}
	}

	var resp socketResponse
	if err := m.dec.Decode(&resp); err != nil {
		return remoteResponse{err: fmt.Sprintf("read error: %v", err)}
	}

	return remoteResponse{
		output: resp.Output,
		err:    resp.Error,
		close:  resp.Close,
	}
}

func (m remoteModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case remoteResponse:
		if msg.err != "" {
			m.messages = append(m.messages, styledErrorf("%s", msg.err))
		}
		if msg.output != "" {
			m.messages = append(m.messages, msg.output)
		}

		m.input = ""
		m.cursorPos = 0
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()

		if msg.close {
			m.conn.Close()
			return m, tea.Quit
		}

		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			m.conn.Close()
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

			cmdText := line
			m.input = ""
			m.cursorPos = 0

			m.viewport.SetContent(m.renderContent())
			return m, func() tea.Msg {
				return m.sendCommand(cmdText)
			}

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

func (m remoteModel) renderContent() string {
	return strings.Join(m.messages, "\n")
}

func (m remoteModel) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	header := renderHeader(m.width, m.hostname, "", "")
	vp := m.viewport.View()
	input := renderInput(m.width, m.input, m.cursorPos)

	return fmt.Sprintf("%s\n%s\n%s", header, vp, input)
}

func runRemoteConsoleTUI(conn net.Conn, historyPath string) error {
	p := tea.NewProgram(newRemoteModel(conn, historyPath), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}
