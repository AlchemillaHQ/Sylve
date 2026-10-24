// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sylveupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"testing/iotest"
)

func TestUpdatePolicy(t *testing.T) {
	current := Installed{Mode: "standalone", Version: "0.3.1", Commit: strings.Repeat("a", 40), BuiltAt: "2026-10-19T12:00:00Z", SHA256: strings.Repeat("1", 64), Channel: "rolling"}
	for _, test := range []struct {
		version, channel, digest string
		allow                    bool
	}{
		{"0.3.0", "rolling", "2", false}, {"0.3.1", "tagged", "2", false}, {"0.3.1", "rolling", "2", true},
		{"0.3.1", "rolling", "1", false}, {"0.3.2", "tagged", "2", true}, {"0.3.10", "tagged", "2", true}, {"0.3.2", "other", "2", false},
	} {
		err := Allowed(current, Candidate{Version: test.version, Channel: test.channel, SHA256: strings.Repeat(test.digest, 64), Commit: strings.Repeat("b", 40), BuiltAt: "2026-10-20T12:00:00Z"})
		if (err == nil) != test.allow {
			t.Fatalf("%+v: %v", test, err)
		}
	}
	current.Channel, current.Commit = "tagged", "legacy-short-commit"
	if err := Allowed(current, Candidate{Version: current.Version, Channel: "rolling", SHA256: strings.Repeat("2", 64), Commit: strings.Repeat("b", 40), BuiltAt: "2026-10-20T12:00:00Z"}); err != nil {
		t.Fatalf("legacy commit with a verified build date unnecessarily blocked rolling update: %v", err)
	}
	current.Mode = "package"
	if Allowed(current, Candidate{Version: "0.4.0", Channel: "rolling"}) == nil {
		t.Fatal("allowed replacement of packaged binary")
	}
	for _, test := range []struct {
		local, target string
		want          bool
	}{{"16.0-CURRENT", "15.1-RELEASE", true}, {"15.1-STABLE", "15.1-RELEASE", true}, {"15.0-RELEASE", "15.1-RELEASE", false}, {"unknown", "15.1-RELEASE", false}} {
		if Compatible(test.local, test.target) != test.want {
			t.Fatalf("compatibility: %+v", test)
		}
	}
}

func TestSameVersionBuildOrdering(t *testing.T) {
	const installedTime = "2026-10-19T12:00:00Z"
	for _, test := range []struct {
		name, currentTime, targetTime, version, want string
	}{
		{"newer rolling build", installedTime, "2026-10-20T12:00:00Z", "0.3.1", ""},
		{"older rolling build", installedTime, "2026-10-18T12:00:00Z", "0.3.1", "sylve_downgrade_forbidden"},
		{"equal timestamps", installedTime, installedTime, "0.3.1", "sylve_build_order_ambiguous"},
		{"equal instants with different offsets", installedTime, "2026-10-19T14:00:00+02:00", "0.3.1", "sylve_build_order_ambiguous"},
		{"subsecond upgrade", installedTime, "2026-10-19T12:00:00.1Z", "0.3.1", ""},
		{"missing installed date", "", installedTime, "0.3.1", "sylve_build_time_unknown"},
		{"invalid installed date", "unknown", installedTime, "0.3.1", "sylve_build_time_unknown"},
		{"zero installed date", "0001-01-01T00:00:00Z", installedTime, "0.3.1", "sylve_build_time_unknown"},
		{"missing candidate date", installedTime, "", "0.3.1", "release_metadata_invalid"},
		{"invalid candidate date", installedTime, "unknown", "0.3.1", "release_metadata_invalid"},
		{"zero candidate date", installedTime, "0001-01-01T00:00:00Z", "0.3.1", "release_metadata_invalid"},
		{"higher version wins over timestamp", installedTime, "2026-10-18T12:00:00Z", "0.3.2", ""},
		{"legacy can upgrade to higher version", "", installedTime, "0.3.2", ""},
		{"lower version cannot use newer timestamp", installedTime, "2026-10-20T12:00:00Z", "0.3.0", "sylve_downgrade_forbidden"},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := Installed{Mode: "standalone", Version: "0.3.1", BuiltAt: test.currentTime, SHA256: strings.Repeat("1", 64)}
			target := Candidate{Channel: "rolling", Version: test.version, Commit: strings.Repeat("a", 40), BuiltAt: test.targetTime, SHA256: strings.Repeat("2", 64)}
			err := Allowed(current, target)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != test.want {
				t.Fatalf("got %v, want %s", err, test.want)
			}
		})
	}
}

