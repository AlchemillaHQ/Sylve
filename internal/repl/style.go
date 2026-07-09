// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	headerBarStyle = lipgloss.NewStyle().
			Background(clrHeaderBG).
			Foreground(clrHeaderFG).
			Bold(true)

	headerVersionStyle = lipgloss.NewStyle().
				Background(clrHeaderBG).
				Foreground(clrDim)

	promptStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrCyan)

	inputStyle = lipgloss.NewStyle().
			Foreground(clrText)

	inputCursorStyle = lipgloss.NewStyle().
				Foreground(clrHeaderBG).
				Background(clrCyan)

	welcomeStyle = lipgloss.NewStyle().
			Foreground(clrGreen).
			Italic(true)

	successStyle = lipgloss.NewStyle().
			Foreground(clrGreen)

	errorStyle = lipgloss.NewStyle().
			Foreground(clrRed)

	keyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrHeaderFG)

	valueStyle = lipgloss.NewStyle().
			Foreground(clrText)

	helpCommandStyle = lipgloss.NewStyle().
				Foreground(clrCyan)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(clrDim)
)

func styledHelpList(commands []cmdHelp) string {
	var b strings.Builder
	maxLen := 0
	for _, c := range commands {
		if l := lipgloss.Width(c.Name); l > maxLen {
			maxLen = l
		}
	}

	for _, c := range commands {
		padding := strings.Repeat(" ", maxLen-lipgloss.Width(c.Name))
		b.WriteString(helpCommandStyle.Render(c.Name))
		b.WriteString(padding)
		b.WriteString("  ")
		b.WriteString(helpDescStyle.Render("-- " + c.Desc))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func styledTable(headers []string, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}

	colWidths := make([]int, len(headers))
	for i, h := range headers {
		colWidths[i] = lipgloss.Width(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			w := lipgloss.Width(cell)
			if w > colWidths[i] {
				colWidths[i] = w
			}
		}
	}

	colStrs := make([]string, len(headers))
	for i, h := range headers {
		colStrs[i] = keyStyle.Width(colWidths[i]).Render(h)
	}
	line := strings.Join(colStrs, "    ")

	var sepParts []string
	for _, w := range colWidths {
		sepParts = append(sepParts, strings.Repeat("─", w))
	}
	sep := helpDescStyle.Render(strings.Join(sepParts, "────"))

	var b strings.Builder
	b.WriteString(line)
	b.WriteString("\n")
	b.WriteString(sep)

	for _, row := range rows {
		b.WriteString("\n")
		cellStrs := make([]string, len(row))
		for i, cell := range row {
			cellStrs[i] = valueStyle.Width(colWidths[i]).Render(cell)
		}
		b.WriteString(strings.Join(cellStrs, "    "))
	}

	return b.String()
}

func styledKeyValue(key, value string) string {
	return keyStyle.Render(key) + "  " + valueStyle.Render(value)
}

func styledSuccessf(format string, args ...any) string {
	return successStyle.Render(fmt.Sprintf(format, args...))
}

func styledErrorf(format string, args ...any) string {
	return errorStyle.Render(fmt.Sprintf(format, args...))
}
