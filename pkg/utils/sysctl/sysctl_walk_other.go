// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !(freebsd && cgo)

package sysctl

import (
	"os/exec"
	"strings"
)

// List is a best-effort fallback used on non-FreeBSD (or non-cgo) builds. It
// shells out to the sysctl(8) binary when available. Writability cannot be
// determined this way, so every entry is reported as read-only.
func List() ([]Tunable, error) {
	out, err := exec.Command("sysctl", "-ae").Output()
	if err != nil {
		return nil, err
	}

	var result []Tunable
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}

		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}

		result = append(result, Tunable{
			Name:     line[:eq],
			Value:    line[eq+1:],
			Type:     "",
			Writable: false,
		})
	}

	return result, nil
}
