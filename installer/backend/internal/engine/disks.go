// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
)

type Partition struct {
	Name       string `json:"name,omitempty"`
	Index      int    `json:"index"`
	Type       string `json:"type"`
	Label      string `json:"label,omitempty"`
	StartBytes uint64 `json:"startBytes"`
	SizeBytes  uint64 `json:"sizeBytes"`
}

type Disk struct {
	Name        string      `json:"name"`
	SizeBytes   uint64      `json:"sizeBytes"`
	SectorSize  uint64      `json:"sectorSize"`
	Description string      `json:"description"`
	Serial      string      `json:"serial,omitempty"`
	Rotation    string      `json:"rotationRate"`
	Kind        string      `json:"kind"`
	Boot        bool        `json:"boot"`
	Scheme      string      `json:"scheme,omitempty"`
	Partitions  []Partition `json:"partitions,omitempty"`
}

var (
	geomBlockRe = regexp.MustCompile(`(?m)^Geom name: (\S+)`)
	sizeRe      = regexp.MustCompile(`(?m)^\s+Mediasize: (\d+)`)
	sectorRe    = regexp.MustCompile(`(?m)^\s+Sectorsize: (\d+)`)
	descrRe     = regexp.MustCompile(`(?m)^\s+descr: (.*)$`)
	identRe     = regexp.MustCompile(`(?m)^\s+ident: (.*)$`)
	rotRe       = regexp.MustCompile(`(?m)^\s+rotationrate: (\S+)`)
	schemeRe    = regexp.MustCompile(`(?m)^scheme: (\S+)`)
	providerRe  = regexp.MustCompile(`^\s*\d+\. Name: (\S+)\s*$`)
	partSizeRe  = regexp.MustCompile(`^\s+Mediasize: (\d+)`)
	partLabelRe = regexp.MustCompile(`^\s+label: (.+)$`)
	partTypeRe  = regexp.MustCompile(`^\s+type: (\S+)\s*$`)
	partIndexRe = regexp.MustCompile(`^\s+index: (\d+)\s*$`)
	partStartRe = regexp.MustCompile(`^\s+start: (\d+)\s*$`)
	opticalRe   = regexp.MustCompile(`^cd[0-9]+$`)
)

func Disks() ([]Disk, error) {
	out, err := exec.Command("/sbin/geom", "disk", "list").Output()
	if err != nil {
		return nil, err
	}

	boot := bootDisk()
	disks := []Disk{}
	for _, block := range strings.Split(string(out), "\n\n") {
		m := geomBlockRe.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		name := m[1]
		if strings.HasPrefix(name, "cd") {
			continue
		}
		d := Disk{Name: name, Boot: name == boot, Rotation: "unknown", SectorSize: 512}
		if s := sizeRe.FindStringSubmatch(block); s != nil {
			d.SizeBytes, _ = strconv.ParseUint(s[1], 10, 64)
		}
		if s := sectorRe.FindStringSubmatch(block); s != nil {
			d.SectorSize, _ = strconv.ParseUint(s[1], 10, 64)
		}
		if s := descrRe.FindStringSubmatch(block); s != nil {
			d.Description = strings.TrimSpace(s[1])
		}
		if s := identRe.FindStringSubmatch(block); s != nil {
			d.Serial = strings.TrimSpace(s[1])
		}
		if s := rotRe.FindStringSubmatch(block); s != nil {
			d.Rotation = strings.TrimSpace(s[1])
		}
		d.Kind = diskKind(d.Name, d.Description, d.Rotation)
		d.Scheme, d.Partitions = partitions(name, d.SectorSize)
		disks = append(disks, d)
	}
	if !bootMediaKnown(boot, disks) {
		return nil, fmt.Errorf("cannot identify the disk containing the running installer")
	}
	return disks, nil
}

func bootMediaKnown(boot string, disks []Disk) bool {
	return opticalRe.MatchString(boot) || slices.ContainsFunc(disks, func(disk Disk) bool {
		return disk.Name == boot && disk.Boot
	})
}

func diskKind(name, descr, rotation string) string {
	n := strings.ToLower(name)
	d := strings.ToLower(descr)
	switch {
	case strings.HasPrefix(n, "nvme"), strings.HasPrefix(n, "nvd"), strings.HasPrefix(n, "nda"):
		return "nvme"
	case strings.HasPrefix(n, "vtbd"), strings.HasPrefix(n, "vbd"),
		strings.HasPrefix(n, "xbd"), strings.HasPrefix(n, "hd"):
		return "virtual"
	case strings.HasPrefix(n, "mmcsd"):
		return "sdcard"
	case rotation == "0":
		return "ssd"
	case rotation != "" && rotation != "unknown":
		return "hdd"
	case strings.Contains(d, "ssd"), strings.Contains(d, "solid state"):
		return "ssd"
	case strings.Contains(d, "nvme"):
		return "nvme"
	default:
		return "hdd"
	}
}

