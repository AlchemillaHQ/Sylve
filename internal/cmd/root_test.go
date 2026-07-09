// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestAsciiArt(t *testing.T) {
	var buf bytes.Buffer
	AsciiArt(&buf)

	out := buf.String()

	if !strings.Contains(out, "____") {
		t.Fatalf("expected ascii art, got %q", out)
	}

	if !strings.Contains(out, "v"+Version) {
		t.Fatalf("expected version in output, got %q", out)
	}
}

func TestNewRootCommand_Name(t *testing.T) {
	root := NewRootCommand(nil)
	if root.Name != "sylve" {
		t.Fatalf("expected name sylve, got %q", root.Name)
	}
}

func TestNewRootCommand_Flags_Defaults(t *testing.T) {
	var configPath string
	var console bool

	root := NewRootCommand(func(ctx context.Context, c *cli.Command) error {
		configPath = c.String("config")
		console = c.Bool("console")
		return nil
	})

	if err := root.Run(context.Background(), []string{"sylve"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if configPath != "" {
		t.Fatalf("expected empty config path, got %q", configPath)
	}
	if console {
		t.Fatal("expected console false by default")
	}
}

func TestNewRootCommand_Flags_Config(t *testing.T) {
	var configPath string

	root := NewRootCommand(func(ctx context.Context, c *cli.Command) error {
		configPath = c.String("config")
		return nil
	})

	if err := root.Run(context.Background(), []string{"sylve", "--config", "/tmp/test.json"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if configPath != "/tmp/test.json" {
		t.Fatalf("expected /tmp/test.json, got %q", configPath)
	}
}

func TestNewRootCommand_Flags_Console(t *testing.T) {
	var console bool

	root := NewRootCommand(func(ctx context.Context, c *cli.Command) error {
		console = c.Bool("console")
		return nil
	})

	if err := root.Run(context.Background(), []string{"sylve", "--console"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !console {
		t.Fatal("expected console true")
	}
}

func TestNewRootCommand_Subcommand_Notes(t *testing.T) {
	root := NewRootCommand(nil)
	found := false
	for _, sub := range root.Commands {
		if sub.Name == "notes" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected notes subcommand")
	}
}

func TestResolveConfigPath_Explicit(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(cfg, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfg {
		t.Fatalf("expected %q, got %q", cfg, got)
	}
}

func TestResolveConfigPath_ExplicitMissing(t *testing.T) {
	_, err := ResolveConfigPath("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing explicit config, got nil")
	}
}

func TestResolveConfigPath_NoArgs_NoneFound(t *testing.T) {
	tmp := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	_, err = ResolveConfigPath("")
	if err == nil {
		t.Fatal("expected error when no config found, got nil")
	}
}

func TestResolveConfigPath_NoArgs_LocalFound(t *testing.T) {
	tmp := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	if err := os.WriteFile("config.json", []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != DefaultConfigLocal {
		t.Fatalf("expected %q, got %q", DefaultConfigLocal, got)
	}
}
