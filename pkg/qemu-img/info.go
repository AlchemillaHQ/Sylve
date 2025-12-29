package qemuimg

import (
	"encoding/json"
	"fmt"
)

type ImageChild struct {
	Name string     `json:"name"`
	Info *ImageInfo `json:"info"`
}

type FormatSpecific struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

type ImageInfo struct {
	Children       []ImageChild    `json:"children"`
	VirtualSize    int64           `json:"virtual-size"`
	Filename       string          `json:"filename"`
	Format         string          `json:"format"`
	ActualSize     int64           `json:"actual-size"`
	ClusterSize    int64           `json:"cluster-size"`
	DirtyFlag      bool            `json:"dirty-flag"`
	FormatSpecific *FormatSpecific `json:"format-specific"`
}

func (q *qimg) Info(path string) (*ImageInfo, error) {
	out, err := q.run(nil, nil, "qemu-img", "info", "--output=json", path)
	if err != nil {
		return nil, err
	}

	var info ImageInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return nil, fmt.Errorf("qemu-img: failed to parse info JSON: %w", err)
	}

	return &info, nil
}

func (q *qimg) InfoBackingChain(path string) ([]*ImageInfo, error) {
	out, err := q.run(nil, nil, "qemu-img", "info", "--backing-chain", "--output=json", path)
	if err != nil {
		return nil, err
	}

	var infos []*ImageInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		return nil, fmt.Errorf("qemu-img: failed to parse backing-chain JSON: %w", err)
	}

	return infos, nil
}