func partitions(name string, sectorSize uint64) (string, []Partition) {
	out, err := exec.Command("/sbin/gpart", "list", name).Output()
	if err != nil || len(out) == 0 {
		return "", nil
	}
	return parsePartitions(string(out), sectorSize)
}

func parsePartitions(text string, sectorSize uint64) (string, []Partition) {
	if i := strings.Index(text, "\nGeom name: "); i > 0 {
		text = text[:i]
	}
	if i := strings.Index(text, "\nConsumers:"); i > 0 {
		text = text[:i]
	}
	if sectorSize == 0 {
		sectorSize = 512
	}

	scheme := ""
	if s := schemeRe.FindStringSubmatch(text); s != nil {
		scheme = s[1]
	}

	var parts []Partition
	var current *Partition
	flush := func() {
		if current != nil {
			parts = append(parts, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if m := providerRe.FindStringSubmatch(line); m != nil {
			flush()
			current = &Partition{Name: m[1]}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case partSizeRe.MatchString(line):
			m := partSizeRe.FindStringSubmatch(line)
			current.SizeBytes, _ = strconv.ParseUint(m[1], 10, 64)
		case partLabelRe.MatchString(line):
			m := partLabelRe.FindStringSubmatch(line)
			current.Label = strings.TrimSpace(m[1])
		case partTypeRe.MatchString(line):
			m := partTypeRe.FindStringSubmatch(line)
			current.Type = m[1]
		case partIndexRe.MatchString(line):
			m := partIndexRe.FindStringSubmatch(line)
			current.Index, _ = strconv.Atoi(m[1])
		case partStartRe.MatchString(line):
			m := partStartRe.FindStringSubmatch(line)
			start, _ := strconv.ParseUint(m[1], 10, 64)
			if start <= ^uint64(0)/sectorSize {
				current.StartBytes = start * sectorSize
			}
		}
	}
	flush()
	sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
	return scheme, parts
}

func DiskByName(name string) (Disk, bool) {
	name = strings.TrimPrefix(name, "/dev/")
	disks, err := Disks()
	if err != nil {
		return Disk{}, false
	}
	for _, d := range disks {
		if d.Name == name {
			return d, true
		}
	}
	return Disk{}, false
}

func bootDisk() string {
	if out, err := exec.Command("/bin/kenv", "-q", "vfs.root.mountfrom").Output(); err == nil {
		spec := strings.TrimSpace(string(out))
		kind, rest, ok := strings.Cut(spec, ":")
		if ok {
			if kind == "zfs" {
				if dev := poolVdevDisk(rest); dev != "" {
					return dev
				}
			}
			if dev := resolveDevice(rest); dev != "" {
				return dev
			}
		}
	}

	out, err := exec.Command("/sbin/mount").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != "/" {
			continue
		}
		if strings.Contains(line, "(zfs") {
			if dev := poolVdevDisk(fields[0]); dev != "" {
				return dev
			}
			continue
		}
		if dev := resolveDevice(fields[0]); dev != "" {
			return dev
		}
	}
	return ""
}

func poolVdevDisk(spec string) string {
	pool, _, _ := strings.Cut(strings.TrimSpace(spec), "/")
	if pool == "" {
		return ""
	}
	client := gzfs.NewClient(gzfs.Options{ZpoolBin: zpoolBin})
	status, err := client.Zpool.GetPoolStatus(context.Background(), pool)
	if err != nil {
		return ""
	}
	return leafDiskIn(status.Vdevs)
}

func leafDiskIn(vdevs map[string]*gzfs.ZPoolStatusVDEV) string {
	for _, name := range slices.Sorted(maps.Keys(vdevs)) {
		vdev := vdevs[name]
		if vdev == nil {
			continue
		}
		if len(vdev.Vdevs) > 0 {
			if dev := leafDiskIn(vdev.Vdevs); dev != "" {
				return dev
			}
			continue
		}
		path := vdev.Path
		if path == "" {
			path = vdev.Name
		}
		if strings.HasPrefix(path, "/dev/") {
			if dev := resolveDevice(path); dev != "" {
				return dev
			}
		}
	}
	return ""
}

func resolveDevice(device string) string {
	dev := strings.TrimPrefix(device, "/dev/")
	if strings.Contains(dev, "/") {
		return stripPartition(glabelProvider(dev))
	}
	return stripPartition(dev)
}

func glabelProvider(provider string) string {
	out, err := exec.Command("/sbin/glabel", "status").Output()
	if err != nil {
		return ""
	}
	return parseGlabelStatus(string(out), provider)
}

func parseGlabelStatus(output, provider string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == provider {
			return fields[2]
		}
	}
	return ""
}

func stripPartition(dev string) string {
	dev = strings.TrimSuffix(dev, ".eli")
	if i := strings.LastIndex(dev, "p"); i > 0 {
		if _, err := strconv.Atoi(dev[i+1:]); err == nil {
			dev = dev[:i]
		}
	}
	if dev == "" || strings.ContainsAny(dev, "/:") {
		return ""
	}
	return dev
}
