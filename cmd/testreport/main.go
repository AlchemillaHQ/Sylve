// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	packageLimit = 20
	testLimit    = 30
)

type testEvent struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}

type timing struct {
	name    string
	action  string
	elapsed float64
}

type counts struct {
	pass int
	skip int
	fail int
}

type totals struct {
	packages       []timing
	tests          []timing
	packageCounts  counts
	testCounts     counts
	packageElapsed float64
	events         int
}

type lockedWriter struct {
	sync.Mutex
	w io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.w.Write(p)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("testreport", flag.ContinueOnError)
	flags.SetOutput(stderr)
	lane := flags.String("lane", "", "test lane name")
	outDir := flags.String("out", "test-results", "artifact directory")
	setup := flags.Duration("setup-duration", -1, "setup time before tests")
	cacheRestored := flags.Bool("cache-restored", false, "whether a Go build cache was restored")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	command := flags.Args()
	if *lane == "" || len(command) == 0 {
		fmt.Fprintln(stderr, "usage: testreport -lane name [flags] -- command [args...]")
		return 2
	}

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Fprintf(stderr, "create report directory: %v\n", err)
		return 2
	}
	raw, err := os.Create(filepath.Join(*outDir, *lane+".json"))
	if err != nil {
		fmt.Fprintf(stderr, "create raw report: %v\n", err)
		return 2
	}
	defer raw.Close()
	logFile, err := os.Create(filepath.Join(*outDir, *lane+".log"))
	if err != nil {
		fmt.Fprintf(stderr, "create readable log: %v\n", err)
		return 2
	}
	defer logFile.Close()

	log := &lockedWriter{w: logFile}
	human := io.MultiWriter(stdout, log)
	errorsOut := io.MultiWriter(stderr, log)
	commandText := strings.Join(command, " ")
	fmt.Fprintf(human, "=== %s test lane ===\ncommand: %s\n\n", *lane, commandText)

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stderr = errorsOut
	childOutput, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(errorsOut, "capture test output: %v\n", err)
		return 2
	}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return finish(*lane, commandText, *outDir, 127, time.Since(started), *setup, *cacheRestored, totals{}, err, human, errorsOut)
	}

	var result totals
	parseErr := consumeJSON(childOutput, raw, human, &result)
	waitErr := cmd.Wait()
	code := commandExitCode(waitErr)
	if parseErr != nil {
		fmt.Fprintf(errorsOut, "test report: %v\n", parseErr)
		if code == 0 {
			code = 2
		}
	}
	return finish(*lane, commandText, *outDir, code, time.Since(started), *setup, *cacheRestored, result, parseErr, human, errorsOut)
}

func consumeJSON(input io.Reader, raw, human io.Writer, result *totals) error {
	stream := io.TeeReader(input, raw)
	decoder := json.NewDecoder(stream)
	for {
		var event testEvent
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			_, drainErr := io.Copy(io.Discard, stream)
			return errors.Join(fmt.Errorf("decode go test JSON: %w", err), drainErr)
		}
		result.add(event)
		if event.Action == "output" {
			if _, err := io.WriteString(human, event.Output); err != nil {
				_, drainErr := io.Copy(io.Discard, stream)
				return errors.Join(fmt.Errorf("write readable test output: %w", err), drainErr)
			}
		}
	}
}

func (result *totals) add(event testEvent) {
	result.events++
	if event.Action != "pass" && event.Action != "skip" && event.Action != "fail" {
		return
	}
	if event.Test == "" {
		result.packages = append(result.packages, timing{event.Package, event.Action, event.Elapsed})
		result.packageElapsed += event.Elapsed
		result.packageCounts.add(event.Action)
		return
	}
	result.testCounts.add(event.Action)
	if !strings.Contains(event.Test, "/") {
		result.tests = append(result.tests, timing{event.Package + ": " + event.Test, event.Action, event.Elapsed})
	}
}

func (counts *counts) add(action string) {
	switch action {
	case "pass":
		counts.pass++
	case "skip":
		counts.skip++
	case "fail":
		counts.fail++
	}
}

