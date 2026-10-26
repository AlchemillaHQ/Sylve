// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"syscall"
)

// NFSKeytabState checks installed credentials without exposing keys or creating
// another Kerberos configuration owner. NFS can withdraw stale serving state
// when Directory Services changes the selected service keys or configuration.
func (s *Service) NFSKeytabState(ctx context.Context, principal string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	read := func(path string, secret bool) ([]byte, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Size() > maxKeytabBytes || info.Mode().Perm()&0022 != 0 || (secret && info.Mode().Perm()&0077 != 0) {
			return nil, fmt.Errorf("unsafe Kerberos configuration file")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return nil, fmt.Errorf("Kerberos configuration changed while opening")
		}
		data, err := io.ReadAll(io.LimitReader(file, maxKeytabBytes+1))
		if len(data) > maxKeytabBytes {
			return nil, fmt.Errorf("Kerberos configuration exceeds 1 MiB")
		}
		return data, err
	}
	keytab, err := read(s.keytabPath, true)
	if err != nil {
		return "", fmt.Errorf("%w: installed NFS keytab is unavailable", ErrUnavailable)
	}
	entries, err := parseKeytab(keytab)
	if err != nil {
		return "", err
	}
	config, err := read(s.krbPath, false)
	if err != nil {
		return "", fmt.Errorf("%w: installed Kerberos configuration is unavailable", ErrUnavailable)
	}
	digest := sha256.New()
	configHash := sha256.Sum256(config)
	digest.Write(configHash[:])
	found := false
	for _, entry := range entries {
		if entry.principal == principal {
			found = true
			keyHash := sha256.Sum256(entry.raw)
			digest.Write(keyHash[:])
		}
	}
	if !found {
		return "", fmt.Errorf("%w: install the NFS principal in Directory Services Keytabs first", ErrUnavailable)
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}
