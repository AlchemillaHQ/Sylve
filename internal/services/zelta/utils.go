package zelta

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func parseHumanSizeBytes(numStr, unitStr, suffixStr string) (uint64, bool) {
	num, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
	if err != nil || num < 0 {
		return 0, false
	}

	unit := strings.ToUpper(strings.TrimSpace(unitStr))
	suffix := strings.TrimSpace(suffixStr)
	base := 1000.0
	if strings.EqualFold(suffix, "i") || strings.EqualFold(suffix, "iB") {
		base = 1024.0
	}

	var power float64
	switch unit {
	case "":
		power = 0
	case "K":
		power = 1
	case "M":
		power = 2
	case "G":
		power = 3
	case "T":
		power = 4
	case "P":
		power = 5
	case "E":
		power = 6
	default:
		return 0, false
	}

	val := num * math.Pow(base, power)
	if val < 0 || val > math.MaxUint64 {
		return 0, false
	}

	return uint64(math.Round(val)), true
}

func parseTotalBytesFromOutput(output string) *uint64 {
	if strings.TrimSpace(output) == "" {
		return nil
	}

	matches := replicationSizeRegex.FindAllStringSubmatch(output, -1)
	if len(matches) > 0 {
		total := uint64(0)
		found := false
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			parsed, err := strconv.ParseUint(match[1], 10, 64)
			if err != nil {
				continue
			}
			if math.MaxUint64-total < parsed {
				total = math.MaxUint64
			} else {
				total += parsed
			}
			found = true
		}
		if found {
			return &total
		}
	}

	matches = syncingSizeRegex.FindAllStringSubmatch(output, -1)
	if len(matches) > 0 {
		total := uint64(0)
		found := false
		for _, match := range matches {
			if len(match) < 4 {
				continue
			}
			parsed, ok := parseHumanSizeBytes(match[1], match[2], match[3])
			if !ok {
				continue
			}
			if math.MaxUint64-total < parsed {
				total = math.MaxUint64
			} else {
				total += parsed
			}
			found = true
		}
		if found {
			return &total
		}
	}

	return nil
}

func parseMovedBytesFromOutput(output string) *uint64 {
	if strings.TrimSpace(output) == "" {
		return nil
	}

	matches := sentSizeRegex.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil
	}

	total := uint64(0)
	found := false
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		parsed, ok := parseHumanSizeBytes(match[1], match[2], match[3])
		if !ok {
			continue
		}
		if math.MaxUint64-total < parsed {
			total = math.MaxUint64
		} else {
			total += parsed
		}
		found = true
	}
	if !found {
		return nil
	}
	return &total
}

func zfsDatasetUsedBytes(s *Service, ctx context.Context, dataset string) (*uint64, error) {
	path := strings.TrimSpace(dataset)
	if path == "" {
		return nil, nil
	}

	ds, err := s.getLocalDataset(ctx, path)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, nil
	}
	used := ds.Used
	return &used, nil
}

func zfsTargetDatasetUsedBytes(
	s *Service,
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
) (*uint64, error) {
	path := normalizeDatasetPath(dataset)
	if s == nil || target == nil || path == "" {
		return nil, nil
	}

	out, err := s.runTargetZFSList(ctx, target, "-Hp", "-o", "used", "-d", "0", "-t", "filesystem,volume", path)
	if err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "does not exist") ||
			strings.Contains(lower, "dataset does not exist") ||
			strings.Contains(lower, "cannot open") {
			return nil, nil
		}
		return nil, err
	}

	line := strings.TrimSpace(out)
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if line == "" {
		return nil, nil
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil, nil
	}

	val, parseErr := strconv.ParseUint(strings.TrimSpace(fields[0]), 10, 64)
	if parseErr != nil {
		return nil, fmt.Errorf("invalid_remote_used_bytes_value: %w", parseErr)
	}

	return &val, nil
}

func datasetFromZeltaEndpoint(endpoint string) string {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return ""
	}

	if idx := strings.LastIndex(raw, ":"); idx >= 0 && idx+1 < len(raw) {
		return normalizeDatasetPath(raw[idx+1:])
	}

	return normalizeDatasetPath(raw)
}

// autoDestSuffix derives a destination suffix from the source dataset when the user hasn't set one.
//
//   - Jails:    ".../jails/105"             → "jails/105"
//   - VMs:      ".../virtual-machines/100"  → "virtual-machines/100"
//   - Other:    "zroot/sylve/mydata"        → "zroot-sylve-mydata"
func autoDestSuffix(source string) string {
	parts := strings.Split(source, "/")

	// Walk backwards looking for a known prefix segment.
	for i := len(parts) - 1; i >= 0; i-- {
		switch parts[i] {
		case "jails", "virtual-machines":
			// Return from this segment onward: jails/105, virtual-machines/100, etc.
			return strings.Join(parts[i:], "/")
		}
	}

	// Fallback: full source path with "/" replaced by "-".
	return strings.ReplaceAll(source, "/", "-")
}
