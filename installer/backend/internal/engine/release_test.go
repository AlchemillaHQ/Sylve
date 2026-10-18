// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestTipUsesGitHubAssetDigest(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	digest := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, digest, url, metadata string
		metadataStatus              int
		wantError                   bool
	}{
		{name: "GitHub SHA-256", digest: "sha256:" + digest},
		{name: "uppercase hash", digest: "sha256:" + strings.ToUpper(digest)},
		{name: "missing digest", wantError: true},
		{name: "unsupported digest", digest: "sha512:" + digest, wantError: true},
		{name: "short digest", digest: "sha256:" + digest[:63], wantError: true},
		{name: "invalid digest", digest: "sha256:" + strings.Repeat("z", 64), wantError: true},
		{name: "insecure download URL", digest: "sha256:" + digest, url: "http://example.test/" + tipAsset, wantError: true},
		{name: "metadata unavailable", digest: "sha256:" + digest, metadataStatus: http.StatusNotFound},
		{name: "invalid metadata", digest: "sha256:" + digest, metadata: "{"},
		{name: "incomplete metadata", digest: "sha256:" + digest, metadata: "{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = old })
			apiRequests, metadataRequests := 0, 0
			http.DefaultTransport = releaseTransport(func(req *http.Request) (*http.Response, error) {
				var body string
				status := http.StatusOK
				switch {
				case strings.HasSuffix(req.URL.Path, "/sylve-version.json"):
					metadataRequests++
					body = fmt.Sprintf(`{"version":"0.4.0","commit":%q}`, commit)
					if tc.metadata != "" {
						body = tc.metadata
					}
					if tc.metadataStatus != 0 {
						status = tc.metadataStatus
					}
				case req.URL.Host == "api.github.com" && strings.HasSuffix(req.URL.Path, "/releases/tags/tip"):
					apiRequests++
					url := tc.url
					if url == "" {
						url = "https://example.test/" + tipAsset
					}
					data, err := json.Marshal(map[string]any{
						"target_commitish": "master",
						"assets": []map[string]string{{
							"name": tipAsset, "browser_download_url": url,
							"digest": tc.digest,
						}},
					})
					if err != nil {
						return nil, err
					}
					body = string(data)
				default:
					return nil, fmt.Errorf("unexpected request: %s", req.URL)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})

			artifact, err := sylveTipArtifacts()
			if (err != nil) != tc.wantError {
				t.Fatalf("sylveTipArtifacts() = %v, want error %v", err, tc.wantError)
			}
			if !tc.wantError {
				wantVersion, wantCommit := "0.4.0", commit
				if tc.metadataStatus != 0 || tc.metadata != "" {
					wantVersion, wantCommit = "", "master"
				}
				if artifact.digest != digest || artifact.commit != wantCommit || artifact.version != wantVersion || artifact.url != "https://example.test/"+tipAsset {
					t.Fatalf("unexpected release artifact: %+v", artifact)
				}
			}
			if apiRequests != 1 || (!tc.wantError && metadataRequests != 1) || (tc.wantError && metadataRequests != 0) {
				t.Fatalf("unexpected lookups: %d API, %d metadata", apiRequests, metadataRequests)
			}
		})
	}
}
