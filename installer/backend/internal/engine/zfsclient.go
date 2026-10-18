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
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/alchemillahq/gzfs"
)

const (
	zpoolBin = "/sbin/zpool"
	zfsBin   = "/sbin/zfs"
)

type zfsRunner struct{ e *Engine }

func (r zfsRunner) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	r.e.logf("$ %s %s", name, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	var combined syncBuffer
	cmd.Stdout = io.MultiWriter(&combined, stdout)
	cmd.Stderr = io.MultiWriter(&combined, stderr)
	err := cmd.Run()
	r.e.logCommandOutput(combined.String())
	return err
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (e *Engine) zfsClient() *gzfs.Client {
	return gzfs.NewClient(gzfs.Options{
		Runner:   zfsRunner{e},
		ZFSBin:   zfsBin,
		ZpoolBin: zpoolBin,
	})
}
