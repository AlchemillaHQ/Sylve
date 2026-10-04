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
	"encoding/json"
	"io"
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
	script := `printf '%s\n' '{"Action":"output","Package":"example/failing","Test":"TestFailure","Output":"--- FAIL: TestFailure (0.01s)\\n"}' '{"Action":"fail","Package":"example/failing","Test":"TestFailure","Elapsed":0.01}' '{"Action":"fail","Package":"example/failing","Elapsed":0.02}' '*** Error code 1' 'Stop.' 'make: stopped making "test-integration"'; exit 7`
	var stdout, stderr bytes.Buffer
	code := run([]string{"-lane", "failure", "-out", dir, "-setup-duration", "2s", "--", "sh", "-c", script}, &stdout, &stderr)
	if code != 7 || !strings.Contains(stdout.String(), "--- FAIL: TestFailure") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 || strings.Contains(stdout.String(), "Reporter error") {
		t.Fatalf("make diagnostics caused a reporter error: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	for _, name := range []string{"failure.json", "failure.log", "failure-summary.md"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("artifact %s: info=%v err=%v", path, info, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "failure.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result totals
	if err := consumeJSON(bytes.NewReader(raw), io.Discard, io.Discard, &result); err != nil {
		t.Fatalf("invalid JSON artifact: %v", err)
	}
	if result.events != 3 || result.testCounts.fail != 1 || result.packageCounts.fail != 1 || bytes.Contains(raw, []byte("*** Error code")) {
		t.Fatalf("failure events or JSON artifact changed: totals=%+v raw=%s", result, raw)
	}
	log, err := os.ReadFile(filepath.Join(dir, "failure.log"))
	if err != nil || !bytes.Contains(log, []byte("*** Error code 1")) {
		t.Fatalf("make diagnostics missing from readable log: %v: %s", err, log)
	}
}

func TestConsumeJSONRejectsMalformedEvents(t *testing.T) {
	input := "{\"Action\":\nremaining diagnostic output\n"
	var human bytes.Buffer
	var result totals
	err := consumeJSON(strings.NewReader(input), io.Discard, &human, &result)
	if err == nil || !strings.Contains(err.Error(), "decode go test JSON") {
		t.Fatalf("malformed event error = %v", err)
	}
	if human.String() != input {
		t.Fatalf("malformed output was not preserved: %q", human.String())
	}
}

func TestConsumeJSONWithDiagnosticsAndLongFinalEvent(t *testing.T) {
	output := strings.Repeat("x", 128*1024)
	event, err := json.Marshal(testEvent{Action: "output", Package: "example/a", Output: output})
	if err != nil {
		t.Fatal(err)
	}
	input := append([]byte("prerequisite diagnostic\n\n"), event...)
	var raw, human bytes.Buffer
	var result totals
	if err := consumeJSON(bytes.NewReader(input), &raw, &human, &result); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw.Bytes(), event) || human.String() != "prerequisite diagnostic\n\n"+output || result.events != 1 {
		t.Fatalf("long final event or diagnostics changed: raw=%d human=%d events=%d", raw.Len(), human.Len(), result.events)
	}
}

func TestRunRejectsSuccessfulCommandWithoutTestEvents(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-lane", "empty", "-out", t.TempDir(), "--", "sh", "-c", "echo diagnostic"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "no go test JSON events received") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
