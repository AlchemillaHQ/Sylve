// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package hostupdates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
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

func TestReleaseReadCacheExpiresAndFollowsSavedChannel(t *testing.T) {
	s, requests := releaseReadService(t)
	first, err := s.GetRelease(t.Context(), false)
	if err != nil || first.Release == nil || first.ReleaseChannel != "rolling" || requests.Load() != 2 {
		t.Fatalf("page load did not resolve the published release: %+v %v", first, err)
	}
	if cached, err := s.GetRelease(t.Context(), false); err != nil || cached.Release == nil || !cached.ReleaseCheckedAt.Equal(*first.ReleaseCheckedAt) || requests.Load() != 2 {
		t.Fatalf("fresh metadata was not reused: %+v %v", cached, err)
	}
	expired := time.Now().Add(-releaseCacheTTL - time.Minute)
	s.releaseAttemptAt = expired
	if err := s.DB.Model(&models.HostUpdateState{ID: 1}).Update("release_checked_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	if refreshed, err := s.GetRelease(t.Context(), false); err != nil || refreshed.Release == nil || !refreshed.ReleaseCheckedAt.After(expired) || requests.Load() != 4 {
		t.Fatalf("expired metadata was not refreshed: %+v %v", refreshed, err)
	}
	if _, err := s.SetReleaseChannel(t.Context(), "tagged"); err != nil {
		t.Fatal(err)
	}
	if tagged, err := s.GetRelease(t.Context(), false); err != nil || tagged.Release == nil || tagged.Release.Channel != "tagged" || tagged.ReleaseChannel != "tagged" || requests.Load() != 6 {
		t.Fatalf("channel change reused the previous channel's metadata: %+v %v", tagged, err)
	}
	if _, err := s.GetRelease(t.Context(), true); err != nil || requests.Load() != 8 {
		t.Fatalf("explicit refresh did not bypass the metadata cache: %v", err)
	}
}

func TestConcurrentReleaseReadsReuseOneMetadataFetch(t *testing.T) {
	s, requests := releaseReadService(t)
	start := make(chan struct{})
	results := make(chan error, 8)
	for range cap(results) {
		go func() {
			<-start
			view, err := s.GetRelease(t.Context(), false)
			if err == nil && (view.Release == nil || view.ReleaseChannel != "rolling") {
				err = errors.New("missing_release_metadata")
			}
			results <- err
		}()
	}
	close(start)
	for range cap(results) {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("concurrent page loads repeated the release fetch: %d", requests.Load())
	}
}

func TestCanceledReleaseReadsDoNotPoisonTheCache(t *testing.T) {
	s, requests := releaseReadService(t)
	transport := s.releases.HTTP.Transport
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.releases.HTTP.Transport = releaseTransport(func(request *http.Request) (*http.Response, error) {
		cancel()
		return nil, request.Context().Err()
	})
	if _, err := s.GetRelease(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read did not stop: %v", err)
	}
	state, err := s.state()
	if err != nil || state.ReleaseError != "" || state.ReleaseCheckedAt != nil || !s.releaseAttemptAt.IsZero() {
		t.Fatalf("canceled read was cached as a release failure: %+v %v", state, err)
	}
	s.releases.HTTP.Transport = transport
	if view, err := s.GetRelease(t.Context(), false); err != nil || view.Release == nil || requests.Load() != 2 {
		t.Fatalf("canceled read suppressed the next page load: %+v %v", view, err)
	}
}
