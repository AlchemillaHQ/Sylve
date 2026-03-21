// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

type Attribute struct {
	ID        uint32
	Name      string
	Value     int
	Worst     int
	Threshold int
	RawValue  uint64
	RawBytes  []byte
	TextValue string
	IsText    bool
}

type DeviceInfo struct {
	Device          string
	Protocol        string
	Temperature     int
	PowerOnHours    int
	PowerCycleCount int
	Attributes      []Attribute
}
