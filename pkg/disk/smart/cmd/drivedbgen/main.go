// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type record struct {
	family, model, firmware, warning, presets string
}

type attrDef struct {
	name, format string
	hdd, ssd     bool
	increasing   bool
}

var vendorOptionRE = regexp.MustCompile(`(?:^|\s)-v\s+([^\s]+)`)
var firmwareBugRE = regexp.MustCompile(`(?:^|\s)-F\s+([a-z0-9]+)`)

var legacyFormats = map[string]string{
	"halfminutes":             "halfmin2hour",
	"minutes":                 "min2hour",
	"seconds":                 "sec2hour",
	"emergencyretractcyclect": "raw48",
	"loadunload":              "raw24/raw24",
	"10xCelsius":              "temp10x",
	"unknown":                 "raw48",
	"increasing":              "raw48+",
	"offlinescanuncsectorct":  "raw48",
	"writeerrorcount":         "raw48",
	"detectedtacount":         "raw48",
}

func main() {
	inPath := flag.String("in", "libbsmart/drivedb/drivedb.h", "input drivedb.h")
	outPath := flag.String("out", "pkg/disk/smart/drivedb_gen.go", "output Go file")
	flag.Parse()

	raw, err := os.ReadFile(*inPath)
	if err != nil {
		fatal(err)
	}
	records, err := parseRecords(raw)
	if err != nil {
		fatal(err)
	}
	if len(records) < 800 {
		fatal(fmt.Errorf("parsed only %d drive records", len(records)))
	}

	generated, err := generate(records)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*outPath, generated, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "drivedbgen:", err)
	os.Exit(1)
}

