// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !linux && !freebsd

package dnssd

import "context"

type stubLinkWatcher struct{}

func newPlatformLinkWatcher() LinkWatcher {
	return &stubLinkWatcher{}
}

func (w *stubLinkWatcher) Subscribe(ctx context.Context) (<-chan LinkUpdate, error) {
	return nil, nil
}
