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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/installer/backend/internal/assets"
)

var sylveServices = []string{
	"dhcp-server", "jails", "samba-server", "virtualization",
	"wol-server", "firewall", "wireguard", "iscsi", "mdns",
}

var sylveBaseDeps = []string{"bhyve-firmware", "dnsmasq", "libvirt", "qemu-tools", "swtpm"}

const (
	sylveSambaFallback = "samba424"
	sylveAssetsBase    = "https://github.com/" + tipRepo + "/releases/download/tip"
)

var (
	releaseCommit string

	sambaNameRe  = regexp.MustCompile(`^(samba4[0-9]+)-`)
	hexCommitRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	bsdSumLineRe = regexp.MustCompile(`^SHA256 \(([^)]+)\) = ([0-9a-fA-F]{64})$`)
	hex64Re      = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

func resolveSambaPackage() string {
	out, err := exec.Command(pkgBinary(), pkgArgs("search", "^samba4")...).Output()
	if err != nil {
		return sylveSambaFallback
	}
	best, bestNum := "", -1
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		m := sambaNameRe.FindStringSubmatch(fields[0])
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(m[1], "samba4"))
		if err != nil {
			continue
		}
		if n > bestNum {
			bestNum, best = n, m[1]
		}
	}
	if best == "" {
		return sylveSambaFallback
	}
	return best
}

