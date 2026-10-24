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
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const repository = "AlchemillaHQ/Sylve"
const assetBase = "https://github.com/" + repository + "/releases/download/"
const apiBase = "https://api.github.com/repos/" + repository
const MaxBinarySize = 512 << 20

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var tagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Candidate struct {
	Channel string `json:"channel"`
	Tag     string `json:"tag"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	SHA256  string `json:"sha256"`
	AssetID int64  `json:"assetId"`
	Size    int64  `json:"size"`
	URL     string `json:"url"`
	FreeBSD string `json:"freebsd"`
	BuiltAt string `json:"builtAt"`
}

type Installed struct {
	Mode    string `json:"mode"`
	Reason  string `json:"reason"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"builtAt"`
	Channel string `json:"channel"`
	SHA256  string `json:"sha256"`
	Path    string `json:"path"`
}

func ParseBuildTime(value string) (time.Time, error) {
	builtAt, err := time.Parse(time.RFC3339, value)
	if err != nil || builtAt.IsZero() {
		return time.Time{}, fmt.Errorf("sylve_build_time_unknown")
	}
	return builtAt.UTC(), nil
}

type asset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

type Client struct{ HTTP *http.Client }

func NewClient() *Client {
	return &Client{HTTP: &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 || !trustedURL(req.URL) {
				return fmt.Errorf("untrusted_release_redirect")
			}
			return nil
		},
	}}
}

func trustedURL(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Hostname() == "github.com" || u.Hostname() == "api.github.com" || strings.HasSuffix(u.Hostname(), ".githubusercontent.com"))
}

func (c *Client) get(ctx context.Context, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Sylve-host-updates")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("release_fetch_failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release_fetch_failed: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("release_fetch_failed: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release_response_too_large")
	}
	return data, nil
}

func (c *Client) Resolve(ctx context.Context, channel, currentVersion string) (Candidate, error) {
	endpoint := "/releases/latest"
	if channel == "rolling" {
		endpoint = "/releases/tags/tip"
	} else if channel != "tagged" {
		return Candidate{}, fmt.Errorf("invalid_release_channel")
	}
	raw, err := c.get(ctx, apiBase+endpoint, 2<<20)
	if err != nil {
		return Candidate{}, err
	}
	var rel release
	if err = json.Unmarshal(raw, &rel); err != nil {
		return Candidate{}, fmt.Errorf("release_metadata_invalid")
	}
	if rel.Draft || (channel == "rolling" && rel.Tag != "tip") || (channel == "tagged" && (rel.Prerelease || !tagPattern.MatchString(rel.Tag))) {
		return Candidate{}, fmt.Errorf("release_metadata_invalid")
	}
	if channel == "tagged" && semver.IsValid("v"+currentVersion) && semver.Compare(rel.Tag, "v"+currentVersion) <= 0 {
		return Candidate{}, fmt.Errorf("sylve_downgrade_forbidden")
	}
	var binary, metadata *asset
	for i := range rel.Assets {
		entry := &rel.Assets[i]
		switch entry.Name {
		case "sylve-" + runtime.GOARCH:
			if binary != nil {
				return Candidate{}, fmt.Errorf("release_metadata_invalid")
			}
			binary = entry
		case "sylve-version.json":
			if metadata != nil {
				return Candidate{}, fmt.Errorf("release_metadata_invalid")
			}
			metadata = entry
		}
	}
	if binary == nil || metadata == nil {
		return Candidate{}, fmt.Errorf("release_publication_incomplete")
	}
	for _, entry := range []*asset{binary, metadata} {
		if entry.ID <= 0 || entry.Size <= 0 || entry.Size > MaxBinarySize || entry.URL != assetBase+rel.Tag+"/"+entry.Name || !strings.HasPrefix(entry.Digest, "sha256:") || !digestPattern.MatchString(strings.TrimPrefix(entry.Digest, "sha256:")) {
			return Candidate{}, fmt.Errorf("release_metadata_invalid")
		}
	}
	raw, err = c.get(ctx, metadata.URL, 1<<20)
	if err != nil {
		return Candidate{}, err
	}
	sum := sha256.Sum256(raw)
	if "sha256:"+hex.EncodeToString(sum[:]) != metadata.Digest {
		return Candidate{}, fmt.Errorf("release_changed_during_check")
	}
	var identity struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		FreeBSD string `json:"freebsd"`
		BuiltAt string `json:"builtAt"`
	}
	if json.Unmarshal(raw, &identity) != nil || !semver.IsValid("v"+identity.Version) || !commitPattern.MatchString(identity.Commit) || identity.FreeBSD == "" {
		return Candidate{}, fmt.Errorf("release_metadata_invalid")
	}
	builtAt, err := ParseBuildTime(identity.BuiltAt)
	if err != nil {
		return Candidate{}, fmt.Errorf("release_metadata_invalid")
	}
	if channel == "tagged" && rel.Tag != "v"+identity.Version {
		return Candidate{}, fmt.Errorf("release_version_mismatch")
	}
	return Candidate{
		Channel: channel,
		Tag:     rel.Tag,
		Version: identity.Version,
		Commit:  identity.Commit,
		SHA256:  strings.TrimPrefix(binary.Digest, "sha256:"),
		AssetID: binary.ID,
		Size:    binary.Size,
		URL:     binary.URL,
		FreeBSD: identity.FreeBSD,
		BuiltAt: builtAt.Format(time.RFC3339Nano),
	}, nil
}

func Allowed(current Installed, target Candidate) error {
	if current.Mode != "standalone" {
		return fmt.Errorf("sylve_package_managed")
	}
	if !semver.IsValid("v"+current.Version) || !digestPattern.MatchString(current.SHA256) {
		return fmt.Errorf("sylve_build_unknown")
	}
	if !semver.IsValid("v"+target.Version) || !commitPattern.MatchString(target.Commit) || !digestPattern.MatchString(target.SHA256) {
		return fmt.Errorf("release_metadata_invalid")
	}
	comparison := semver.Compare("v"+target.Version, "v"+current.Version)
	if comparison < 0 {
		return fmt.Errorf("sylve_downgrade_forbidden")
	}
	if current.SHA256 == target.SHA256 {
		return fmt.Errorf("sylve_already_current")
	}
	if comparison == 0 && target.Channel == "tagged" {
		return fmt.Errorf("sylve_downgrade_forbidden")
	}
	if target.Channel != "tagged" && target.Channel != "rolling" {
		return fmt.Errorf("invalid_release_channel")
	}
	targetTime, err := ParseBuildTime(target.BuiltAt)
	if err != nil {
		return fmt.Errorf("release_metadata_invalid")
	}
	if comparison == 0 {
		currentTime, err := ParseBuildTime(current.BuiltAt)
		if err != nil {
			return err
		}
		if targetTime.Before(currentTime) {
			return fmt.Errorf("sylve_downgrade_forbidden")
		}
		if targetTime.Equal(currentTime) {
			return fmt.Errorf("sylve_build_order_ambiguous")
		}
	}
	return nil
}

var freebsdVersion = regexp.MustCompile(`^([0-9]+)\.([0-9]+)(?:[-.].*)?$`)

func Compatible(userland, builtFor string) bool {
	local, target := freebsdVersion.FindStringSubmatch(userland), freebsdVersion.FindStringSubmatch(builtFor)
	if local == nil || target == nil {
		return false
	}
	return semver.Compare("v"+local[1]+"."+local[2]+".0", "v"+target[1]+"."+target[2]+".0") >= 0
}

func FileDigest(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (c *Client) Download(ctx context.Context, target Candidate, destination string) error {
	if target.URL != assetBase+target.Tag+"/sylve-"+runtime.GOARCH || (target.Tag != "tip" && !tagPattern.MatchString(target.Tag)) || !digestPattern.MatchString(target.SHA256) || target.Size <= 0 || target.Size > MaxBinarySize {
		return fmt.Errorf("release_metadata_invalid")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", target.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Sylve-host-updates")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("release_fetch_failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("release_fetch_failed: HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, target.Size+1))
	if err != nil {
		return err
	}
	if size != target.Size || hex.EncodeToString(hash.Sum(nil)) != target.SHA256 {
		return fmt.Errorf("release_checksum_mismatch")
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	info, err := buildinfo.ReadFile(destination)
	if err != nil {
		return fmt.Errorf("release_binary_invalid")
	}
	return validateBuild(info, target)
}

func validateBuild(info *debug.BuildInfo, target Candidate) error {
	if info.Path != "github.com/alchemillahq/sylve/cmd/sylve" {
		return fmt.Errorf("release_binary_invalid")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	flags := strings.Fields(settings["-ldflags"])
	const commitFlag = "github.com/alchemillahq/sylve/internal/cmd.Commit="
	const builtAtFlag = "github.com/alchemillahq/sylve/internal/cmd.BuiltAt="
	bakedCommit, bakedBuiltAt := "", ""
	for i, flag := range flags {
		if strings.HasPrefix(flag, commitFlag) && i > 0 && flags[i-1] == "-X" {
			bakedCommit = strings.TrimPrefix(flag, commitFlag)
		} else if strings.HasPrefix(flag, "-X="+commitFlag) {
			bakedCommit = strings.TrimPrefix(flag, "-X="+commitFlag)
		} else if strings.HasPrefix(flag, builtAtFlag) && i > 0 && flags[i-1] == "-X" {
			bakedBuiltAt = strings.TrimPrefix(flag, builtAtFlag)
		} else if strings.HasPrefix(flag, "-X="+builtAtFlag) {
			bakedBuiltAt = strings.TrimPrefix(flag, "-X="+builtAtFlag)
		}
	}
	if settings["GOOS"] != "freebsd" || settings["GOARCH"] != runtime.GOARCH || settings["vcs.revision"] != target.Commit || bakedCommit != target.Commit {
		return fmt.Errorf("release_binary_identity_mismatch")
	}
	builtAt, err := ParseBuildTime(bakedBuiltAt)
	targetTime, targetErr := ParseBuildTime(target.BuiltAt)
	if err != nil || targetErr != nil || !builtAt.Equal(targetTime) {
		return fmt.Errorf("release_binary_identity_mismatch")
	}
	return nil
}
