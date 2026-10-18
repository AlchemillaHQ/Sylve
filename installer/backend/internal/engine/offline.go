// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

var (
	offlineRoot    = "/var/db/freebsd-sylve/offline"
	offlineRepoDir = offlineRoot + "/repo"
	offlineTipDir  = offlineRoot + "/tip"
)

const (
	offlineBaseRepoConf = `FreeBSD-base {
  url: "file:///var/db/freebsd-sylve/offline/repo",
  signature_type: "none",
  enabled: yes
}
`

	offlinePortsRepoConf = `FreeBSD-ports {
  url: "file:///var/db/freebsd-sylve/offline/repo",
  signature_type: "none",
  enabled: yes
}
`
	offlineKmodsRepoConf = `FreeBSD-ports-kmods {
  enabled: no
}
`
)

type OfflinePayloadInfo struct {
	Available  bool   `json:"available"`
	BakedAt    string `json:"bakedAt,omitempty"`
	Packages   int    `json:"packages,omitempty"`
	TipVersion string `json:"tipVersion,omitempty"`
	TipCommit  string `json:"tipCommit,omitempty"`
}

type offlineManifest struct {
	BakedAt  string `json:"bakedAt"`
	Packages int    `json:"packages"`
	Tip      struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"tip"`
}

func offlineTipPath() string { return offlineTipDir + "/" + tipAsset }

func OfflinePayload() OfflinePayloadInfo {
	for _, path := range []string{
		offlineRepoDir + "/meta.conf",
		offlineRepoDir + "/packagesite.pkg",
		offlineTipPath(),
		offlineTipDir + "/SHA256SUMS",
	} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return OfflinePayloadInfo{}
		}
	}
	result := OfflinePayloadInfo{Available: true}
	raw, err := os.ReadFile(offlineRoot + "/manifest.json")
	if err != nil {
		return result
	}
	var manifest offlineManifest
	if json.Unmarshal(raw, &manifest) != nil {
		return result
	}
	result.BakedAt = manifest.BakedAt
	result.Packages = manifest.Packages
	result.TipVersion = manifest.Tip.Version
	result.TipCommit = manifest.Tip.Commit
	return result
}

func offlinePayloadAvailable() bool { return OfflinePayload().Available }

func hostReachable(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "sylve-installer")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func (e *Engine) writeRepoConfigs(offline bool) error {
	base, ports := baseRepoConf, portsRepoConf
	if offline {
		base, ports = offlineBaseRepoConf, offlinePortsRepoConf
	}
	reposDir := mountRoot + "/usr/local/etc/pkg/repos"
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		return err
	}
	if err := writeFile(reposDir+"/FreeBSD-base.conf", base); err != nil {
		return err
	}
	if err := writeFile(reposDir+"/FreeBSD-ports.conf", ports); err != nil {
		return err
	}
	kmods := reposDir + "/FreeBSD-ports-kmods.conf"
	if offline {
		if err := writeFile(kmods, offlineKmodsRepoConf); err != nil {
			return err
		}
	} else {
		_ = os.Remove(kmods)
	}
	e.repoOffline = offline
	return nil
}

func bundledTip() (digest, version, commit string, err error) {
	sums, err := os.ReadFile(offlineTipDir + "/SHA256SUMS")
	if err != nil {
		return "", "", "", err
	}
	digest = sumsDigestFor(string(sums), tipAsset)
	if digest == "" {
		return "", "", "", fmt.Errorf("bundled SHA256SUMS has no entry for %s", tipAsset)
	}
	raw, err := os.ReadFile(offlineTipDir + "/sylve-version.json")
	if err == nil {
		var meta struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		}
		if json.Unmarshal(raw, &meta) == nil {
			version, commit = meta.Version, meta.Commit
		}
	}
	return digest, version, commit, nil
}

func copyFileVerified(src, dst, wantSHA256 string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if wantSHA256 != "" && got != wantSHA256 {
		_ = os.Remove(dst)
		return fmt.Errorf("bundled binary checksum mismatch: want %s got %s", wantSHA256, got)
	}
	return nil
}
