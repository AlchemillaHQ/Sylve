// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const (
	DefaultConfigLocal  = "./config.json"
	DefaultConfigSystem = "/usr/local/etc/sylve/config.json"
)

const Version = "0.2.3"

func AsciiArt(w io.Writer) {
	fmt.Fprintln(w, "  ____        _           ")
	fmt.Fprintln(w, " / ___| _   _| |_   _____ ")
	fmt.Fprintln(w, " \\___ \\| | | | \\ \\ / / _ \\")
	fmt.Fprintln(w, "  ___) | |_| | |\\ V /  __/")
	fmt.Fprintln(w, " |____/ \\__, |_| \\_/ \\___|")
	fmt.Fprintln(w, "        |___/              ")
	fmt.Fprintf(w, "\t              v%s\n", Version)
}

type FlagResult struct {
	ConfigPath  string
	REPL        bool
	ShowHelp    bool
	ShowVersion bool
}

func newFlagSet(output io.Writer) (*flag.FlagSet, *string, *bool, *bool, *bool, *bool, *bool) {
	fs := flag.NewFlagSet("sylve", flag.ContinueOnError)
	fs.SetOutput(output)

	configPath := fs.String("config", "", "path to config file (default: ./config.json, then /usr/local/etc/sylve/config.json)")
	help := fs.Bool("help", false, "print help and exit")
	helpShort := fs.Bool("h", false, "print help and exit")
	version := fs.Bool("version", false, "print version and exit")
	versionShort := fs.Bool("v", false, "print version and exit")
	repl := fs.Bool("console", false, "enable interactive command prompt")

	return fs, configPath, help, helpShort, version, versionShort, repl
}

func ParseFlags(args []string) (FlagResult, error) {
	fs, configPath, help, helpShort, version, versionShort, repl := newFlagSet(io.Discard)

	if err := fs.Parse(args); err != nil {
		return FlagResult{}, err
	}

	return FlagResult{
		ConfigPath:  *configPath,
		REPL:        *repl,
		ShowHelp:    *help || *helpShort,
		ShowVersion: *version || *versionShort,
	}, nil
}

// ResolveConfigPath returns the config file path to use, following this priority:
//  1. explicit (from -config flag) — must exist or returns an error
//  2. ./config.json
//  3. /usr/local/etc/sylve/config.json
//
// Returns an error if none of the candidates are found.
func ResolveConfigPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config file not found: %s", explicit)
		}
		return explicit, nil
	}

	for _, candidate := range []string{DefaultConfigLocal, DefaultConfigSystem} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no config file found; tried %q and %q; use -config to specify one",
		DefaultConfigLocal, DefaultConfigSystem)
}

func PrintUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage of sylve:")
	fs, _, _, _, _, _, _ := newFlagSet(w)
	fs.PrintDefaults()
}
