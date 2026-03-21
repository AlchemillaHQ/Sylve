// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build (darwin || freebsd || openbsd || netbsd) && !cgo

package sysctl

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func runSysctl(args ...string) ([]byte, error) {
	cmd := exec.Command("sysctl", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(ee.Stderr))
			if msg != "" {
				return nil, fmt.Errorf("sysctl %s: %s", strings.Join(args, " "), msg)
			}
		}
		return nil, err
	}
	return out, nil
}

func GetInt64(name string) (int64, error) {
	b, err := runSysctl("-n", name)
	if err != nil {
		return 0, err
	}

	s := strings.TrimSpace(string(b))
	return strconv.ParseInt(s, 10, 64)
}

func GetString(name string) (string, error) {
	b, err := runSysctl("-n", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func GetBytes(name string) ([]byte, error) {
	return runSysctl("-n", name)
}

func Set(name string, value []byte) error {
	_, err := runSysctl(fmt.Sprintf("%s=%s", name, string(value)))
	return err
}

func SetInt32(name string, value int32) error {
	s := strconv.FormatInt(int64(value), 10)
	return Set(name, []byte(s))
}
