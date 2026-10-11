// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
)

func TestKeytabParserRejectsMalformedContainers(t *testing.T) {
	valid := testKeytab("cifs/sylve.example.com@EXAMPLE.COM", 258, 7)
	entries, err := parseKeytab(valid)
	if err != nil || len(entries) != 1 || entries[0].kvno != 258 || entries[0].principal != "cifs/sylve.example.com@EXAMPLE.COM" {
		t.Fatalf("version-2 parse: %+v %v", entries, err)
	}
	for i := 0; i < len(valid); i++ {
		if i == 2 {
			continue
		} // An empty v2 keytab is valid as a merge baseline.
		if _, err := parseKeytab(valid[:i]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted truncated entry at %d: %v", i, err)
		}
	}
	for _, malformed := range [][]byte{{5, 1}, {5, 2, 0x80, 0, 0, 0}, append(valid, 0), testKeytab("bad\nuser@EXAMPLE.COM", 1, 1), testKeytab("bad\\user@EXAMPLE.COM", 1, 1)} {
		if _, err := parseKeytab(malformed); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted malformed keytab: %v", err)
		}
	}
	var hole bytes.Buffer
	hole.Write([]byte{5, 2})
	_ = binary.Write(&hole, binary.BigEndian, int32(-4))
	hole.Write([]byte{0, 0, 0, 0})
	hole.Write(valid[2:])
	if rows, err := parseKeytab(hole.Bytes()); err != nil || len(rows) != 1 {
		t.Fatalf("deleted slots: %+v %v", rows, err)
	}
}

func TestKeytabMergeKeepsVersionsAndReplacesMatchingEntries(t *testing.T) {
	first := testKeytab("host/sylve@EXAMPLE.COM", 1, 1)
	second := testKeytab("host/sylve@EXAMPLE.COM", 2, 2)
	replacement := testKeytab("host/sylve@EXAMPLE.COM", 1, 3)
	data, err := mergeKeytabs(first, second, replacement)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseKeytab(data)
	if err != nil || len(entries) != 2 || len(keytabPrincipals(entries)) != 1 {
		t.Fatalf("merge: %+v %v", entries, err)
	}
	if !bytes.Contains(entries[0].raw, bytes.Repeat([]byte{3}, 32)) || entries[1].kvno != 2 {
		t.Fatal("did not replace exact tuple or discarded another version")
	}
}

func TestKeytabParserPreservesRecordExtensionsAndPadding(t *testing.T) {
	valid := testKeytab("host/sylve.example.com@EXAMPLE.COM", 258, 7)
	for _, test := range []struct {
		name     string
		withKVNO bool
		extra    []byte
		kvno     uint32
	}{
		{"Heimdal flags", true, []byte{0, 0, 0, 0}, 258},
		{"record padding", true, []byte{1, 2, 3, 4, 5}, 258},
		{"8-bit KVNO", false, nil, 2},
		{"short padding", false, []byte{0, 0}, 2},
		{"zero 32-bit KVNO", false, []byte{0, 0, 0, 0}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := slices.Clone(valid[6:])
			if !test.withKVNO {
				raw = raw[:len(raw)-4]
			}
			raw = append(raw, test.extra...)
			var file bytes.Buffer
			file.Write([]byte{5, 2})
			_ = binary.Write(&file, binary.BigEndian, int32(len(raw)))
			file.Write(raw)
			entries, err := parseKeytab(file.Bytes())
			if err != nil || len(entries) != 1 || entries[0].kvno != test.kvno {
				t.Fatalf("record parse: %+v %v", entries, err)
			}
			merged, err := mergeKeytabs(file.Bytes())
			if err != nil || !bytes.Equal(merged, file.Bytes()) {
				t.Fatalf("merge changed record extensions: %v", err)
			}
		})
	}
}

func FuzzKeytabParser(f *testing.F) {
	f.Add(testKeytab("cifs/sylve.example.com@EXAMPLE.COM", 1, 7))
	f.Add([]byte{5, 2})
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = parseKeytab(data) })
}
