// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCandidateArchiveUsesRepositoryLayout(t *testing.T) {
	for _, relative := range []string{"app-2.pkg", "All/app-2.pkg", "All/Hashed/ab/app-2-abcdef.pkg", "./Hashed/app-2~d2b6c4f365.pkg"} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			archive := path.Join("/var/cache/pkg/sylve-inspect", relative)
			fetched := filepath.Join(root, archive)
			queries := 0
			client := Client{Target: Target{JID: 1, Name: "fixture", Root: root}, RunCommand: func(_ context.Context, args []string) (Result, error) {
				switch {
				case slices.Contains(args, "search"):
					command := args[slices.Index(args, "search"):]
					if !slices.Equal(command, []string{"search", "-U", "-r", "ports", "-e", "-S", "pkg-name", "-R", "--raw-format", "json", "app-2"}) {
						t.Fatalf("unscoped archive query: %v", command)
					}
					data, _ := json.Marshal([]map[string]string{{"name": "app", "version": "2", "path": relative}})
					return Result{Stdout: string(data)}, nil
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
						t.Fatalf("queried a guessed archive path: %v", command)
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
			if _, err := os.Stat(filepath.Join(root, "var/cache/pkg/sylve-inspect")); !os.IsNotExist(err) {
				t.Fatalf("inspection archive or empty directories were not cleaned: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "var/cache/pkg")); err != nil {
				t.Fatal("cleanup removed the jail package cache", err)
			}
		})
	}
}

func TestCandidateArchiveRejectsInvalidRepositoryMetadata(t *testing.T) {
	outputs := []string{"[]", "[", `[{"name":"other","version":"2","repopath":"All/app-2.pkg"}]`, `[{"name":"app","version":"3","repopath":"All/app-2.pkg"}]`, `[{"name":"app","version":"2","repopath":"All/app-2.pkg","path":"other.pkg"}]`, `[{"name":"app","version":"2","repopath":"All/app-2.pkg"},{"name":"app","version":"2","repopath":"other.pkg"}]`}
	for _, relative := range []string{"", ".", "..", "../outside.pkg", "/tmp/outside.pkg", "All/../app-2.pkg", "All//app-2.pkg", "./../outside.pkg", "All\\app-2.pkg", "All/app\n2.pkg", "All/app\x002.pkg"} {
		data, _ := json.Marshal([]map[string]string{{"name": "app", "version": "2", "repopath": relative}})
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

func TestInspectionArchiveRejectsSymlinksBeforeFetch(t *testing.T) {
	for _, symlink := range []string{"sylve-inspect", "sylve-inspect/All", "sylve-inspect/All/app-2.pkg"} {
		t.Run(symlink, func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			kept := filepath.Join(external, "app-2.pkg")
			if symlink == "sylve-inspect" {
				kept = filepath.Join(external, "All/app-2.pkg")
				if err := os.MkdirAll(filepath.Dir(kept), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(kept, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "var/cache/pkg", symlink)
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
				return Result{Stdout: `[{"name":"app","version":"2","repopath":"All/app-2.pkg"}]`}, nil
			}}
			plan := Plan{Changes: []Change{{Action: "upgrade", Name: "app", From: "1", To: "2", Repository: "ports"}}}
			if err := client.VerifyCandidates(t.Context(), plan, []Repository{{Name: "ports"}}); err == nil || !strings.Contains(err.Error(), "package_archive_path_unavailable") {
				t.Fatalf("accepted symlinked inspection path: %v", err)
			}
			client.removeInspectionArchive("All/app-2.pkg")
			if value, err := os.ReadFile(kept); err != nil || string(value) != "keep" {
				t.Fatalf("inspection or cleanup escaped the jail: %q %v", value, err)
			}
		})
	}
}
