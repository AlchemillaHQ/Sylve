// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.
//go:build freebsd || darwin

package sysctl

// #include <sys/types.h>
// #include <sys/sysctl.h>
// #include <stdlib.h>
import "C"

import "unsafe"

func GetInt64(name string) (int64, error) {
	var value int64
	oldlen := C.size_t(unsafe.Sizeof(value))

	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))

	_, err := C.sysctlbyname(
		nameC,
		unsafe.Pointer(&value),
		&oldlen,
		nil,
		0,
	)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func GetString(name string) (string, error) {
	b, err := GetBytes(name)
	if err != nil {
		return "", err
	}

	// trim trailing NUL
	if len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}

	return string(b), nil
}

func GetBytes(name string) ([]byte, error) {
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))

	var oldlen C.size_t

	// size query
	_, err := C.sysctlbyname(nameC, nil, &oldlen, nil, 0)
	if err != nil {
		return nil, err
	}

	if oldlen == 0 {
		return nil, nil
	}

	buf := make([]byte, oldlen)

	// value query
	_, err = C.sysctlbyname(
		nameC,
		unsafe.Pointer(&buf[0]),
		&oldlen,
		nil,
		0,
	)
	if err != nil {
		return nil, err
	}

	return buf[:oldlen], nil
}

func Set(name string, value []byte) error {
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))

	var newp unsafe.Pointer
	newlen := C.size_t(len(value))

	if len(value) > 0 {
		newp = unsafe.Pointer(&value[0])
	}

	_, err := C.sysctlbyname(nameC, nil, nil, newp, newlen)
	return err
}

func SetInt32(name string, value int32) error {
	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))

	newlen := C.size_t(unsafe.Sizeof(value))
	newp := unsafe.Pointer(&value)

	_, err := C.sysctlbyname(nameC, nil, nil, newp, newlen)
	return err
}
