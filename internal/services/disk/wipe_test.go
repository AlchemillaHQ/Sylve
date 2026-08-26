// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	diskUtils "github.com/alchemillahq/sylve/pkg/disk"
)

type edgeWriteCall struct {
	offset int64
	size   int
}

type memoryDiskEdgeWriter struct {
	data       []byte
	writeCalls []edgeWriteCall
	writeFn    func(call int, buffer []byte, offset int64) (int, error)
	syncCalls  int
	syncErr    error
}

type memoryDiskWriteCloser struct {
	*memoryDiskEdgeWriter
	closeCalls int
	closeErr   error
}

func (w *memoryDiskEdgeWriter) WriteAt(buffer []byte, offset int64) (int, error) {
	call := len(w.writeCalls)
	w.writeCalls = append(w.writeCalls, edgeWriteCall{offset: offset, size: len(buffer)})

	if w.writeFn != nil {
		return w.writeFn(call, buffer, offset)
	}

	return copy(w.data[int(offset):], buffer), nil
}

func (w *memoryDiskEdgeWriter) Sync() error {
	w.syncCalls++
	return w.syncErr
}

func (w *memoryDiskWriteCloser) Close() error {
	w.closeCalls++
	return w.closeErr
}

func TestWipeDiskEdges(t *testing.T) {
	const diskSize = 4 * diskEdgeWipeSize

	writer := &memoryDiskEdgeWriter{
		data: bytes.Repeat([]byte{0xa5}, int(diskSize)),
	}

	if err := wipeDiskEdges(writer, diskSize); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(writer.writeCalls) != 2 {
		t.Fatalf("write calls = %d; want 2", len(writer.writeCalls))
	}
	if got := writer.writeCalls[0]; got.offset != 0 || got.size != int(diskEdgeWipeSize) {
		t.Fatalf("primary write = %+v; want offset 0 and size %d", got, diskEdgeWipeSize)
	}
	if got := writer.writeCalls[1]; got.offset != int64(diskSize-diskEdgeWipeSize) || got.size != int(diskEdgeWipeSize) {
		t.Fatalf(
			"backup write = %+v; want offset %d and size %d",
			got,
			diskSize-diskEdgeWipeSize,
			diskEdgeWipeSize,
		)
	}
	if writer.syncCalls != 1 {
		t.Fatalf("sync calls = %d; want 1", writer.syncCalls)
	}

	zeroes := make([]byte, diskEdgeWipeSize)
	if !bytes.Equal(writer.data[:diskEdgeWipeSize], zeroes) {
		t.Fatal("primary disk edge was not zeroed")
	}
	if !bytes.Equal(writer.data[diskSize-diskEdgeWipeSize:], zeroes) {
		t.Fatal("backup disk edge was not zeroed")
	}

	middle := writer.data[diskEdgeWipeSize : diskSize-diskEdgeWipeSize]
	if !bytes.Equal(middle, bytes.Repeat([]byte{0xa5}, len(middle))) {
		t.Fatal("bytes outside the disk edges were modified")
	}
}

func TestWipeDiskEdgesAcceptsMinimumSize(t *testing.T) {
	writer := &memoryDiskEdgeWriter{
		data: make([]byte, minimumGPTWipeSize),
	}

	if err := wipeDiskEdges(writer, minimumGPTWipeSize); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(writer.writeCalls) != 2 {
		t.Fatalf("write calls = %d; want 2", len(writer.writeCalls))
	}
	if writer.writeCalls[1].offset != int64(diskEdgeWipeSize) {
		t.Fatalf("backup offset = %d; want %d", writer.writeCalls[1].offset, diskEdgeWipeSize)
	}
	if writer.syncCalls != 1 {
		t.Fatalf("sync calls = %d; want 1", writer.syncCalls)
	}
}