func finish(lane, command, outDir string, code int, wall, setup time.Duration, cacheRestored bool, result totals, reportErr error, human, stderr io.Writer) int {
	summary := formatReport(lane, command, code, wall, setup, cacheRestored, result, reportErr)
	if _, err := io.WriteString(human, "\n"+summary); err != nil && code == 0 {
		code = 2
	}
	if err := os.WriteFile(filepath.Join(outDir, lane+"-summary.md"), []byte(summary), 0644); err != nil {
		fmt.Fprintf(stderr, "write test summary: %v\n", err)
		if code == 0 {
			code = 2
		}
	}
	return code
}

func formatReport(lane, command string, code int, wall, setup time.Duration, cacheRestored bool, result totals, reportErr error) string {
	status := "pass"
	if code != 0 {
		status = "fail"
	}
	version := commandOutput("freebsd-version")
	if version == "" {
		version = commandOutput("uname", "-sr")
	}
	if version == "" {
		version = runtime.GOOS
	}

	var report strings.Builder
	fmt.Fprintf(&report, "## Test duration report: %s\n\n", markdown(lane))
	fmt.Fprintln(&report, "| Field | Value |\n|---|---|")
	fmt.Fprintf(&report, "| Lane | %s |\n", markdown(lane))
	fmt.Fprintf(&report, "| Command | `%s` |\n", markdown(command))
	fmt.Fprintf(&report, "| Result | %s (exit %d) |\n", status, code)
	fmt.Fprintf(&report, "| Go test wall time | %s |\n", wall.Round(time.Millisecond))
	fmt.Fprintf(&report, "| Setup time | %s |\n", optionalDuration(setup))
	fmt.Fprintf(&report, "| Package elapsed sum | %.3fs |\n", result.packageElapsed)
	fmt.Fprintf(&report, "| Test/subtest events (pass / skip / fail) | %d / %d / %d |\n", result.testCounts.pass, result.testCounts.skip, result.testCounts.fail)
	fmt.Fprintf(&report, "| Package events (pass / skip / fail) | %d / %d / %d |\n", result.packageCounts.pass, result.packageCounts.skip, result.packageCounts.fail)
	fmt.Fprintf(&report, "| Parsed JSON events | %d |\n", result.events)
	fmt.Fprintf(&report, "| FreeBSD version | %s |\n", markdown(version))
	fmt.Fprintf(&report, "| Go version | %s (%s/%s) |\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&report, "| CPU count | %d |\n", runtime.NumCPU())
	fmt.Fprintf(&report, "| Go build cache restored | %t |\n", cacheRestored)
	writeRanking(&report, "Slowest packages", result.packages, packageLimit)
	writeRanking(&report, "Slowest top-level tests", result.tests, testLimit)
	if reportErr != nil {
		fmt.Fprintf(&report, "\n### Reporter error\n\n`%s`\n", markdown(reportErr.Error()))
	}
	return report.String()
}

func writeRanking(report *strings.Builder, title string, values []timing, limit int) {
	fmt.Fprintf(report, "\n### %s (top %d)\n\n", title, limit)
	if len(values) == 0 {
		fmt.Fprintln(report, "_No completed entries._")
		return
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].elapsed == values[j].elapsed {
			return values[i].name < values[j].name
		}
		return values[i].elapsed > values[j].elapsed
	})
	if len(values) > limit {
		values = values[:limit]
	}
	fmt.Fprintln(report, "| # | Elapsed | Result | Name |\n|---:|---:|---|---|")
	for index, value := range values {
		fmt.Fprintf(report, "| %d | %.3fs | %s | `%s` |\n", index+1, value.elapsed, value.action, markdown(value.name))
	}
}

func commandOutput(name string, args ...string) string {
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
		return exitErr.ExitCode()
	}
	return 1
}

func optionalDuration(duration time.Duration) string {
	if duration < 0 {
		return "not recorded"
	}
	return duration.Round(time.Millisecond).String()
}

func markdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "`", "\\`")
	return strings.ReplaceAll(value, "\n", " ")
}
