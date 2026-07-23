// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"
)

const (
	DefaultConfigLocal  = "./config.json"
	DefaultConfigSystem = "/usr/local/etc/sylve/config.json"
)

const Version = "0.2.3"

var Commit = "unknown"

func AsciiArt(w io.Writer) {
	fmt.Fprintln(w, "  ____        _           ")
	fmt.Fprintln(w, " / ___| _   _| |_   _____ ")
	fmt.Fprintln(w, " \\___ \\| | | | \\ \\ / / _ \\")
	fmt.Fprintln(w, "  ___) | |_| | |\\ V /  __/")
	fmt.Fprintln(w, " |____/ \\__, |_| \\_/ \\___|")
	fmt.Fprintln(w, "        |___/              ")
	fmt.Fprintf(w, "\t              v%s\n", Version)
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

var asciiArtBlock = `  ____        _           
 / ___| _   _| |_   _____ 
 \___ \| | | | \ \ / / _ \
  ___) | |_| | |\ V /  __/
 |____/ \__, |_| \_/ \___|
        |___/              
` + "\t              v" + Version

func NewRootCommand(daemonAction func(ctx context.Context, cmd *cli.Command) error) *cli.Command {
	return newRootCommand(daemonAction, func() bool {
		return os.Geteuid() == 0
	})
}

func newRootCommand(daemonAction func(ctx context.Context, cmd *cli.Command) error, isRoot func() bool) *cli.Command {
	cmd := &cli.Command{
		Name:    "sylve",
		Usage:   "FreeBSD management platform",
		Version: Version,
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			if !isRoot() {
				return ctx, fmt.Errorf("root privileges required")
			}
			return ctx, nil
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Usage:   "path to config file (default: ./config.json, then /usr/local/etc/sylve/config.json)",
				Aliases: []string{"c"},
			},
			&cli.BoolFlag{
				Name:    "console",
				Usage:   "enable interactive command prompt",
				Aliases: []string{"con"},
			},
		},
		Commands: []*cli.Command{
			newNotesCommand(),
			newJailsCommand(),
			newVMsCommand(),
			newTasksCommand(),
			newSwitchesCommand(),
			newObjectsCommand(),
			newDownloadsCommand(),
		},
		CustomRootCommandHelpTemplate: asciiArtBlock + "\n\n" + cli.RootCommandHelpTemplate,
	}

	if daemonAction != nil {
		cmd.Action = daemonAction
	}

	return cmd
}
