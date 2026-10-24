// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const archiveTestSum = "f8b1f712f078009f786ea23ebe0a2b14498c80f454453ed6e8384ad54041acfc"
const archiveTestContents = "fixture archive"

func archiveTestMetadata(name, version, relative string) string {
	data, _ := json.Marshal([]map[string]any{{"name": name, "version": version, "repopath": relative, "sum": archiveTestSum, "pkgsize": len(archiveTestContents)}})
	return string(data)
}

func writeTestArchive(t *testing.T, root, name, version string) {
	t.Helper()
	file := filepath.Join(root, "var/cache/pkg", name+"-"+version+"~"+archiveTestSum[:10]+".pkg")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(archiveTestContents), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateArchiveUsesNativeCacheLayout(t *testing.T) {
	for _, relative := range []string{"app-2.pkg", "All/app-2.pkg", "All/Hashed/ab/app-2-abcdef.pkg", "./Hashed/app-2~d2b6c4f365.pkg"} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			archive := "/var/cache/pkg/app-2~f8b1f712f0.pkg"
			fetched := filepath.Join(root, archive)
			queries := 0
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}, RunCommand: func(_ context.Context, args []string) (Result, error) {
				switch {
				case slices.Contains(args, "search"):
					command := args[slices.Index(args, "search"):]
					if !slices.Equal(command, []string{"search", "-U", "-r", "ports", "-e", "-S", "pkg-name", "-R", "--raw-format", "json", "app-2"}) {
						t.Fatalf("unscoped archive query: %v", command)
					}
					return Result{Stdout: archiveTestMetadata("app", "2", relative)}, nil
				case slices.Contains(args, "fetch"):
					if err := os.MkdirAll(filepath.Dir(fetched), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(fetched, []byte("fixture archive"), 0644); err != nil {
						t.Fatal(err)
					}
					return Result{}, nil
				case slices.Contains(args, "query"):
					command := args[slices.Index(args, "query"):]
					if len(command) != 4 || command[1] != "-F" || command[2] != archive {
						t.Fatalf("queried outside the native cache: %v", command)
					}
					if _, err := os.Stat(fetched); err != nil {
						t.Fatal("queried an archive that was not fetched", err)
					}
					queries++
					if command[3] == "%n\t%v\t%o" {
						return Result{Stdout: "app\t2\tmisc/app\n"}, nil
					}
					if command[3] == "%Fp" {
						return Result{Stdout: "/usr/local/share/app/data\n"}, nil
					}
					fallthrough
				default:
					t.Fatalf("unexpected command: %v", args)
					return Result{}, nil
				}
			}}
			plan := Plan{Changes: []Change{{Action: "upgrade", Name: "app", From: "1", To: "2", Repository: "ports"}}}
			if err := client.VerifyCandidates(t.Context(), plan, []Repository{{Name: "ports"}}); err != nil || queries != 2 {
				t.Fatalf("archive verification: %v, queries=%d", err, queries)
			}
			if _, err := os.Stat(fetched); err != nil {
				t.Fatal("preview removed the cached archive", err)
			}
		})
	}
}

