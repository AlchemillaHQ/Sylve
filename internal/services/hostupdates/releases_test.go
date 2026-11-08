// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package hostupdates

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/sylveupdate"
	"github.com/google/uuid"
)

func releaseReadService(t *testing.T) (*Service, *atomic.Int32) {
	t.Helper()
	s := testService(t)
	s.ownership = func(context.Context, *pkgmanager.Client, string) (bool, string, error) {
		return false, "", nil
	}
	metadata := `{"version":"0.3.2","commit":"` + strings.Repeat("b", 40) + `","freebsd":"15.1-RELEASE","builtAt":"2026-10-24T12:00:00Z"}`
	sum := sha256.Sum256([]byte(metadata))
	requests := &atomic.Int32{}
	s.releases.HTTP.Transport = releaseTransport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		body := metadata
		if request.URL.Host == "api.github.com" {
			tag := "tip"
			switch request.URL.Path {
			case "/repos/AlchemillaHQ/Sylve/releases/tags/tip":
			case "/repos/AlchemillaHQ/Sylve/releases/latest":
				tag = "v0.3.2"
			default:
				return nil, errors.New("unexpected_release_request")
			}
			base := "https://github.com/AlchemillaHQ/Sylve/releases/download/" + tag + "/"
			raw, err := json.Marshal(map[string]any{"tag_name": tag, "assets": []map[string]any{
				{"id": 1, "name": "sylve-" + runtime.GOARCH, "browser_download_url": base + "sylve-" + runtime.GOARCH, "digest": "sha256:" + strings.Repeat("2", 64), "size": 123},
				{"id": 2, "name": "sylve-version.json", "browser_download_url": base + "sylve-version.json", "digest": "sha256:" + hex.EncodeToString(sum[:]), "size": len(metadata)},
			}})
			if err != nil {
				return nil, err
			}
			body = string(raw)
		} else if !strings.HasSuffix(request.URL.Path, "/sylve-version.json") {
			return nil, errors.New("unexpected_binary_download")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return s, requests
}

func TestCanceledReleaseReadIsNotRecordedAsFailure(t *testing.T) {
	s, _ := releaseReadService(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.releases.HTTP.Transport = releaseTransport(func(request *http.Request) (*http.Response, error) {
		cancel()
		return nil, request.Context().Err()
	})
	if _, err := s.GetRelease(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read did not stop: %v", err)
	}
	state, err := s.state()
	if err != nil || state.ReleaseError != "" || state.ReleaseCheckedAt != nil {
		t.Fatalf("canceled read was recorded as a release failure: %+v %v", state, err)
	}
}

// fakeSylveBuild copies the test binary and rewrites its embedded build info,
// which is all a release download verifies.
func fakeSylveBuild(t *testing.T, commit, builtAt string) []byte {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := elf.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	section := file.Section(".go.buildinfo")
	file.Close()
	data, err := os.ReadFile(self)
	if err != nil || section == nil {
		t.Fatalf("no build info: %v", err)
	}
	info := debug.BuildInfo{Path: "github.com/alchemillahq/sylve/cmd/sylve", Settings: []debug.BuildSetting{
		{Key: "-ldflags", Value: "-X github.com/alchemillahq/sylve/internal/cmd.Commit=" + commit + " -X github.com/alchemillahq/sylve/internal/cmd.BuiltAt=" + builtAt},
		{Key: "GOARCH", Value: runtime.GOARCH}, {Key: "GOOS", Value: "freebsd"}, {Key: "vcs.revision", Value: commit},
	}}
	module := strings.Repeat("\x00", 16) + info.String() + strings.Repeat("\x00", 16)
	blob := binary.AppendUvarint(nil, uint64(len(runtime.Version())))
	blob = append(blob, runtime.Version()...)
	blob = binary.AppendUvarint(blob, uint64(len(module)))
	copy(data[section.Offset+32:section.Offset+section.Size], append(blob, module...))
	return data
}

func TestSylveApplyInstallsReleaseAndKeepsPreviousBinary(t *testing.T) {
	s := testService(t)
	s.standalonePath = s.executable
	s.ownership = func(context.Context, *pkgmanager.Client, string) (bool, string, error) {
		return false, "", nil
	}
	s.command = func(context.Context, string, ...string) (string, error) { return "15.1-RELEASE", nil }
	s.restart = func() {}
	commit, builtAt := strings.Repeat("b", 40), "2026-10-24T12:00:00Z"
	build := fakeSylveBuild(t, commit, builtAt)
	sum := sha256.Sum256(build)
	target := sylveupdate.Candidate{Channel: "rolling", Tag: "tip", Version: "0.3.2", Commit: commit, SHA256: hex.EncodeToString(sum[:]), AssetID: 1, Size: int64(len(build)), URL: "https://github.com/AlchemillaHQ/Sylve/releases/download/tip/sylve-" + runtime.GOARCH, FreeBSD: "15.1-RELEASE", BuiltAt: builtAt}
	s.releases.HTTP.Transport = releaseTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != target.URL {
			return nil, errors.New("unexpected_release_request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(build))}, nil
	})
	op := models.HostUpdateOperation{UUID: uuid.NewString(), Action: "sylve-apply", PlanID: uuid.NewString()}
	if err := s.DB.Create(&op).Error; err != nil {
		t.Fatal(err)
	}
	plan := models.HostUpdatePlan{ID: op.PlanID, Kind: "sylve", Release: &target, ExecutableHash: s.runningDigest, ExpiresAt: time.Now().Add(time.Hour), AppliedOperationID: &op.ID}
	if err := s.DB.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.apply(t.Context(), &op, io.Discard); err != nil {
		t.Fatal(err)
	}
	job, err := s.Job(t.Context(), op.ID)
	if err != nil || job.Phase != "restarting" || job.RestartDigest != target.SHA256 {
		t.Fatalf("release install did not hand over to the restart: %+v %v", job, err)
	}
	if digest, err := sylveupdate.FileDigest(s.executable); err != nil || digest != target.SHA256 {
		t.Fatalf("release was not installed: %s %v", digest, err)
	}
	previous, err := os.Stat(s.executable + ".prev")
	if err != nil || !os.SameFile(previous, s.executableInfo) {
		t.Fatalf("previous binary was not kept as a hard link: %v", err)
	}
}
