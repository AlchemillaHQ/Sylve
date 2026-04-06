// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailServiceInterfaces

// SupportedBootstrapVersion describes a FreeBSD major/minor version that can
// be bootstrapped via pkgbase. Append entries here when new versions gain
// pkgbase sets (e.g. 16.0 once available).
type SupportedBootstrapVersion struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// SupportedVersions is the authoritative list of FreeBSD versions Sylve can
// bootstrap. Add new entries here as pkgbase sets become available.
var SupportedVersions = []SupportedBootstrapVersion{
	{Major: 15, Minor: 0},
}

// BootstrapTypeSpec describes one pkgbase set within a major/minor version.
type BootstrapTypeSpec struct {
	// Type is the short identifier used in the DB and API ("base" or "minimal").
	Type string
	// Name is the ZFS dataset leaf name, e.g. "15-0-Base".
	Name string
	// Label is the human-readable name shown in the UI.
	Label string
	// PkgSet is the pkgbase package group name passed to `pkg install`.
	PkgSet string
}

// BootstrapTypes lists the pkgbase set variants Sylve supports per version.
var BootstrapTypes = []BootstrapTypeSpec{
	{
		Type:   "base",
		Name:   "%d-%d-Base",
		Label:  "FreeBSD %d Base",
		PkgSet: "FreeBSD-set-base-jail",
	},
	{
		Type:   "minimal",
		Name:   "%d-%d-Minimal",
		Label:  "FreeBSD %d Minimal",
		PkgSet: "FreeBSD-set-minimal-jail",
	},
}

// BootstrapEntry is the complete state of one (pool, version, type) bootstrap
// combination as returned by ListBootstraps.
type BootstrapEntry struct {
	Pool       string `json:"pool"`
	Name       string `json:"name"`
	Label      string `json:"label"`
	Dataset    string `json:"dataset"`
	MountPoint string `json:"mountPoint"`
	Major      int    `json:"major"`
	Minor      int    `json:"minor"`
	Type       string `json:"type"`
	Exists     bool   `json:"exists"`
	Status     string `json:"status"`
	Phase      string `json:"phase"`
	Error      string `json:"error"`
}

// BootstrapRequest is the payload for CreateBootstrap.
type BootstrapRequest struct {
	Pool  string `json:"pool" binding:"required"`
	Major int    `json:"major" binding:"required"`
	Minor int    `json:"minor"`
	Type  string `json:"type" binding:"required"`
}
