// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !darwin && !freebsd && !openbsd && !netbsd && !linux

package sysctl

import "fmt"

var errUnsupported = fmt.Errorf("sysctl is not supported on this platform")

func GetInt64(name string) (int64, error)     { return 0, errUnsupported }
func GetString(name string) (string, error)   { return "", errUnsupported }
func GetBytes(name string) ([]byte, error)    { return nil, errUnsupported }
func Set(name string, value []byte) error     { return errUnsupported }
func SetInt32(name string, value int32) error { return errUnsupported }
