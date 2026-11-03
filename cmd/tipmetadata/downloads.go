// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	downloadsStart = "<!-- sylve-tip-downloads -->"
	downloadsEnd   = "<!-- /sylve-tip-downloads -->"
)

var downloadsStatePattern = regexp.MustCompile(`(?s)<!-- sylve-tip-downloads-state: (.*?) -->`)

// Release assets whose downloads add up across tip republishes, in column order.
// The installers were recorded later, so older notes may lack their checkpoints.
var recordedDownloads = []struct {
	name, label string
	optional    bool
}{
	{"sylve-amd64", "Daemon AMD64", false},
	{"sylve-arm64", "Daemon ARM64", false},
	{"FreeBSD+Sylve-15-STABLE-Installer-amd64.iso", "Installer 15-STABLE", true},
	{"FreeBSD+Sylve-16-CURRENT-Installer-amd64.iso", "Installer 16-CURRENT", true},
}

type releaseDownloads struct {
	Body   string          `json:"body"`
	Assets []downloadAsset `json:"assets"`
}

type downloadAsset struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count uint64 `json:"download_count"`
}

type downloadCheckpoint struct {
	Total   uint64 `json:"total"`
	AssetID int64  `json:"assetId"`
	Count   uint64 `json:"count"`
}

func recordDownloads(args []string) error {
	flags := flag.NewFlagSet("downloads", flag.ContinueOnError)
	releasePath := flags.String("release", "", "GitHub release API response")
	output := flags.String("out", "", "updated release notes file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *releasePath == "" || *output == "" {
		return fmt.Errorf("-release and -out are required")
	}
	data, err := os.ReadFile(*releasePath)
	if err != nil {
		return err
	}
	var release releaseDownloads
	if err := json.Unmarshal(data, &release); err != nil {
		return err
	}
	notes, err := downloadNotes(release)
	if err != nil {
		return err
	}
	return os.WriteFile(*output, []byte(notes), 0o644)
}

func downloadNotes(release releaseDownloads) (string, error) {
	state := make(map[string]downloadCheckpoint)
	start := strings.Index(release.Body, downloadsStart)
	end := strings.Index(release.Body, downloadsEnd)
	if start >= 0 || end >= 0 {
		if start < 0 || end < start || strings.Count(release.Body, downloadsStart) != 1 || strings.Count(release.Body, downloadsEnd) != 1 {
			return "", fmt.Errorf("invalid recorded downloads section")
		}
		end += len(downloadsEnd)
		matches := downloadsStatePattern.FindAllStringSubmatch(release.Body[start:end], -1)
		if len(matches) != 1 {
			return "", fmt.Errorf("missing recorded downloads checkpoint")
		}
		if err := json.Unmarshal([]byte(matches[0][1]), &state); err != nil {
			return "", fmt.Errorf("invalid recorded downloads checkpoint: %w", err)
		}
	}

	var labels, aligns, totals []string
	for _, recorded := range recordedDownloads {
		name := recorded.name
		checkpoint, exists := state[name]
		if start >= 0 && !exists && !recorded.optional {
			return "", fmt.Errorf("missing recorded downloads checkpoint for %s", name)
		}
		if checkpoint.Total < checkpoint.Count {
			return "", fmt.Errorf("invalid recorded downloads total for %s", name)
		}
		for _, asset := range release.Assets {
			if asset.Name != name {
				continue
			}
			if asset.ID <= 0 {
				return "", fmt.Errorf("missing release asset ID for %s", name)
			}
			increment := asset.Count
			if checkpoint.AssetID == asset.ID {
				if asset.Count < checkpoint.Count {
					return "", fmt.Errorf("download count decreased for %s", name)
				}
				increment -= checkpoint.Count
			}
			checkpoint.Total += increment
			checkpoint.AssetID = asset.ID
			checkpoint.Count = asset.Count
		}
		state[name] = checkpoint
		labels = append(labels, recorded.label)
		aligns = append(aligns, "---:")
		totals = append(totals, fmt.Sprint(checkpoint.Total))
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	table := tableRow(labels) + "\n" + tableRow(aligns) + "\n" + tableRow(totals)
	section := downloadsStart + "\n### Recorded downloads\n\n" + table +
		"\n\n<!-- sylve-tip-downloads-state: " + string(data) + " -->\n" + downloadsEnd
	if start >= 0 {
		return release.Body[:start] + section + release.Body[end:], nil
	}
	if release.Body == "" {
		return section + "\n", nil
	}
	return strings.TrimRight(release.Body, "\r\n") + "\n\n" + section + "\n", nil
}

func tableRow(cells []string) string {
	return "| " + strings.Join(cells, " | ") + " |"
}
