// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

func shouldStartLocalSylve(consoleEnabled bool, attach func() (bool, error)) (bool, error) {
	if !consoleEnabled {
		return true, nil
	}

	attached, err := attach()
	if err != nil {
		return false, err
	}

	return !attached, nil
}