func TestWipeDiskEdgesRejectsInvalidSizeBeforeWriting(t *testing.T) {
	tests := []struct {
		name     string
		diskSize uint64
		wantErr  error
	}{
		{
			name:     "too small",
			diskSize: minimumGPTWipeSize - 1,
			wantErr:  errDiskTooSmallForGPTWipe,
		},
		{
			name:     "too large for WriteAt",
			diskSize: maximumWriteAtRange + 1,
			wantErr:  errDiskTooLargeForGPTWipe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &memoryDiskEdgeWriter{}

			err := wipeDiskEdges(writer, tt.diskSize)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v; want %v", err, tt.wantErr)
			}
			if len(writer.writeCalls) != 0 {
				t.Fatalf("write calls = %d; want 0", len(writer.writeCalls))
			}
			if writer.syncCalls != 0 {
				t.Fatalf("sync calls = %d; want 0", writer.syncCalls)
			}
		})
	}
}

func TestWipeDiskEdgesPropagatesWriteFailures(t *testing.T) {
	writeErr := errors.New("write failed")

	tests := []struct {
		name             string
		failingCall      int
		result           int
		writeErr         error
		wantWrites       int
		wantErrorContain string
		wantCause        error
	}{
		{
			name:             "primary write error",
			failingCall:      0,
			writeErr:         writeErr,
			wantWrites:       1,
			wantErrorContain: "error wiping primary GPT",
			wantCause:        writeErr,
		},
		{
			name:             "primary short write",
			failingCall:      0,
			result:           int(diskEdgeWipeSize) - 1,
			wantWrites:       1,
			wantErrorContain: "error wiping primary GPT",
			wantCause:        io.ErrShortWrite,
		},
		{
			name:             "backup write error",
			failingCall:      1,
			writeErr:         writeErr,
			wantWrites:       2,
			wantErrorContain: "error wiping backup GPT",
			wantCause:        writeErr,
		},
		{
			name:             "backup short write",
			failingCall:      1,
			result:           int(diskEdgeWipeSize) - 1,
			wantWrites:       2,
			wantErrorContain: "error wiping backup GPT",
			wantCause:        io.ErrShortWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &memoryDiskEdgeWriter{
				data: make([]byte, 4*diskEdgeWipeSize),
			}
			writer.writeFn = func(call int, buffer []byte, offset int64) (int, error) {
				if call == tt.failingCall {
					return tt.result, tt.writeErr
				}
				return copy(writer.data[int(offset):], buffer), nil
			}

			err := wipeDiskEdges(writer, uint64(len(writer.data)))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, tt.wantCause) {
				t.Fatalf("error = %v; want cause %v", err, tt.wantCause)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tt.wantErrorContain)) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErrorContain)
			}
			if len(writer.writeCalls) != tt.wantWrites {
				t.Fatalf("write calls = %d; want %d", len(writer.writeCalls), tt.wantWrites)
			}
			if writer.syncCalls != 0 {
				t.Fatalf("sync calls = %d; want 0", writer.syncCalls)
			}
		})
	}
}

func TestWipeDiskEdgesPropagatesSyncFailure(t *testing.T) {
	syncErr := errors.New("sync failed")
	writer := &memoryDiskEdgeWriter{
		data:    make([]byte, 4*diskEdgeWipeSize),
		syncErr: syncErr,
	}

	err := wipeDiskEdges(writer, uint64(len(writer.data)))
	if !errors.Is(err, syncErr) {
		t.Fatalf("error = %v; want sync failure", err)
	}
	if len(writer.writeCalls) != 2 {
		t.Fatalf("write calls = %d; want 2", len(writer.writeCalls))
	}
	if writer.syncCalls != 1 {
		t.Fatalf("sync calls = %d; want 1", writer.syncCalls)
	}
}

