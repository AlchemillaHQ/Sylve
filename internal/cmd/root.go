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
)

const Version = "0.1.1"

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

func ParseFlags(args []string) (FlagResult, error) {
	fs := flag.NewFlagSet("sylve", flag.ContinueOnError)

	configPath := fs.String("config", "./config.json", "path to config file")
	help := fs.Bool("help", false, "print help and exit")
	version := fs.Bool("version", false, "print version and exit")
	repl := fs.Bool("console", false, "enable interactive command prompt")

	if err := fs.Parse(args); err != nil {
		return FlagResult{}, err
	}

	return FlagResult{
		ConfigPath:  *configPath,
		REPL:        *repl,
		ShowHelp:    *help,
		ShowVersion: *version,
	}, nil
}
