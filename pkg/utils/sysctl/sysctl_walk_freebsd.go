// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd && cgo

package sysctl

/*
#include <sys/types.h>
#include <sys/sysctl.h>
#include <stdlib.h>
*/
import "C"

import (
	"bytes"
	"encoding/hex"
	"math"
	"strconv"
	"unsafe"
)

const intSize = C.int(unsafe.Sizeof(C.int(0)))

// sysctlMeta queries one of the magic CTL_SYSCTL_* sub-oids (NAME, OIDFMT, ...)
// for the given oid and writes the result into out, returning the byte length.
func sysctlMeta(magic C.int, oid []C.int, out []byte) (int, error) {
	q := make([]C.int, 0, len(oid)+2)
	q = append(q, C.int(C.CTL_SYSCTL), magic)
	q = append(q, oid...)

	blen := C.size_t(len(out))
	if r, err := C.sysctl(&q[0], C.u_int(len(q)), unsafe.Pointer(&out[0]), &blen, nil, 0); r != 0 {
		return 0, err
	}

	return int(blen), nil
}

// sysctlRaw reads the raw value bytes of the given oid.
func sysctlRaw(oid []C.int) ([]byte, error) {
	var blen C.size_t
	if r, err := C.sysctl(&oid[0], C.u_int(len(oid)), nil, &blen, nil, 0); r != 0 {
		return nil, err
	}
	if blen == 0 {
		return nil, nil
	}

	buf := make([]byte, blen)
	if r, err := C.sysctl(&oid[0], C.u_int(len(oid)), unsafe.Pointer(&buf[0]), &blen, nil, 0); r != 0 {
		return nil, err
	}

	return buf[:blen], nil
}

func oidName(oid []C.int) string {
	buf := make([]byte, 1024)
	n, err := sysctlMeta(C.int(C.CTL_SYSCTL_NAME), oid, buf)
	if err != nil || n == 0 {
		return ""
	}

	s := buf[:n]
	if i := bytes.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}

	return string(s)
}

func oidFmt(oid []C.int) (uint32, string, bool) {
	buf := make([]byte, 1024)
	n, err := sysctlMeta(C.int(C.CTL_SYSCTL_OIDFMT), oid, buf)
	if err != nil || n < 4 {
		return 0, "", false
	}

	kind := *(*uint32)(unsafe.Pointer(&buf[0]))

	format := ""
	if n > 4 {
		f := buf[4:n]
		if i := bytes.IndexByte(f, 0); i >= 0 {
			f = f[:i]
		}
		format = string(f)
	}

	return kind, format, true
}

// formatKelvin renders a FreeBSD "IK" temperature sysctl (deci-Kelvin by
// default, or 10^n Kelvin when a precision digit follows) as degrees Celsius.
func formatKelvin(value int32, format string) string {
	prec := 1
	if len(format) > 2 && format[2] >= '0' && format[2] <= '9' {
		prec = int(format[2] - '0')
	}

	celsius := float64(value)/math.Pow10(prec) - 273.15

	return strconv.FormatFloat(celsius, 'f', 1, 64) + "C"
}

func typeName(ctype uint32) string {
	switch ctype {
	case uint32(C.CTLTYPE_NODE):
		return "node"
	case uint32(C.CTLTYPE_INT):
		return "int"
	case uint32(C.CTLTYPE_STRING):
		return "string"
	case uint32(C.CTLTYPE_S64):
		return "int64"
	case uint32(C.CTLTYPE_OPAQUE):
		return "opaque"
	case uint32(C.CTLTYPE_UINT):
		return "uint"
	case uint32(C.CTLTYPE_LONG):
		return "long"
	case uint32(C.CTLTYPE_ULONG):
		return "ulong"
	case uint32(C.CTLTYPE_U64):
		return "uint64"
	case uint32(C.CTLTYPE_U8):
		return "uint8"
	case uint32(C.CTLTYPE_U16):
		return "uint16"
	case uint32(C.CTLTYPE_S8):
		return "int8"
	case uint32(C.CTLTYPE_S16):
		return "int16"
	case uint32(C.CTLTYPE_S32):
		return "int32"
	case uint32(C.CTLTYPE_U32):
		return "uint32"
	}

	return "unknown"
}