func TestResolveWholeDiskSize(t *testing.T) {
	const mediaSize = int64(4 * diskEdgeWipeSize)

	tests := []struct {
		name           string
		device         string
		disks          []diskServiceInterfaces.DiskInfo
		checkErr       error
		listErr        error
		wantSize       uint64
		wantErrContain string
		wantCheckCalls int
		wantListCalls  int
	}{
		{
			name:   "primary device name",
			device: "/dev/nda0",
			disks: []diskServiceInterfaces.DiskInfo{
				{Name: "nda0", MediaSize: mediaSize},
			},
			wantSize:       uint64(mediaSize),
			wantCheckCalls: 1,
			wantListCalls:  1,
		},
		{
			name:   "GEOM alias",
			device: "/dev/diskid/DISK-example",
			disks: []diskServiceInterfaces.DiskInfo{
				{Name: "nda0", Aliases: []string{"diskid/DISK-example"}, MediaSize: mediaSize},
			},
			wantSize:       uint64(mediaSize),
			wantCheckCalls: 1,
			wantListCalls:  1,
		},
		{
			name:           "malformed path",
			device:         "/dev/../etc/passwd",
			wantErrContain: "invalid disk device path",
		},
		{
			name:           "relative path",
			device:         "nda0",
			wantErrContain: "invalid disk device path",
		},
		{
			name:   "partition is not a whole disk",
			device: "/dev/nda0p1",
			disks: []diskServiceInterfaces.DiskInfo{
				{Name: "nda0", MediaSize: mediaSize},
			},
			wantErrContain: "not a top-level disk provider",
			wantCheckCalls: 1,
			wantListCalls:  1,
		},
		{
			name:           "device validation fails",
			device:         "/dev/nda0",
			checkErr:       errors.New("not a device"),
			wantErrContain: "not a device",
			wantCheckCalls: 1,
		},
		{
			name:           "inventory fails",
			device:         "/dev/nda0",
			listErr:        errors.New("GEOM unavailable"),
			wantErrContain: "failed to list disk providers",
			wantCheckCalls: 1,
			wantListCalls:  1,
		},
		{
			name:   "invalid media size",
			device: "/dev/nda0",
			disks: []diskServiceInterfaces.DiskInfo{
				{Name: "nda0", MediaSize: 0},
			},
			wantErrContain: "invalid media size",
			wantCheckCalls: 1,
			wantListCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkCalls := 0
			listCalls := 0

			size, err := resolveWholeDiskSize(
				tt.device,
				func(string) error {
					checkCalls++
					return tt.checkErr
				},
				func() ([]diskServiceInterfaces.DiskInfo, error) {
					listCalls++
					return tt.disks, tt.listErr
				},
			)

			if size != tt.wantSize {
				t.Fatalf("size = %d; want %d", size, tt.wantSize)
			}
			if tt.wantErrContain == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErrContain) {
				t.Fatalf("error = %v; want error containing %q", err, tt.wantErrContain)
			}
			if checkCalls != tt.wantCheckCalls {
				t.Fatalf("device validation calls = %d; want %d", checkCalls, tt.wantCheckCalls)
			}
			if listCalls != tt.wantListCalls {
				t.Fatalf("inventory calls = %d; want %d", listCalls, tt.wantListCalls)
			}
		})
	}
}

func TestDestroyPartitionTableWipesAfterGpartResult(t *testing.T) {
	tests := []struct {
		name       string
		destroyErr error
	}{
		{name: "gpart success"},
		{
			name:       "no GEOM partition table",
			destroyErr: fmt.Errorf("%w: nda0", diskUtils.ErrNoPartitionTable),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destroyCalled := false
			writer := &memoryDiskWriteCloser{
				memoryDiskEdgeWriter: &memoryDiskEdgeWriter{
					data: make([]byte, 4*diskEdgeWipeSize),
				},
			}

			err := destroyPartitionTable(
				"/dev/nda0",
				func(string) (uint64, error) {
					return uint64(len(writer.data)), nil
				},
				func(device string) error {
					if device != "/dev/nda0" {
						t.Fatalf("destroy device = %q; want /dev/nda0", device)
					}
					destroyCalled = true
					return tt.destroyErr
				},
				func(device string) (diskWriteCloser, error) {
					if !destroyCalled {
						t.Fatal("disk was opened before gpart completed")
					}
					if device != "/dev/nda0" {
						t.Fatalf("open device = %q; want /dev/nda0", device)
					}
					return writer, nil
				},
			)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(writer.writeCalls) != 2 {
				t.Fatalf("write calls = %d; want 2", len(writer.writeCalls))
			}
			if writer.syncCalls != 1 {
				t.Fatalf("sync calls = %d; want 1", writer.syncCalls)
			}
			if writer.closeCalls != 1 {
				t.Fatalf("close calls = %d; want 1", writer.closeCalls)
			}
		})
	}
}