func TestCandidateArchiveRejectsInvalidRepositoryMetadata(t *testing.T) {
	valid := map[string]any{"name": "app", "version": "2", "repopath": "All/app-2.pkg", "sum": archiveTestSum, "pkgsize": len(archiveTestContents)}
	invalid := []map[string]any{
		{"name": "other"},
		{"version": "3"},
		{"path": "other.pkg"},
		{"sum": ""},
		{"sum": "3$" + archiveTestSum},
		{"sum": strings.Repeat("g", 64)},
		{"pkgsize": -1},
		{"pkgsize": 0},
		{"repopath": "directory.with.dots/app-2"},
	}
	for _, relative := range []string{"", ".", "..", "../outside.pkg", "/tmp/outside.pkg", "All/../app-2.pkg", "All//app-2.pkg", "./../outside.pkg", "All\\app-2.pkg", "All/app\n2.pkg", "All/app\x002.pkg"} {
		invalid = append(invalid, map[string]any{"repopath": relative})
	}
	duplicate, _ := json.Marshal([]map[string]any{valid, valid})
	outputs := []string{"[]", "[", string(duplicate)}
	for _, fields := range invalid {
		candidate := maps.Clone(valid)
		maps.Copy(candidate, fields)
		data, _ := json.Marshal([]map[string]any{candidate})
		outputs = append(outputs, string(data))
	}
	for _, output := range outputs {
		t.Run(output, func(t *testing.T) {
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: t.TempDir()}, RunCommand: func(_ context.Context, args []string) (Result, error) {
				if !slices.Contains(args, "search") {
					t.Fatalf("downloaded an archive with unverified metadata: %v", args)
				}
				return Result{Stdout: output}, nil
			}}
			plan := Plan{Changes: []Change{{Action: "upgrade", Name: "app", From: "1", To: "2", Repository: "ports"}}}
			if err := client.VerifyCandidates(t.Context(), plan, []Repository{{Name: "ports"}}); err == nil || !strings.Contains(err.Error(), "package_archive_path_unavailable") {
				t.Fatalf("accepted invalid archive metadata %q: %v", output, err)
			}
		})
	}
}

func TestCandidateArchiveFailureLogsReason(t *testing.T) {
	var log bytes.Buffer
	client := Client{Target: Target{JID: 1, Name: "fixture", Root: t.TempDir()}, Log: &log, RunCommand: func(_ context.Context, args []string) (Result, error) {
		if !slices.Contains(args, "search") {
			t.Fatalf("attempted to fetch an unsafe archive: %v", args)
		}
		return Result{Stdout: `[{"name":"app","version":"2","repopath":"./../outside.pkg"}]`}, nil
	}}
	plan := Plan{Changes: []Change{{Action: "upgrade", Name: "app", From: "1", To: "2", Repository: "ports"}}}
	if err := client.VerifyCandidates(t.Context(), plan, []Repository{{Name: "ports"}}); err == nil || !strings.Contains(log.String(), "unsafe repository path") || !strings.Contains(log.String(), "./../outside.pkg") {
		t.Fatalf("archive rejection lost its diagnostic: %v, log=%q", err, log.String())
	}
}

func TestCachedArchiveRejectsSymlinksBeforeFetch(t *testing.T) {
	for _, symlink := range []string{"var", "var/cache", "var/cache/pkg", "var/cache/pkg/app-2~f8b1f712f0.pkg"} {
		t.Run(symlink, func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			kept := filepath.Join(external, "app-2~f8b1f712f0.pkg")
			if err := os.WriteFile(kept, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, symlink)
			if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
				t.Fatal(err)
			}
			destination := external
			if strings.HasSuffix(symlink, ".pkg") {
				destination = kept
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}, RunCommand: func(_ context.Context, args []string) (Result, error) {
				if !slices.Contains(args, "search") {
					t.Fatalf("attempted fetch through a jail symlink: %v", args)
				}
				return Result{Stdout: archiveTestMetadata("app", "2", "All/app-2.pkg")}, nil
			}}
			plan := Plan{Changes: []Change{{Action: "upgrade", Name: "app", From: "1", To: "2", Repository: "ports"}}}
			if err := client.VerifyCandidates(t.Context(), plan, []Repository{{Name: "ports"}}); err == nil || !strings.Contains(err.Error(), "package_archive_path_unavailable") {
				t.Fatalf("accepted symlinked inspection path: %v", err)
			}
			if value, err := os.ReadFile(kept); err != nil || string(value) != "keep" {
				t.Fatalf("inspection or cleanup escaped the jail: %q %v", value, err)
			}
		})
	}
}

