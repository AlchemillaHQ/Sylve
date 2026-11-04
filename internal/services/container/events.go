// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// watchEvents follows podman's event logs so that state changes Sylve didn't
// make itself, such as a crash or a restart policy, still reach the UI.
func (s *Service) watchEvents(ctx context.Context) {
	offsets := map[string]int64{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		logs, _ := filepath.Glob(filepath.Join(runDir, "*", "libpod", "events", "events.log"))
		for _, name := range logs {
			offset, seen := offsets[name]
			if !seen {
				if info, err := os.Stat(name); err == nil {
					offsets[name] = info.Size()
				}
				continue
			}
			var changed map[uint]bool
			offsets[name], changed = readEvents(name, offset)
			for ocid := range changed {
				s.changed(ocid)
			}
		}
	}
}

// readEvents reads the events logged after offset, returning the new offset
// and the containers whose state changed.
func readEvents(name string, offset int64) (int64, map[uint]bool) {
	changed := map[uint]bool{}
	file, err := os.Open(name)
	if err != nil {
		return offset, changed
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == offset {
		return offset, changed
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, changed
	}

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			break
		}
		offset += int64(len(line))
		var event struct {
			Type       string            `json:"Type"`
			Status     string            `json:"Status"`
			Attributes map[string]string `json:"Attributes"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "container" {
			continue
		}
		switch event.Status {
		case "start", "died", "pause", "unpause", "remove":
			if ocid, err := strconv.ParseUint(event.Attributes[ocidLabel], 10, 64); err == nil {
				changed[uint(ocid)] = true
			}
		}
	}
	return offset, changed
}