func parseRecords(src []byte) ([]record, error) {
	var records []record
	var fields []strings.Builder
	inRecord := false

	for i := 0; i < len(src); {
		switch {
		case i+1 < len(src) && src[i] == '/' && src[i+1] == '/':
			i += 2
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case i+1 < len(src) && src[i] == '/' && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			if i+1 >= len(src) {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += 2
		case src[i] == '"':
			start := i
			i++
			for i < len(src) {
				if src[i] == '\\' {
					i += 2
					continue
				}
				if src[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(src) || src[i-1] != '"' {
				return nil, fmt.Errorf("unterminated string at byte %d", start)
			}
			value, err := strconv.Unquote(string(src[start:i]))
			if err != nil {
				return nil, fmt.Errorf("C string at byte %d: %w", start, err)
			}
			if inRecord {
				fields[len(fields)-1].WriteString(value)
			}
		case src[i] == '{' && !inRecord:
			inRecord = true
			fields = make([]strings.Builder, 1, 5)
			i++
		case src[i] == ',' && inRecord:
			fields = append(fields, strings.Builder{})
			i++
		case src[i] == '}' && inRecord:
			if len(fields) == 5 {
				records = append(records, record{
					family: fields[0].String(), firmware: fields[2].String(),
					model: fields[1].String(), warning: fields[3].String(),
					presets: fields[4].String(),
				})
			}
			inRecord = false
			fields = nil
			i++
		default:
			i++
		}
	}
	return records, nil
}

func parseAttrs(presets string) map[int]attrDef {
	attrs := make(map[int]attrDef)
	for _, match := range vendorOptionRE.FindAllStringSubmatch(presets, -1) {
		parts := strings.Split(match[1], ",")
		if len(parts) < 2 {
			continue
		}
		id, err := strconv.Atoi(parts[0])
		if err != nil || id < 1 || id > 255 {
			continue
		}
		formatName := parts[1]
		if replacement := legacyFormats[formatName]; replacement != "" {
			formatName = replacement
		}
		def := attrDef{format: strings.TrimSuffix(formatName, "+")}
		def.increasing = strings.HasSuffix(formatName, "+")
		if len(parts) >= 3 && parts[2] != "HDD" && parts[2] != "SSD" {
			def.name = parts[2]
		}
		for _, option := range parts[2:] {
			switch option {
			case "HDD":
				def.hdd = true
			case "SSD":
				def.ssd = true
			}
		}
		attrs[id] = def
	}
	return attrs
}

func parseBugs(presets string) []string {
	seen := make(map[string]bool)
	var bugs []string
	for _, match := range firmwareBugRE.FindAllStringSubmatch(presets, -1) {
		var name string
		switch match[1] {
		case "nologdir":
			name = "FirmwareBugNoLogDir"
		case "samsung":
			name = "FirmwareBugSamsung"
		case "samsung2":
			name = "FirmwareBugSamsung2"
		case "samsung3":
			name = "FirmwareBugSamsung3"
		case "xerrorlba":
			name = "FirmwareBugXErrorLBA"
		}
		if name != "" && !seen[name] {
			seen[name] = true
			bugs = append(bugs, name)
		}
	}
	return bugs
}

func generate(records []record) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("// SPDX-License-Identifier: BSD-2-Clause\n//\n// Copyright (c) 2025 The FreeBSD Foundation.\n//\n// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>\n// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,\n// under sponsorship from the FreeBSD Foundation.\n\npackage smart\n\n")

	defaults := map[int]attrDef{}
	for _, rec := range records {
		if rec.family == "DEFAULT" {
			defaults = parseAttrs(rec.presets)
			break
		}
	}
	writeAttrs(&out, "var DefaultAttrDefs = map[uint32]AttrDef", defaults, "", false)

	out.WriteString("var modelDB = []DriveModelEntry{\n")
	for _, rec := range records {
		if rec.family == "DEFAULT" || strings.HasPrefix(rec.family, "VERSION:") || rec.model == "" || rec.model == "-" {
			continue
		}
		fmt.Fprintln(&out, "\t{")
		fmt.Fprintf(&out, "\t\tFamily: %q,\n\t\tModelPattern: %q,\n", rec.family, rec.model)
		if rec.firmware != "" && rec.firmware != "-" {
			fmt.Fprintf(&out, "\t\tFirmwareRegex: %q,\n", rec.firmware)
		}
		if warning := sanitizeWarning(rec.warning); warning != "" {
			fmt.Fprintf(&out, "\t\tWarning: %q,\n", warning)
		}
		if bugs := parseBugs(rec.presets); len(bugs) != 0 {
			fmt.Fprintf(&out, "\t\tFirmwareBugs: %s,\n", strings.Join(bugs, " | "))
		}
		attrs := parseAttrs(rec.presets)
		if len(attrs) != 0 {
			writeAttrs(&out, "\t\tAttrOverrides: map[uint32]AttrDef", attrs, "\t\t", true)
		}
		fmt.Fprintln(&out, "\t},")
	}
	out.WriteString("}\n")
	return format.Source(out.Bytes())
}

func sanitizeWarning(warning string) string {
	lines := strings.Split(warning, "\n")
	kept := lines[:0]
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, " or hdparm ") || strings.Contains(lower, "/ticket/") || strings.Contains(lower, "/wiki/") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func writeAttrs(out *bytes.Buffer, declaration string, attrs map[int]attrDef, indent string, trailingComma bool) {
	fmt.Fprintf(out, "%s{\n", declaration)
	ids := make([]int, 0, len(attrs))
	for id := range attrs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		def := attrs[id]
		fmt.Fprintf(out, "%s\t%d: {", indent, id)
		var fields []string
		if def.format != "" {
			fields = append(fields, fmt.Sprintf("Format: %q", def.format))
		}
		if def.name != "" {
			fields = append(fields, fmt.Sprintf("Name: %q", def.name))
		}
		if def.hdd {
			fields = append(fields, "HDDOnly: true")
		}
		if def.ssd {
			fields = append(fields, "SSDOnly: true")
		}
		if def.increasing {
			fields = append(fields, "Increasing: true")
		}
		fmt.Fprintf(out, "%s},\n", strings.Join(fields, ", "))
	}
	if trailingComma {
		fmt.Fprintf(out, "%s},\n", indent)
	} else {
		fmt.Fprintf(out, "%s}\n", indent)
	}
}