func TestCandidateDownloadsAreBatchedAndStillInspected(t *testing.T) {
	for _, failure := range []string{"", "metadata", "duplicate path", "symlink after fetch"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			plan := Plan{Changes: []Change{
				{Action: "upgrade", Name: "app-a", From: "1", To: "2", Repository: "ports"},
				{Action: "install", Name: "app-b", To: "2", Repository: "ports"},
				{Action: "upgrade", Name: "app-c", From: "1", To: "2", Repository: "other"},
				{Action: "remove", Name: "obsolete", From: "1"},
			}}
			repos := []Repository{{Name: "ports"}, {Name: "other"}}
			if failure == "duplicate path" {
				repos[0].URL = "file:///fixture"
			}
			var batches [][]string
			queried := map[string]bool{}
			kept := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(kept, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}, RunCommand: func(_ context.Context, args []string) (Result, error) {
				switch {
				case slices.Contains(args, "search"):
					name := strings.TrimSuffix(args[len(args)-1], "-2")
					relative := "All/" + name + "-2.pkg"
					if failure == "duplicate path" {
						relative = "All/shared.pkg"
					}
					return Result{Stdout: archiveTestMetadata(name, "2", relative)}, nil
				case slices.Contains(args, "fetch"):
					command := args[slices.Index(args, "fetch"):]
					if !slices.Equal(command[:5], []string{"fetch", "-U", "-y", "-r", command[4]}) || slices.Contains(command, "-o") {
						t.Fatalf("unexpected fetch arguments: %v", command)
					}
					batches = append(batches, slices.Clone(command))
					for _, versioned := range command[5:] {
						file := filepath.Join(root, "var/cache/pkg", versioned+"~f8b1f712f0.pkg")
						if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
							t.Fatal(err)
						}
						if failure == "symlink after fetch" {
							if err := os.Symlink(kept, file); err != nil {
								t.Fatal(err)
							}
						} else if err := os.WriteFile(file, []byte(archiveTestContents), 0644); err != nil {
							t.Fatal(err)
						}
					}
					return Result{}, nil
				case slices.Contains(args, "query"):
					command := args[slices.Index(args, "query"):]
					file := command[2]
					if _, err := os.Stat(filepath.Join(root, file)); err != nil {
						t.Fatalf("inspected an unfetched archive: %v", err)
					}
					name := strings.TrimSuffix(filepath.Base(file), "-2~f8b1f712f0.pkg")
					if command[3] == "%Fp" {
						queried[name] = true
						return Result{Stdout: "/usr/local/share/" + name + "/data\n"}, nil
					}
					version := "2"
					if failure == "metadata" && name == "app-b" {
						version = "999"
					}
					return Result{Stdout: name + "\t" + version + "\tmisc/" + name + "\n"}, nil
				default:
					t.Fatalf("unexpected command: %v", args)
					return Result{}, nil
				}
			}}
			err := client.VerifyCandidates(t.Context(), plan, repos)
			if (err != nil) != (failure != "") {
				t.Fatalf("unexpected verification result: %v", err)
			}
			if failure == "" {
				if len(queried) != 3 || len(batches) != 2 || !slices.Equal(batches[0][5:], []string{"app-a-2", "app-b-2"}) || !slices.Equal(batches[1][5:], []string{"app-c-2"}) || batches[0][4] != "ports" || batches[1][4] != "other" {
					t.Fatalf("unscoped fetch or incomplete inspection: %v %v", batches, queried)
				}
			} else if failure == "duplicate path" && len(batches) != 0 {
				t.Fatal("downloaded before validating all archive paths")
			}
			if len(batches) > 0 {
				if _, err := os.Lstat(filepath.Join(root, "var/cache/pkg/app-a-2~f8b1f712f0.pkg")); err != nil {
					t.Fatalf("downloaded archive was not retained: %v", err)
				}
			}
			if value, err := os.ReadFile(kept); err != nil || string(value) != "keep" {
				t.Fatalf("inspection or cleanup escaped the target: %q %v", value, err)
			}
		})
	}
}