func readValue(oid []C.int, ctype uint32, format string) string {
	if ctype == uint32(C.CTLTYPE_OPAQUE) {
		return "(opaque)"
	}

	raw, err := sysctlRaw(oid)
	if err != nil || len(raw) == 0 {
		return ""
	}

	switch ctype {
	case uint32(C.CTLTYPE_STRING):
		s := raw
		if i := bytes.IndexByte(s, 0); i >= 0 {
			s = s[:i]
		}
		return string(s)
	case uint32(C.CTLTYPE_INT), uint32(C.CTLTYPE_S32):
		if len(raw) >= 4 {
			v := *(*int32)(unsafe.Pointer(&raw[0]))
			if ctype == uint32(C.CTLTYPE_INT) && len(format) >= 2 && format[0] == 'I' && format[1] == 'K' {
				return formatKelvin(v, format)
			}
			return strconv.FormatInt(int64(v), 10)
		}
	case uint32(C.CTLTYPE_UINT), uint32(C.CTLTYPE_U32):
		if len(raw) >= 4 {
			return strconv.FormatUint(uint64(*(*uint32)(unsafe.Pointer(&raw[0]))), 10)
		}
	case uint32(C.CTLTYPE_LONG):
		if len(raw) >= int(unsafe.Sizeof(C.long(0))) {
			return strconv.FormatInt(int64(*(*C.long)(unsafe.Pointer(&raw[0]))), 10)
		}
	case uint32(C.CTLTYPE_ULONG):
		if len(raw) >= int(unsafe.Sizeof(C.ulong(0))) {
			return strconv.FormatUint(uint64(*(*C.ulong)(unsafe.Pointer(&raw[0]))), 10)
		}
	case uint32(C.CTLTYPE_S64):
		if len(raw) >= 8 {
			return strconv.FormatInt(*(*int64)(unsafe.Pointer(&raw[0])), 10)
		}
	case uint32(C.CTLTYPE_U64):
		if len(raw) >= 8 {
			return strconv.FormatUint(*(*uint64)(unsafe.Pointer(&raw[0])), 10)
		}
	case uint32(C.CTLTYPE_U8):
		return strconv.FormatUint(uint64(raw[0]), 10)
	case uint32(C.CTLTYPE_S8):
		return strconv.FormatInt(int64(int8(raw[0])), 10)
	case uint32(C.CTLTYPE_U16):
		if len(raw) >= 2 {
			return strconv.FormatUint(uint64(*(*uint16)(unsafe.Pointer(&raw[0]))), 10)
		}
	case uint32(C.CTLTYPE_S16):
		if len(raw) >= 2 {
			return strconv.FormatInt(int64(*(*int16)(unsafe.Pointer(&raw[0]))), 10)
		}
	}

	if len(raw) > 32 {
		raw = raw[:32]
	}

	return hex.EncodeToString(raw)
}

// List walks the entire sysctl MIB tree and returns every leaf tunable along
// with its current value, type and whether the kernel allows writes to it.
func List() ([]Tunable, error) {
	result := make([]Tunable, 0, 1024)
	maxLen := int(C.CTL_MAXNAME) + 2

	oid := []C.int{1} // CTL_KERN, the first real top-level node

	for {
		q := make([]C.int, 0, len(oid)+2)
		q = append(q, C.int(C.CTL_SYSCTL), C.int(C.CTL_SYSCTL_NEXT))
		q = append(q, oid...)

		next := make([]C.int, maxLen)
		nlen := C.size_t(len(next)) * C.size_t(intSize)
		if r, _ := C.sysctl(&q[0], C.u_int(len(q)), unsafe.Pointer(&next[0]), &nlen, nil, 0); r != 0 {
			break
		}

		n := int(nlen) / int(intSize)
		if n == 0 {
			break
		}

		cur := append([]C.int(nil), next[:n]...)
		oid = cur

		name := oidName(cur)
		if name == "" {
			continue
		}

		kind, format, ok := oidFmt(cur)
		if !ok {
			continue
		}

		ctype := kind & uint32(C.CTLTYPE)
		if ctype == uint32(C.CTLTYPE_NODE) {
			continue
		}

		result = append(result, Tunable{
			Name:     name,
			Value:    readValue(cur, ctype, format),
			Type:     typeName(ctype),
			Writable: kind&uint32(C.CTLFLAG_WR) != 0,
		})
	}

	return result, nil
}
