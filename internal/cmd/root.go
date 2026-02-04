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
	"os"
)

const Version = "0.0.1"

func AsciiArt() {
	fmt.Println("  ____        _           ")
	fmt.Println(" / ___| _   _| |_   _____ ")
	fmt.Println(" \\___ \\| | | | \\ \\ / / _ \\")
	fmt.Println("  ___) | |_| | |\\ V /  __/")
	fmt.Println(" |____/ \\__, |_| \\_/ \\___|")
	fmt.Println("        |___/              ")
	fmt.Printf("\t              v%s\n", Version)
}

func ParseFlags() (string, bool) {
	configPath := flag.String("config", "./config.json", "path to config file")
	help := flag.Bool("help", false, "print help and exit")
	version := flag.Bool("version", false, "print version and exit")
	repl := flag.Bool("repl", false, "enable interactive command prompt")

	flag.Parse()

	if *version {
		println(Version)
		os.Exit(0)
	}

	if *help {
		flag.Usage()
		os.Exit(0)
	}

	return *configPath, *repl
}