func TestCandidateDownloadArgumentBatchesAreBounded(t *testing.T) {
	client := Client{Target: Target{JID: 1, Name: "fixture", Root: t.TempDir()}}
	var plan Plan
	for i := range 129 {
		plan.Changes = append(plan.Changes, Change{Action: "upgrade", Name: fmt.Sprintf("app-%03d", i), From: "1", To: "2", Repository: "ports"})
	}
	var sizes []int
	inspected := 0
	client.RunCommand = func(_ context.Context, args []string) (Result, error) {
		if slices.Contains(args, "search") {
			name := strings.TrimSuffix(args[len(args)-1], "-2")
			return Result{Stdout: archiveTestMetadata(name, "2", "All/"+name+"-2.pkg")}, nil
		}
		if index := slices.Index(args, "fetch"); index >= 0 {
			sizes = append(sizes, len(args[index:])-5)
			for _, versioned := range args[index+5:] {
				writeTestArchive(t, client.Target.Root, strings.TrimSuffix(versioned, "-2"), "2")
			}
			return Result{}, nil
		}
		t.Fatalf("unexpected command: %v", args)
		return Result{}, nil
	}
	if err := client.inspectCandidates(t.Context(), plan, []Repository{{Name: "ports"}}, func(Change, string) error { inspected++; return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sizes, []int{128, 1}) || inspected != 129 {
		t.Fatalf("unbounded fetch or missing inspections: %v %d", sizes, inspected)
	}
}

func TestArchiveCacheKeepsCompletedDownloads(t *testing.T) {
	root := t.TempDir()
	attempt := 0
	plan := Plan{Changes: []Change{{Action: "install", Name: "app-a", To: "2"}, {Action: "install", Name: "app-b", To: "2"}}}
	client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}, RunCommand: func(_ context.Context, args []string) (Result, error) {
		if slices.Contains(args, "search") {
			name := strings.TrimSuffix(args[len(args)-1], "-2")
			return Result{Stdout: archiveTestMetadata(name, "2", "All/"+name+"-2.pkg")}, nil
		}
		if index := slices.Index(args, "fetch"); index >= 0 {
			attempt++
			if attempt == 1 {
				writeTestArchive(t, root, "app-a", "2")
				return Result{ExitCode: 1, Stderr: "fixture network failure"}, nil
			}
			if !slices.Equal(args[index+5:], []string{"app-b-2"}) {
				t.Fatalf("retried a completed download: %v", args)
			}
			writeTestArchive(t, root, "app-b", "2")
			return Result{}, nil
		}
		t.Fatalf("unexpected command: %v", args)
		return Result{}, nil
	}}
	inspected := 0
	inspect := func(Change, string) error { inspected++; return nil }
	if err := client.inspectCandidates(t.Context(), plan, []Repository{{Name: "ports"}}, inspect); err == nil || inspected != 0 {
		t.Fatalf("approved an incomplete fetch: %v", err)
	}
	if err := client.inspectCandidates(t.Context(), plan, []Repository{{Name: "ports"}}, inspect); err != nil || inspected != 2 || attempt != 2 {
		t.Fatalf("retry did not reuse completed downloads: attempts=%d inspected=%d err=%v", attempt, inspected, err)
	}
	if err := client.inspectCandidates(t.Context(), plan, []Repository{{Name: "ports"}}, inspect); err != nil || inspected != 4 || attempt != 2 {
		t.Fatalf("complete cache was downloaded again or not inspected: attempts=%d inspected=%d err=%v", attempt, inspected, err)
	}
}

func TestPkgArchiveChecksumFormats(t *testing.T) {
	root := t.TempDir()
	writeTestArchive(t, root, "app", "2")
	client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}}
	for _, sum := range []string{
		archiveTestSum,
		"1$" + archiveTestSum,
		"0$axcxxjy6ady6jh3pnixhmfcfwe1aey16wkthdmd7at1kpyfecf9b",
		"2$m1d9fn8ab7o8cgwtbx8dzzo9kxayopbkzes8mpcxjek4uawaax68tt8q6qk49x9giid9kitnah8t7pz6mepausfkoe6rmmw58j6n3gy",
		"5$jrkg7kzd1qfixscqw7485tzw4nerkwk34psqfzzyk3npnqyzao7b",
	} {
		cached, err := client.cachedArchive(t.Context(), candidateArchive{path: "/var/cache/pkg/app-2~f8b1f712f0.pkg", checksum: sum, size: int64(len(archiveTestContents))})
		if err != nil || !cached {
			t.Fatalf("failed to verify %q checksum: %v", sum, err)
		}
	}
}

