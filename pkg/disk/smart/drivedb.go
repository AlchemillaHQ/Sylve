// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package smart

//go:generate go run ./cmd/drivedbgen -in ../../../libbsmart/drivedb/drivedb.h -out drivedb_gen.go

import (
	"regexp"
	"sync"
)

type AttrDef struct {
	Name       string
	Format     string
	HDDOnly    bool
	SSDOnly    bool
	NoNormVal  bool
	NoWorstVal bool
	Increasing bool
}

type FirmwareBug uint32

const (
	FirmwareBugNoLogDir FirmwareBug = 1 << iota
	FirmwareBugSamsung
	FirmwareBugSamsung2
	FirmwareBugSamsung3
	FirmwareBugXErrorLBA
)

type DriveModelEntry struct {
	Family        string
	ModelPattern  string
	FirmwareRegex string
	Warning       string
	FirmwareBugs  FirmwareBug
	AttrOverrides map[uint32]AttrDef
	HDDOnly       bool
	SSDOnly       bool
}

type compiledEntry struct {
	pattern       *regexp.Regexp
	firmwareRe    *regexp.Regexp
	family        string
	warning       string
	firmwareBugs  FirmwareBug
	attrOverrides map[uint32]AttrDef
}

type DriveMatch struct {
	Family        string
	Warning       string
	FirmwareBugs  FirmwareBug
	AttrOverrides map[uint32]AttrDef
}

var compiledDB []compiledEntry

func init() {
	compiledDB = compileDriveDB(modelDB)
}

func compileDriveDB(entries []DriveModelEntry) []compiledEntry {
	compiled := make([]compiledEntry, 0, len(entries))
	for _, entry := range entries {
		re, err := regexp.Compile("^(?:" + entry.ModelPattern + ")$")
		if err != nil {
			continue
		}
		var fwRe *regexp.Regexp
		if entry.FirmwareRegex != "" {
			fwRe, err = regexp.Compile("^(?:" + entry.FirmwareRegex + ")$")
			if err != nil {
				continue
			}
		}
		compiled = append(compiled, compiledEntry{
			pattern:       re,
			firmwareRe:    fwRe,
			family:        entry.Family,
			warning:       entry.Warning,
			firmwareBugs:  entry.FirmwareBugs,
			attrOverrides: entry.AttrOverrides,
		})
	}
	return compiled
}

func LookupAttrDef(id uint32) (AttrDef, bool) {
	d, ok := DefaultAttrDefs[id]
	return d, ok
}

var driveMatchCache sync.Map

type cachedDriveMatch struct {
	match DriveMatch
	ok    bool
}

func LookupModelAttrs(model, firmware string) map[uint32]AttrDef {
	match, ok := LookupDrive(model, firmware)
	if !ok {
		return map[uint32]AttrDef{}
	}
	return match.AttrOverrides
}

func LookupDrive(model, firmware string) (DriveMatch, bool) {
	cacheKey := model + "\x00" + firmware
	if cached, ok := driveMatchCache.Load(cacheKey); ok {
		result := cached.(cachedDriveMatch)
		return result.match, result.ok
	}

	match, ok := lookupDrive(compiledDB, model, firmware)
	driveMatchCache.Store(cacheKey, cachedDriveMatch{match: match, ok: ok})
	return match, ok
}

func lookupModelAttrs(entries []compiledEntry, model, firmware string) map[uint32]AttrDef {
	match, ok := lookupDrive(entries, model, firmware)
	if !ok {
		return map[uint32]AttrDef{}
	}
	return match.AttrOverrides
}

func lookupDrive(entries []compiledEntry, model, firmware string) (DriveMatch, bool) {
	for _, entry := range entries {
		if !entry.pattern.MatchString(model) {
			continue
		}
		if entry.firmwareRe != nil && !entry.firmwareRe.MatchString(firmware) {
			continue
		}
		result := make(map[uint32]AttrDef, len(entry.attrOverrides))
		for id, def := range entry.attrOverrides {
			result[id] = def
		}
		return DriveMatch{
			Family:        entry.family,
			Warning:       entry.warning,
			FirmwareBugs:  entry.firmwareBugs,
			AttrOverrides: result,
		}, true
	}
	return DriveMatch{}, false
}
