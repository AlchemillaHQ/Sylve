// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/charmbracelet/lipgloss"
)

const headerTitle = "◇ Sylve"

type headerToken struct {
	kind       string
	label      string
	shortLabel string
	value      string
	shortValue string
	valueStyle lipgloss.Style
	standalone bool
}

func renderHeader(width int, hostname string, status consoleprotocol.StatusSnapshot) string {
	if width <= 0 {
		return ""
	}

	centerWidth := lipgloss.Width(headerTitle)
	if width <= centerWidth {
		return headerBarStyle.Width(width).Render(truncateCells(headerTitle, width))
	}

	leftWidth := (width - centerWidth) / 2
	rightWidth := width - centerWidth - leftWidth
	left := headerBarStyle.Width(leftWidth).Render(truncateCells(hostname, leftWidth))
	center := headerBarStyle.Render(headerTitle)
	rightContent := renderHeaderStatus(rightWidth, status)
	right := headerBarStyle.Width(rightWidth).Align(lipgloss.Right).Render(rightContent)
	return left + center + right
}

func renderHeaderStatus(width int, status consoleprotocol.StatusSnapshot) string {
	if width <= 0 {
		return ""
	}

	tokens := buildHeaderTokens(status, false)
	if rendered, ok := fitHeaderTokens(tokens, width, false); ok {
		return rendered
	}

	tokens = buildHeaderTokens(status, true)
	if rendered, ok := fitHeaderTokens(tokens, width, false); ok {
		return rendered
	}
	if rendered, ok := fitHeaderTokens(tokens, width, true); ok {
		return rendered
	}

	dropGroups := [][]string{{"uptime"}, {"vm", "jail"}}
	if status.ActiveTasks != nil && *status.ActiveTasks == 0 {
		dropGroups = append(dropGroups, []string{"tasks"})
	}
	if status.ActiveTasks != nil && *status.ActiveTasks > 0 {
		dropGroups = append(dropGroups, []string{"tasks"})
	}
	dropGroups = append(dropGroups, []string{"zfs"}, []string{"stale"})

	for _, kinds := range dropGroups {
		tokens = removeHeaderTokens(tokens, kinds...)
		if rendered, ok := fitHeaderTokens(tokens, width, false); ok {
			return rendered
		}
		if rendered, ok := fitHeaderTokens(tokens, width, true); ok {
			return rendered
		}
	}

	core := keepHeaderTokens(tokens, "cpu", "ram", "version")
	if rendered, ok := fitHeaderTokens(core, width, true); ok {
		return rendered
	}

	version := "v" + cmd.Version
	if status.Version != "" {
		version = "v" + strings.TrimPrefix(status.Version, "v")
	}
	return headerVersionStyle.Render(truncateCells(version, width))
}

func buildHeaderTokens(status consoleprotocol.StatusSnapshot, compactRAM bool) []headerToken {
	tokens := make([]headerToken, 0, 9)
	if status.CPUUsage != nil {
		value := fmt.Sprintf("%.0f%%", *status.CPUUsage)
		tokens = append(tokens, metricHeaderToken("cpu", "CPU", "C", value, value, usageHeaderStyle(*status.CPUUsage)))
	}
	if status.RAMUsed != nil && status.RAMTotal != nil && *status.RAMTotal > 0 {
		percent := float64(*status.RAMUsed) / float64(*status.RAMTotal) * 100
		value := formatCompactMemory(*status.RAMUsed, *status.RAMTotal)
		if compactRAM {
			value = fmt.Sprintf("%.0f%%", percent)
		}
		tokens = append(tokens, metricHeaderToken("ram", "RAM", "M", value, fmt.Sprintf("%.0f%%", percent), usageHeaderStyle(percent)))
	}
	if status.Uptime != nil {
		value := formatCompactUptime(*status.Uptime)
		tokens = append(tokens, metricHeaderToken("uptime", "UP", "U", value, value, headerInfoStyle))
	}
	if status.ZFSHealth != nil {
		value := compactPoolHealth(*status.ZFSHealth)
		tokens = append(tokens, metricHeaderToken("zfs", "ZFS", "Z", value, value, poolHealthHeaderStyle(*status.ZFSHealth)))
	}
	if status.VMRunning != nil && status.VMTotal != nil {
		value := fmt.Sprintf("%d/%d", *status.VMRunning, *status.VMTotal)
		tokens = append(tokens, metricHeaderToken("vm", "VM", "V", value, value, headerInfoStyle))
	}
	if status.JailRunning != nil && status.JailTotal != nil {
		value := fmt.Sprintf("%d/%d", *status.JailRunning, *status.JailTotal)
		tokens = append(tokens, metricHeaderToken("jail", "J", "J", value, value, headerInfoStyle))
	}
	if status.ActiveTasks != nil {
		value := fmt.Sprintf("%d", *status.ActiveTasks)
		style := headerGoodStyle
		if *status.ActiveTasks > 0 {
			style = headerInfoStyle
		}
		tokens = append(tokens, metricHeaderToken("tasks", "T", "T", value, value, style))
	}
	if status.Stale {
		tokens = append(tokens, headerToken{
			kind:       "stale",
			value:      "STALE",
			shortValue: "~",
			valueStyle: headerWarningStyle,
			standalone: true,
		})
	}

	version := cmd.Version
	if status.Version != "" {
		version = strings.TrimPrefix(status.Version, "v")
	}
	tokens = append(tokens, headerToken{
		kind:       "version",
		value:      "v" + version,
		shortValue: "v" + version,
		valueStyle: headerVersionStyle,
		standalone: true,
	})
	return tokens
}