func TestArchiveCacheKeepsPartialAndLocalFiles(t *testing.T) {
	for _, mode := range []string{"partial", "corrupt", "oversized", "local repository", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			writeTestArchive(t, root, "app", "2")
			candidate := candidateArchive{path: "/var/cache/pkg/app-2~f8b1f712f0.pkg", checksum: archiveTestSum, size: int64(len(archiveTestContents))}
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}}
			ctx := t.Context()
			contents := archiveTestContents
			switch mode {
			case "partial":
				contents = contents[:5]
			case "corrupt", "local repository":
				contents = "X" + contents[1:]
				candidate.local = mode == "local repository"
			case "oversized":
				contents += "extra"
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			file := filepath.Join(root, candidate.path)
			if err := os.WriteFile(file, []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
			cached, err := client.cachedArchive(ctx, candidate)
			if cached || (err != nil) != (candidate.local || mode == "cancelled") {
				t.Fatalf("unexpected cache result: cached=%v err=%v", cached, err)
			}
			if mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			_, statErr := os.Stat(file)
			if os.IsNotExist(statErr) != (mode == "corrupt" || mode == "oversized") {
				t.Fatalf("discarded a partial/local file or retained corrupt bytes: %v", statErr)
			}
		})
	}
}

func TestCandidateFetchCannotBypassChecksumVerification(t *testing.T) {
	for _, contents := range []string{"", "X" + archiveTestContents[1:]} {
		t.Run(contents, func(t *testing.T) {
			root := t.TempDir()
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}, RunCommand: func(_ context.Context, args []string) (Result, error) {
				if slices.Contains(args, "search") {
					return Result{Stdout: archiveTestMetadata("app", "2", "All/app-2.pkg")}, nil
				}
				if slices.Contains(args, "fetch") {
					if contents != "" {
						writeTestArchive(t, root, "app", "2")
						if err := os.WriteFile(filepath.Join(root, "var/cache/pkg/app-2~f8b1f712f0.pkg"), []byte(contents), 0644); err != nil {
							t.Fatal(err)
						}
					}
					return Result{}, nil
				}
				t.Fatalf("inspected bytes without a verified checksum: %v", args)
				return Result{}, nil
			}}
			err := client.VerifyCandidates(t.Context(), Plan{Changes: []Change{{Action: "install", Name: "app", To: "2"}}}, []Repository{{Name: "ports"}})
			if err == nil || !strings.Contains(err.Error(), "package_archive_checksum_mismatch") {
				t.Fatalf("accepted corrupt or absent downloaded bytes: %v", err)
			}
		})
	}
}

func TestFetchOutputStreamsBeforeCommandCompletes(t *testing.T) {
	directory := t.TempDir()
	binary, release := filepath.Join(directory, "pkg"), filepath.Join(directory, "continue")
	script := "#!/bin/sh\nprintf 'Fetching https://user:'\nprintf 'secret@example.invalid/pkg\\n'\nwhile [ ! -f '" + release + "' ]; do sleep 0.02; done\nprintf 'complete\\n'\nprintf 'warning\\n' >&2\n"
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	client := Client{Binary: binary, Target: Target{JID: 1, Name: "fixture", Root: directory}, Log: writer}
	go func() {
		_, err := client.Run(ctx, "fetch", "-U", "-y", "fixture-2")
		_ = writer.Close()
		done <- err
	}()
	log := bufio.NewReader(reader)
	first, err := log.ReadString('\n')
	if err != nil || ctx.Err() != nil || first != "Fetching https://[redacted]@example.invalid/pkg\n" {
		t.Fatalf("fetch output was buffered or not redacted: %q %v %v", first, err, ctx.Err())
	}
	if err = os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(log)
	if err != nil || <-done != nil || strings.Count(first+string(rest), "Fetching") != 1 || !strings.Contains(string(rest), "complete") || !strings.Contains(string(rest), "warning") {
		t.Fatalf("fetch output lost or duplicated: %q %v", first+string(rest), err)
	}
}
