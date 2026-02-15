package swapctl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
)

type SwapDevice struct {
	Device     string
	Blocks1024 int64
	Used       int64
}

func GetSwapDevices() ([]SwapDevice, error) {
	output, err := utils.RunCommand("/sbin/swapctl", "-l")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) <= 1 {
		return []SwapDevice{}, nil
	}

	var devices []SwapDevice
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		blocks, err1 := strconv.ParseInt(fields[1], 10, 64)
		used, err2 := strconv.ParseInt(fields[2], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("failed to parse swapctl output line: %q", line)
		}

		devices = append(devices, SwapDevice{
			Device:     fields[0],
			Blocks1024: blocks,
			Used:       used,
		})
	}

	return devices, nil
}