func metricHeaderToken(kind, label, shortLabel, value, shortValue string, style lipgloss.Style) headerToken {
	return headerToken{
		kind:       kind,
		label:      label,
		shortLabel: shortLabel,
		value:      value,
		shortValue: shortValue,
		valueStyle: style,
	}
}

func fitHeaderTokens(tokens []headerToken, width int, short bool) (string, bool) {
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		parts = append(parts, token.render(short))
	}
	rendered := strings.Join(parts, headerBarStyle.Render(" "))
	return rendered, lipgloss.Width(rendered) <= width
}

func (t headerToken) render(short bool) string {
	value := t.value
	label := t.label
	if short {
		value = t.shortValue
		label = t.shortLabel
	}
	if t.standalone {
		return t.valueStyle.Render(value)
	}
	separator := " "
	if short {
		separator = ""
	}
	return headerLabelStyle.Render(label) + headerBarStyle.Render(separator) + t.valueStyle.Render(value)
}

func removeHeaderTokens(tokens []headerToken, kinds ...string) []headerToken {
	removed := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		removed[kind] = struct{}{}
	}
	filtered := make([]headerToken, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := removed[token.kind]; !ok {
			filtered = append(filtered, token)
		}
	}
	return filtered
}

func keepHeaderTokens(tokens []headerToken, kinds ...string) []headerToken {
	kept := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		kept[kind] = struct{}{}
	}
	filtered := make([]headerToken, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := kept[token.kind]; ok {
			filtered = append(filtered, token)
		}
	}
	return filtered
}

func usageHeaderStyle(percent float64) lipgloss.Style {
	switch {
	case percent >= 90:
		return headerCriticalStyle
	case percent >= 70:
		return headerWarningStyle
	default:
		return headerGoodStyle
	}
}

func poolHealthHeaderStyle(health string) lipgloss.Style {
	switch strings.ToUpper(strings.TrimSpace(health)) {
	case "ONLINE":
		return headerGoodStyle
	case "NONE", "UNKNOWN":
		return headerVersionStyle
	case "DEGRADED":
		return headerWarningStyle
	default:
		return headerCriticalStyle
	}
}

func compactPoolHealth(health string) string {
	switch strings.ToUpper(strings.TrimSpace(health)) {
	case "ONLINE":
		return "OK"
	case "DEGRADED":
		return "DEG"
	case "FAULTED":
		return "FAULT"
	case "OFFLINE":
		return "OFF"
	case "REMOVED":
		return "REM"
	case "UNAVAIL", "UNAVAILABLE":
		return "UNAVAIL"
	case "SUSPENDED":
		return "SUSP"
	case "CORRUPT_DATA":
		return "CORRUPT"
	case "NONE":
		return "-"
	default:
		return "?"
	}
}

func formatCompactMemory(used, total uint64) string {
	const (
		mib = float64(1024 * 1024)
		gib = float64(1024 * 1024 * 1024)
	)
	unit := "M"
	divisor := mib
	if float64(total) >= gib {
		unit = "G"
		divisor = gib
	}
	return fmt.Sprintf("%s/%s%s", compactDecimal(float64(used)/divisor), compactDecimal(float64(total)/divisor), unit)
}

func compactDecimal(value float64) string {
	if math.Abs(value-math.Round(value)) < 0.05 {
		return fmt.Sprintf("%.0f", value)
	}
	return fmt.Sprintf("%.1f", value)
}

func formatCompactUptime(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	duration := time.Duration(seconds) * time.Second
	days := int(duration / (24 * time.Hour))
	hours := int(duration/time.Hour) % 24
	minutes := int(duration/time.Minute) % 60
	switch {
	case days >= 14:
		return fmt.Sprintf("%dw%dd", days/7, days%7)
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func truncateCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}

	var b strings.Builder
	remaining := width - 1
	for _, r := range value {
		runeWidth := lipgloss.Width(string(r))
		if runeWidth > remaining {
			break
		}
		b.WriteRune(r)
		remaining -= runeWidth
	}
	b.WriteRune('…')
	return b.String()
}
