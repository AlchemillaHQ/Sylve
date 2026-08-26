// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConsumeJSON(t *testing.T) {
	input := strings.Join([]string{
		`{"Action":"output","Package":"example/a","Test":"TestFast","Output":"=== RUN   TestFast\n"}`,
		`{"Action":"pass","Package":"example/a","Test":"TestFast","Elapsed":0.25}`,
		`{"Action":"pass","Package":"example/a","Test":"TestFast/case","Elapsed":0.1}`,
		`{"Action":"pass","Package":"example/a","Elapsed":0.5}`,
	}, "\n") + "\n"
	var raw, human bytes.Buffer
	var result totals
	if err := consumeJSON(strings.NewReader(input), &raw, &human, &result); err != nil {
		t.Fatal(err)
	}
	if raw.String() != input || human.String() != "=== RUN   TestFast\n" {
		t.Fatalf("raw=%q human=%q", raw.String(), human.String())
	}
	if result.events != 4 || result.testCounts.pass != 2 || result.packageCounts.pass != 1 || len(result.tests) != 1 {
		t.Fatalf("unexpected totals: %#v", result)
	}
}

func TestFormatReport(t *testing.T) {
	result := totals{
		packages:       []timing{{"fast", "pass", 1}, {"slow", "fail", 5}},
		tests:          []timing{{"fast: TestFast", "pass", .5}, {"slow: TestSlow", "fail", 4}},
		packageCounts:  counts{pass: 1, fail: 1},
		testCounts:     counts{pass: 1, skip: 2, fail: 1},
		packageElapsed: 6,
		events:         12,
	}
	report := formatReport("unit", "go test -json -short ./...", 1, 12*time.Second, 4*time.Second, true, result, nil)
	for _, want := range []string{
		"Test duration report: unit",
		"Go test wall time | 12s",
		"Setup time | 4s",
		"Package elapsed sum | 6.000s",
		"1 / 2 / 1",
		"Go build cache restored | true",
		"| 1 | 5.000s | fail | `slow` |",
		"| 1 | 4.000s | fail | `slow: TestSlow` |",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
}

func TestRunPreservesFailureAndArtifacts(t *testing.T) {
	dir := t.TempDir()
	script := `printf '%s\n' '{"Action":"output","Package":"example/failing","Test":"TestFailure","Output":"--- FAIL: TestFailure (0.01s)\\n"}' '{"Action":"fail","Package":"example/failing","Test":"TestFailure","Elapsed":0.01}' '{"Action":"fail","Package":"example/failing","Elapsed":0.02}'; exit 7`
	var stdout, stderr bytes.Buffer
	code := run([]string{"-lane", "failure", "-out", dir, "-setup-duration", "2s", "--", "sh", "-c", script}, &stdout, &stderr)
	if code != 7 || !strings.Contains(stdout.String(), "--- FAIL: TestFailure") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"failure.json", "failure.log", "failure-summary.md"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("artifact %s: info=%v err=%v", path, info, err)
		}
	}
}
