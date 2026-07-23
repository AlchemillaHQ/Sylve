// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	maxReplHistoryEntries = 500
)

func loadReplHistory(path string) []string {
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var history []string
	for _, command := range strings.Split(string(data), "\n") {
		history, _ = addReplHistory(history, command)
	}
	return history
}

func recordReplHistory(path string, history []string, command string) []string {
	history, added := addReplHistory(history, command)
	if added {
		// Command execution should not depend on history storage succeeding.
		_ = saveReplHistory(path, history)
	}
	return history
}

func addReplHistory(history []string, command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || (len(history) > 0 && history[len(history)-1] == command) {
		return history, false
	}

	history = append(history, command)
	if len(history) > maxReplHistoryEntries {
		history = append([]string(nil), history[len(history)-maxReplHistoryEntries:]...)
	}
	return history, true
}

func saveReplHistory(path string, history []string) error {
	if path == "" {
		return nil
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}

	data := strings.Join(history, "\n")
	if data != "" {
		data += "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
