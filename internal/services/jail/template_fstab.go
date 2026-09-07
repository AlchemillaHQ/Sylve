// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"path"
	"strings"
)

func normalizeTemplateFstabRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("template_fstab_root_required")
	}

	root = path.Clean(root)
	if !path.IsAbs(root) || root == "/" {
		return "", fmt.Errorf("invalid_template_fstab_root")
	}

	return root, nil
}

func isFstabFieldSeparator(value byte) bool {
	return value == ' ' || value == '\t'
}

func fstabDestinationRange(line string) (int, int, bool) {
	position := 0
	for position < len(line) && isFstabFieldSeparator(line[position]) {
		position++
	}
	if position == len(line) || line[position] == '#' {
		return 0, 0, false
	}

	for position < len(line) && !isFstabFieldSeparator(line[position]) && line[position] != '\r' {
		position++
	}
	for position < len(line) && isFstabFieldSeparator(line[position]) {
		position++
	}
	if position == len(line) || line[position] == '#' || line[position] == '\r' {
		return 0, 0, false
	}

	start := position
	for position < len(line) && !isFstabFieldSeparator(line[position]) && line[position] != '\r' {
		position++
	}
	return start, position, start != position
}

func decodeFstabField(value string) string {
	var decoded strings.Builder
	decoded.Grow(len(value))

	for index := 0; index < len(value); index++ {
		if value[index] == '\\' && index+3 < len(value) &&
			value[index+1] >= '0' && value[index+1] <= '3' &&
			value[index+2] >= '0' && value[index+2] <= '7' &&
			value[index+3] >= '0' && value[index+3] <= '7' {
			decoded.WriteByte(
				(value[index+1]-'0')*64 +
					(value[index+2]-'0')*8 +
					(value[index+3] - '0'),
			)
			index += 3
			continue
		}

		decoded.WriteByte(value[index])
	}

	return decoded.String()
}

func encodeFstabField(value string) string {
	var encoded strings.Builder
	encoded.Grow(len(value))

	for index := 0; index < len(value); index++ {
		switch value[index] {
		case ' ':
			encoded.WriteString(`\040`)
		case '\t':
			encoded.WriteString(`\011`)
		case '\n':
			encoded.WriteString(`\012`)
		case '\r':
			encoded.WriteString(`\015`)
		case '\\':
			encoded.WriteString(`\134`)
		default:
			encoded.WriteByte(value[index])
		}
	}

	return encoded.String()
}

func disableUnresolvedTemplateFstab(fstab string) string {
	if strings.TrimSpace(fstab) == "" {
		return fstab
	}

	lines := strings.Split(fstab, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines[index] = "# " + line
	}

	return "# Sylve: these fstab entries were disabled because the legacy template does not\n" +
		"# record its source jail root. Review destination paths before uncommenting them.\n" +
		strings.Join(lines, "\n")
}

func rebaseTemplateFstab(fstab, sourceRoot, targetRoot string) (string, error) {
	if strings.TrimSpace(fstab) == "" {
		return fstab, nil
	}

	sourceRoot, err := normalizeTemplateFstabRoot(sourceRoot)
	if err != nil {
		return "", err
	}
	targetRoot, err = normalizeTemplateFstabRoot(targetRoot)
	if err != nil {
		return "", err
	}

	lines := strings.Split(fstab, "\n")
	for index, line := range lines {
		start, end, ok := fstabDestinationRange(line)
		if !ok {
			continue
		}

		destination := path.Clean(decodeFstabField(line[start:end]))
		if !path.IsAbs(destination) {
			continue
		}

		relativeDestination := ""
		switch {
		case destination == sourceRoot:
		case strings.HasPrefix(destination, sourceRoot+"/"):
			relativeDestination = strings.TrimPrefix(destination, sourceRoot+"/")
		default:
			continue
		}

		rebasedDestination := targetRoot
		if relativeDestination != "" {
			rebasedDestination = path.Join(targetRoot, relativeDestination)
		}

		lines[index] = line[:start] + encodeFstabField(rebasedDestination) + line[end:]
	}

	return strings.Join(lines, "\n"), nil
}