func TestDownloadRequiresExactApprovedBytes(t *testing.T) {
	for _, body := range []string{"old", "new", "too many bytes"} {
		t.Run(body, func(t *testing.T) {
			client := NewClient()
			client.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			sum := sha256.Sum256([]byte("old"))
			target := Candidate{Tag: "tip", URL: assetBase + "tip/sylve-" + runtime.GOARCH, SHA256: hex.EncodeToString(sum[:]), Size: 3}
			file := filepath.Join(t.TempDir(), "sylve")
			err := client.Download(t.Context(), target, file)
			if err == nil {
				t.Fatal("accepted unverified executable")
			}
			if body != "old" && err.Error() != "release_checksum_mismatch" {
				t.Fatalf("wrong failure: %v", err)
			}
			if info, statErr := os.Stat(file); statErr != nil || info.Size() > target.Size+1 || info.Mode().Perm() != 0600 {
				t.Fatalf("unbounded or nonprivate staging: %+v %v", info, statErr)
			}
		})
	}
}

func TestDownloadedBuildMustHaveExactTargetIdentity(t *testing.T) {
	commit := strings.Repeat("a", 40)
	const commitFlag = "github.com/alchemillahq/sylve/internal/cmd.Commit="
	const builtAtFlag = "github.com/alchemillahq/sylve/internal/cmd.BuiltAt="
	const builtAt = "2026-10-19T12:00:00Z"
	for _, flags := range []string{"-s -w -X " + commitFlag + commit + " -X " + builtAtFlag + builtAt, "-X=" + commitFlag + commit + " -X=" + builtAtFlag + builtAt} {
		info := &debug.BuildInfo{Path: "github.com/alchemillahq/sylve/cmd/sylve", Settings: []debug.BuildSetting{{Key: "GOOS", Value: "freebsd"}, {Key: "GOARCH", Value: runtime.GOARCH}, {Key: "vcs.revision", Value: commit}, {Key: "-ldflags", Value: flags}}}
		target := Candidate{Commit: commit, BuiltAt: builtAt}
		if err := validateBuild(info, target); err != nil {
			t.Fatal(err)
		}
		info.Settings[3].Value += "0"
		if err := validateBuild(info, target); err == nil {
			t.Fatal("accepted commit prefix instead of exact baked commit")
		}
		info.Settings[3].Value = flags
		info.Settings[0].Value = "linux"
		if err := validateBuild(info, target); err == nil {
			t.Fatal("accepted wrong target OS")
		}
	}
}

func TestDownloadedBuildMustMatchApprovedTimestamp(t *testing.T) {
	commit := strings.Repeat("a", 40)
	const builtAt = "2026-10-19T12:00:00Z"
	for _, test := range []struct {
		name, stamp string
		allow       bool
	}{
		{"matching", builtAt, true},
		{"same instant", "2026-10-19T14:00:00+02:00", true},
		{"older", "2026-10-18T12:00:00Z", false},
		{"newer", "2026-10-20T12:00:00Z", false},
		{"subsecond mismatch", "2026-10-19T12:00:00.1Z", false},
		{"missing", "", false},
		{"invalid", "unknown", false},
		{"zero", "0001-01-01T00:00:00Z", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			flags := "-X github.com/alchemillahq/sylve/internal/cmd.Commit=" + commit
			if test.stamp != "" {
				flags += " -X github.com/alchemillahq/sylve/internal/cmd.BuiltAt=" + test.stamp
			}
			info := &debug.BuildInfo{Path: "github.com/alchemillahq/sylve/cmd/sylve", Settings: []debug.BuildSetting{{Key: "GOOS", Value: "freebsd"}, {Key: "GOARCH", Value: runtime.GOARCH}, {Key: "vcs.revision", Value: commit}, {Key: "-ldflags", Value: flags}}}
			err := validateBuild(info, Candidate{Commit: commit, BuiltAt: builtAt})
			if (err == nil) != test.allow {
				t.Fatalf("timestamp verification: %v", err)
			}
		})
	}
}

