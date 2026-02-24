package zelta

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
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
		last := matches[len(matches)-1]
		if len(last) >= 2 {
			if parsed, err := strconv.ParseUint(last[1], 10, 64); err == nil {
				return &parsed
			}
		}
	}

	matches = syncingSizeRegex.FindAllStringSubmatch(output, -1)
	if len(matches) > 0 {
		last := matches[len(matches)-1]
		if len(last) >= 4 {
			if parsed, ok := parseHumanSizeBytes(last[1], last[2], last[3]); ok {
				return &parsed
			}
		}
	}

	return nil
}

func zfsDatasetUsedBytes(ctx context.Context, dataset string) (*uint64, error) {
	path := strings.TrimSpace(dataset)
	if path == "" {
		return nil, nil
	}

	out, err := utils.RunCommandWithContext(ctx, "zfs", "list", "-Hp", "-o", "used", path)
	if err != nil {
		return nil, err
	}

	line := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	if line == "" || line == "-" {
		return nil, nil
	}

	parsed, err := strconv.ParseUint(line, 10, 64)
	if err != nil {
		return nil, err
	}

	return &parsed, nil
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