func TestDestroyPartitionTableStopsBeforeRawWipe(t *testing.T) {
	resolveErr := errors.New("resolution failed")
	busyErr := errors.New("Device busy")
	unexpectedErr := errors.New("gpart failed")
	openErr := errors.New("open failed")

	tests := []struct {
		name            string
		diskSize        uint64
		resolveErr      error
		destroyErr      error
		openErr         error
		wantDestroyCall bool
		wantOpenCall    bool
		wantCause       error
	}{
		{
			name:       "resolution failure",
			resolveErr: resolveErr,
			wantCause:  resolveErr,
		},
		{
			name:      "disk too small",
			diskSize:  minimumGPTWipeSize - 1,
			wantCause: errDiskTooSmallForGPTWipe,
		},
		{
			name:            "busy disk",
			diskSize:        minimumGPTWipeSize,
			destroyErr:      busyErr,
			wantDestroyCall: true,
			wantCause:       busyErr,
		},
		{
			name:            "unexpected gpart failure",
			diskSize:        minimumGPTWipeSize,
			destroyErr:      unexpectedErr,
			wantDestroyCall: true,
			wantCause:       unexpectedErr,
		},
		{
			name:            "raw open failure",
			diskSize:        minimumGPTWipeSize,
			openErr:         openErr,
			wantDestroyCall: true,
			wantOpenCall:    true,
			wantCause:       openErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destroyCalled := false
			openCalled := false
			writer := &memoryDiskWriteCloser{
				memoryDiskEdgeWriter: &memoryDiskEdgeWriter{
					data: make([]byte, minimumGPTWipeSize),
				},
			}

			err := destroyPartitionTable(
				"/dev/nda0",
				func(string) (uint64, error) {
					return tt.diskSize, tt.resolveErr
				},
				func(string) error {
					destroyCalled = true
					return tt.destroyErr
				},
				func(string) (diskWriteCloser, error) {
					openCalled = true
					return writer, tt.openErr
				},
			)

			if !errors.Is(err, tt.wantCause) {
				t.Fatalf("error = %v; want cause %v", err, tt.wantCause)
			}
			if destroyCalled != tt.wantDestroyCall {
				t.Fatalf("destroy called = %t; want %t", destroyCalled, tt.wantDestroyCall)
			}
			if openCalled != tt.wantOpenCall {
				t.Fatalf("open called = %t; want %t", openCalled, tt.wantOpenCall)
			}
			if len(writer.writeCalls) != 0 {
				t.Fatalf("write calls = %d; want 0", len(writer.writeCalls))
			}
			if writer.syncCalls != 0 {
				t.Fatalf("sync calls = %d; want 0", writer.syncCalls)
			}
			if writer.closeCalls != 0 {
				t.Fatalf("close calls = %d; want 0", writer.closeCalls)
			}
		})
	}
}

func TestDestroyPartitionTableClosesAfterWipeFailure(t *testing.T) {
	syncErr := errors.New("sync failed")
	closeErr := errors.New("close failed")
	writer := &memoryDiskWriteCloser{
		memoryDiskEdgeWriter: &memoryDiskEdgeWriter{
			data:    make([]byte, minimumGPTWipeSize),
			syncErr: syncErr,
		},
		closeErr: closeErr,
	}

	err := destroyPartitionTable(
		"/dev/nda0",
		func(string) (uint64, error) {
			return uint64(len(writer.data)), nil
		},
		func(string) error {
			return nil
		},
		func(string) (diskWriteCloser, error) {
			return writer, nil
		},
	)

	if !errors.Is(err, syncErr) {
		t.Fatalf("error = %v; want sync failure", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("error = %v; want close failure", err)
	}
	if writer.closeCalls != 1 {
		t.Fatalf("close calls = %d; want 1", writer.closeCalls)
	}
}