type transport func(*http.Request) (*http.Response, error)

func (fn transport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestResolvePinsAndValidatesRelease(t *testing.T) {
	metadata := `{"version":"0.3.1","commit":"` + strings.Repeat("a", 40) + `","freebsd":"15.1-RELEASE","builtAt":"2026-10-19T12:00:00Z"}`
	sum := sha256.Sum256([]byte(metadata))
	metaDigest := hex.EncodeToString(sum[:])
	rel := release{Tag: "tip", Prerelease: true, Assets: []asset{
		{ID: 1, Name: "sylve-" + runtime.GOARCH, URL: assetBase + "tip/sylve-" + runtime.GOARCH, Digest: "sha256:" + strings.Repeat("b", 64), Size: 123},
		{ID: 2, Name: "sylve-version.json", URL: assetBase + "tip/sylve-version.json", Digest: "sha256:" + metaDigest, Size: int64(len(metadata))},
	}}
	client := NewClient()
	client.HTTP.Transport = transport(func(req *http.Request) (*http.Response, error) {
		body := metadata
		if req.URL.Host == "api.github.com" {
			raw, _ := json.Marshal(rel)
			body = string(raw)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	got, err := client.Resolve(context.Background(), "rolling", "0.3.1")
	if err != nil || got.AssetID != 1 || got.Version != "0.3.1" {
		t.Fatalf("%+v: %v", got, err)
	}
	metadata += " "
	if _, err = client.Resolve(context.Background(), "rolling", "0.3.1"); err == nil {
		t.Fatal("accepted metadata replaced during publication")
	}
	metadata = strings.TrimSpace(metadata)
	rel.Assets[0].URL = "https://example.org/evil"
	if _, err = client.Resolve(context.Background(), "rolling", "0.3.1"); err == nil {
		t.Fatal("accepted external download URL")
	}
}

func TestOlderTaggedReleasesDoNotNeedBuildMetadataToRejectDowngrades(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = transport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != apiBase+"/releases/latest" {
			t.Fatalf("unexpected metadata fetch: %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.3.1","assets":[]}`))}, nil
	})
	for _, current := range []string{"0.3.1", "0.3.2"} {
		if _, err := client.Resolve(t.Context(), "tagged", current); err == nil || err.Error() != "sylve_downgrade_forbidden" {
			t.Fatalf("unexpected old-tag decision: %v", err)
		}
	}
}

func TestReleaseRedirectTrust(t *testing.T) {
	for _, address := range []string{"http://github.com/a", "https://github.com.evil/a", "https://127.0.0.1/a", "https://user:secret@github.com/a", "https://github.com:443/a"} {
		req, _ := http.NewRequest("GET", address, nil)
		if trustedURL(req.URL) {
			t.Fatalf("trusted %s", address)
		}
	}
}

func TestReleaseFetchFailuresKeepActionableCodesAndCancellation(t *testing.T) {
	for _, cause := range []error{errors.New("fixture_network_unavailable"), context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			client := NewClient()
			client.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, cause })
			_, err := client.Resolve(t.Context(), "rolling", "0.3.1")
			if !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), "release_fetch_failed:") {
				t.Fatalf("release read lost its error code or cause: %v", err)
			}
			target := Candidate{Tag: "tip", URL: assetBase + "tip/sylve-" + runtime.GOARCH, SHA256: strings.Repeat("1", 64), Size: 1}
			err = client.Download(t.Context(), target, filepath.Join(t.TempDir(), "sylve"))
			if !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), "release_fetch_failed:") {
				t.Fatalf("release download lost its error code or cause: %v", err)
			}
		})
	}
	client := NewClient()
	client.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))}, nil
	})
	_, err := client.Resolve(t.Context(), "rolling", "0.3.1")
	if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.HasPrefix(err.Error(), "release_fetch_failed:") {
		t.Fatalf("incomplete metadata read lost its error code or cause: %v", err)
	}
}