func (e *Engine) installSylvePackages() (err error) {
	var mounted []string
	defer func() {
		for i := len(mounted) - 1; i >= 0; i-- {
			err = errors.Join(err, e.cmd("/sbin/umount", mounted[i]))
		}
	}()
	type packageMount struct{ kind, source, target, options string }
	mounts := []packageMount{
		{"devfs", "devfs", mountRoot + "/dev", "rw"},
	}
	if offlinePayloadAvailable() {
		mounts = append(mounts, packageMount{
			"nullfs", offlineRepoDir, mountRoot + offlineRepoDir, "ro",
		})
	}
	for _, mount := range mounts {
		if err := os.MkdirAll(mount.target, 0o755); err != nil {
			return err
		}
		if err := e.cmd("/sbin/mount", "-t", mount.kind, "-o", mount.options, mount.source, mount.target); err != nil {
			return err
		}
		mounted = append(mounted, mount.target)
	}
	if _, err := os.Stat("/etc/resolv.conf"); err == nil {
		if err := e.cmd("/bin/cp", "/etc/resolv.conf", mountRoot+"/etc/resolv.conf"); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	install := func() error {
		deps := append(append([]string{}, sylveBaseDeps...), resolveSambaPackage())
		e.logf("sylve deps: %s", strings.Join(deps, " "))
		args := append([]string{"-c", mountRoot, "-o", "REPOS_DIR=/etc/pkg,/usr/local/etc/pkg/repos", "install", "-y"}, deps...)
		return e.cmdEnv([]string{"ASSUME_ALWAYS_YES=yes", "PATH=/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin"}, pkgBinary(), args...)
	}
	err = install()
	if err == nil || e.repoOffline || !offlinePayloadAvailable() {
		return err
	}
	e.logf("online Sylve deps failed (%v) - retrying from the bundled offline payload", err)
	if err := e.writeRepoConfigs(true); err != nil {
		return err
	}
	return install()
}

type tipArtifact struct {
	url     string
	digest  string
	version string
	commit  string
}

func sylveTipArtifacts() (tipArtifact, error) {
	url, digest, commit, err := sylveTipRelease()
	if err != nil {
		return tipArtifact{}, err
	}
	artifact := tipArtifact{url: url, digest: digest, commit: commit}
	metaRaw, err := httpFetch(sylveAssetsBase + "/sylve-version.json")
	if err != nil {
		return artifact, nil
	}
	var meta struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if json.Unmarshal(metaRaw, &meta) == nil && meta.Version != "" && hexCommitRe.MatchString(meta.Commit) {
		artifact.version, artifact.commit = meta.Version, meta.Commit
	}
	return artifact, nil
}

func sumsDigestFor(text, name string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := bsdSumLineRe.FindStringSubmatch(line); m != nil {
			if m[1] == name {
				return strings.ToLower(m[2])
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && hex64Re.MatchString(fields[0]) && fields[len(fields)-1] == name {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

func httpFetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "freebsd-sylve-installer")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func (e *Engine) fetchTip(dest string) error {
	if releaseCommit != "" {
		digest, version, commit, err := bundledTip()
		if err != nil {
			return fmt.Errorf("read bundled Sylve release: %w", err)
		}
		if commit != releaseCommit || version == "" {
			return fmt.Errorf("bundled Sylve metadata does not match release commit %s", releaseCommit)
		}
		e.logf("sylve: using bundled release %s (%s)", version, commit)
		return copyFileVerified(offlineTipPath(), dest, digest)
	}

	var onlineErr error
	if !offlinePayloadAvailable() || hostReachable("https://github.com/") {
		art, err := sylveTipArtifacts()
		if err == nil {
			if art.version != "" {
				e.logf("sylve tip: %s %s (%s)", art.url, art.version, art.commit)
			} else {
				e.logf("sylve tip: %s (%s)", art.url, art.commit)
			}
			if derr := download(art.url, dest, art.digest); derr == nil {
				return nil
			} else {
				onlineErr = derr
			}
		} else {
			onlineErr = err
		}
	} else {
		onlineErr = fmt.Errorf("github.com unreachable")
	}

	digest, version, commit, err := bundledTip()
	if err != nil {
		return fmt.Errorf("sylve tip download failed: %w", onlineErr)
	}
	if version != "" {
		e.logf("sylve tip: using bundled payload %s (%s)", version, commit)
	} else {
		e.logf("sylve tip: using bundled payload (%s)", commit)
	}
	return copyFileVerified(offlineTipPath(), dest, digest)
}

func (e *Engine) installSylveRc() error {
	data, err := assets.FS.ReadFile("rcd/sylve")
	if err != nil {
		return err
	}
	dest := mountRoot + "/usr/local/etc/rc.d/sylve"
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o555)
}

type bootstrapDoc struct {
	Version     int               `json:"version"`
	Initialized bool              `json:"initialized"`
	Services    []string          `json:"services,omitempty"`
	Pools       []string          `json:"pools,omitempty"`
	DNS         []string          `json:"dns,omitempty"`
	Switches    []bootstrapSwitch `json:"switches,omitempty"`
}

type bootstrapSwitch struct {
	Type     string                   `json:"type"`
	Standard *bootstrapStandardSwitch `json:"standard,omitempty"`
}

type bootstrapStandardSwitch struct {
	Name           string            `json:"name"`
	Ports          []string          `json:"ports"`
	DHCP           bool              `json:"dhcp,omitempty"`
	SLAAC          bool              `json:"slaac,omitempty"`
	DisableIPv6    bool              `json:"disableIPv6,omitempty"`
	DefaultRoute   bool              `json:"defaultRoute,omitempty"`
	DefaultRoute6  bool              `json:"defaultRoute6,omitempty"`
	Network4Manual string            `json:"network4Manual,omitempty"`
	Gateway4Manual string            `json:"gateway4Manual,omitempty"`
	Network6Manual string            `json:"network6Manual,omitempty"`
	Gateway6Manual string            `json:"gateway6Manual,omitempty"`
	BridgeMAC      map[string]string `json:"bridgeMac"`
}

func (e *Engine) sylveBootstrapDoc() bootstrapDoc {
	doc := bootstrapDoc{
		Version:     1,
		Initialized: true,
		Services:    append([]string{}, sylveServices...),
	}
	if !e.ufsMode() {
		doc.Pools = []string{e.poolName()}
	}

	n := e.plan.Network
	if n.Mode == "static" && n.DNS != "" {
		doc.DNS = append(doc.DNS, n.DNS)
	}
	if n.Mode6 == "static" && n.DNS6 != "" {
		doc.DNS = append(doc.DNS, n.DNS6)
	}

	uplink := strings.TrimSpace(n.Uplink)
	sw := &bootstrapStandardSwitch{
		Name:  "lan",
		Ports: []string{},
	}
	if uplink != "" {
		sw.Ports = []string{uplink}
		sw.BridgeMAC = map[string]string{"mode": "port", "port": uplink}
	}
	if n.Mode == "static" {
		sw.Network4Manual = n.Address
		sw.Gateway4Manual = n.Gateway
		sw.DefaultRoute = n.Gateway != ""
	} else {
		sw.DHCP = true
		sw.DefaultRoute = true
	}
	switch n.Mode6 {
	case "slaac":
		sw.SLAAC = true
		sw.DefaultRoute6 = true
	case "static":
		sw.Network6Manual = n.Address6
		sw.Gateway6Manual = n.Gateway6
		sw.DefaultRoute6 = sw.Network6Manual != ""
	default:
		sw.DisableIPv6 = true
	}
	doc.Switches = []bootstrapSwitch{{Type: "standard", Standard: sw}}
	return doc
}

func (e *Engine) writeSylveBootstrap() error {
	blob, err := json.MarshalIndent(e.sylveBootstrapDoc(), "", "    ")
	if err != nil {
		return err
	}
	return writeFile(mountRoot+"/usr/local/etc/sylve/bootstrap.json", string(blob)+"\n")
}
