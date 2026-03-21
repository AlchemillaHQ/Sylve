// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build linux

package sysctl

import (
	"os"
	"strconv"
	"strings"
)

func nameToPath(name string) string {
	return "/proc/sys/" + strings.ReplaceAll(name, ".", "/")
}

func GetInt64(name string) (int64, error) {
	b, err := GetBytes(name)
	if err != nil {
		return 0, err
	}

	s := strings.TrimSpace(string(b))
	return strconv.ParseInt(s, 10, 64)
}

func GetString(name string) (string, error) {
	b, err := GetBytes(name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func GetBytes(name string) ([]byte, error) {
	return os.ReadFile(nameToPath(name))
}

func Set(name string, value []byte) error {
	return os.WriteFile(nameToPath(name), value, 0644)
}

func SetInt32(name string, value int32) error {
	s := strconv.FormatInt(int64(value), 10)
	return Set(name, []byte(s))
}
