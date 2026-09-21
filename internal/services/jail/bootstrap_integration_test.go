// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/pkg/utils"
)

const sfImmutable = 0x00020000

type integrationRoot struct {
	root     string
	pkgDBDir string
	env      []string
}

func TestIntegrationBootstrapPkgOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("pkgbase integration test requires network access; skipped in short mode")
	}
	if runtime.GOOS != "freebsd" {
		t.Skip("pkgbase integration test requires FreeBSD")
	}
	if os.Geteuid() != 0 {
		t.Skip("pkgbase integration test requires root")
	}

	pkgPath, err := resolveAndValidatePkg(
		context.Background(),
		exec.LookPath,
		utils.RunCommandWithEnvContext,
		os.Environ(),
	)
	if err != nil {
		t.Skipf("pkg preflight unavailable: %v", err)
	}

	archOutput, err := utils.RunCommand("sysctl", "-n", "hw.machine_arch")
	if err != nil {
		t.Skipf("machine arch unavailable: %v", err)
	}
	arch := strings.TrimSpace(archOutput)
	if arch == "" {
		t.Skip("machine arch is empty")
	}

	major, minor := 15, 1
	hostKeyDir := fmt.Sprintf("/usr/share/keys/pkgbase-%d", major)
	if _, err := os.Stat(filepath.Join(hostKeyDir, "trusted")); err != nil {
		t.Skipf("pkgbase signing keys unavailable: %v", err)
	}

	workDir := t.TempDir()
	repoDir := filepath.Join(workDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	repoName := fmt.Sprintf("FreeBSD-base-release-%d", minor)
	repoConf := fmt.Sprintf(`%s: {
    url:              "pkg+https://pkg.freebsd.org/${ABI}/base_release_%d",
    mirror_type:      "srv",
    signature_type:   "fingerprints",
    fingerprints:     "/usr/share/keys/pkgbase-%d",
    enabled:          yes
}
`, repoName, minor, major)
	if err := os.WriteFile(filepath.Join(repoDir, repoName+".conf"), []byte(repoConf), 0644); err != nil {
		t.Fatal(err)
	}

	abi := fmt.Sprintf("FreeBSD:%d:%s", major, arch)
	osVersion := fmt.Sprintf("%d00000", major)
	fingerprintsRelPath := fmt.Sprintf("/usr/share/keys/pkgbase-%d", major)

	prepare := func(installAsUser bool) integrationRoot {
		root := filepath.Join(workDir, fmt.Sprintf("root-%t", installAsUser))
		if err := os.MkdirAll(filepath.Join(root, "usr", "share", "keys"), 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := utils.RunCommand("cp", "-a", hostKeyDir, filepath.Join(root, "usr", "share", "keys")+"/"); err != nil {
			t.Fatalf("copy signing keys: %v", err)
		}

		env := utils.FilterEnv(os.Environ(), "INSTALL_AS_USER")
		if installAsUser {
			env = append(env, "INSTALL_AS_USER=yes")
		}
		for _, entry := range env {
			if !installAsUser && strings.HasPrefix(entry, "INSTALL_AS_USER") {
				t.Fatalf("fixed environment leaked %q", entry)
			}
		}

		return integrationRoot{
			root:     root,
			pkgDBDir: filepath.Join(root, "var", "db", "pkg"),
			env:      env,
		}
	}

	cfgFor := func(target integrationRoot) bootstrapPkgArgsConfig {
		return bootstrapPkgArgsConfig{
			MountPoint:          target.root,
			RepoConfDir:         repoDir,
			OSVersion:           osVersion,
			ABI:                 abi,
			Major:               major,
			Minor:               minor,
			FingerprintsRelPath: fingerprintsRelPath,
			PkgDBDir:            target.pkgDBDir,
		}
	}

	updateRepo := func(target integrationRoot) {
		result, runErr := utils.RunCommandWithEnvContext(
			context.Background(),
			target.env,
			pkgPath,
			buildBootstrapPkgArgs(cfgFor(target), "update", "-r", repoName)...,
		)
		if runErr != nil || result.ExitCode != 0 {
			t.Skipf("pkgbase repository unavailable: %v\n%s", runErr, result.Output)
		}
	}

	installRoot := func(installAsUser bool) integrationRoot {
		target := prepare(installAsUser)
		updateRepo(target)
		for _, subcmd := range [][]string{
			{"install", "-r", repoName, "FreeBSD-runtime", "FreeBSD-dma"},
			{"install", "pkg"},
		} {
			result, runErr := utils.RunCommandWithEnvContext(
				context.Background(),
				target.env,
				pkgPath,
				buildBootstrapPkgArgs(cfgFor(target), subcmd...)...,
			)
			if runErr != nil || result.ExitCode != 0 {
				t.Fatalf("pkg %v failed: %v\n%s", subcmd, runErr, result.Output)
			}
		}
		return target
	}

	control := installRoot(true)
	fixed := installRoot(false)
	t.Cleanup(func() {
		clearIntegrationFlags(control.root)
		clearIntegrationFlags(fixed.root)
	})

	assertIntegrationStat(t, fixed.root, "var/mail", 0, 6, 0775)
	assertIntegrationStat(t, fixed.root, "var/spool/dma", 0, 6, 0770)
	assertIntegrationStat(t, fixed.root, "usr/libexec/dma", 0, 6, 02555)
	assertIntegrationStat(t, fixed.root, "usr/libexec/dma-mbox-create", 0, 6, 04554)
	assertIntegrationStat(t, control.root, "var/mail", 0, 0, 0775)
	assertIntegrationStat(t, control.root, "var/spool/dma", 0, 0, 0770)
	assertIntegrationStat(t, control.root, "usr/libexec/dma", 0, 0, 02555)
	assertIntegrationStat(t, control.root, "usr/libexec/dma-mbox-create", 0, 0, 04554)

	emptyFixed := statIntegrationPath(t, filepath.Join(fixed.root, "var", "empty"))
	if emptyFixed.Flags&sfImmutable == 0 {
		t.Fatalf("/var/empty lacks schg in the fixed root: flags=%#x", emptyFixed.Flags)
	}
	emptyControl := statIntegrationPath(t, filepath.Join(control.root, "var", "empty"))
	if emptyControl.Flags&sfImmutable != 0 {
		t.Fatalf("/var/empty unexpectedly has schg in the control root: flags=%#x", emptyControl.Flags)
	}

	fixedAudit := runIntegrationAudit(t, pkgPath, fixed, abi)
	if fixedAudit.ExitCode != 0 {
		t.Fatalf("fixed root audit failed: exit=%d\n%s", fixedAudit.ExitCode, fixedAudit.Output)
	}

	controlAudit := runIntegrationAudit(t, pkgPath, control, abi)
	if controlAudit.ExitCode == 0 {
		t.Fatalf("control root audit unexpectedly passed:\n%s", controlAudit.Output)
	}
	if !strings.Contains(controlAudit.Output, "[gname]") {
		t.Fatalf("control audit did not report ownership mismatches:\n%s", controlAudit.Output)
	}

	if err := syscall.Chflags(filepath.Join(fixed.root, "var", "empty"), 0); err != nil {
		t.Fatalf("clear schg: %v", err)
	}
	flagAudit := runIntegrationAudit(t, pkgPath, fixed, abi)
	if flagAudit.ExitCode == 0 {
		t.Fatalf("audit passed with schg removed:\n%s", flagAudit.Output)
	}
	if !strings.Contains(flagAudit.Output, "[fflags]") {
		t.Fatalf("audit did not report a flag mismatch:\n%s", flagAudit.Output)
	}
	if err := syscall.Chflags(filepath.Join(fixed.root, "var", "empty"), int(emptyFixed.Flags)); err != nil {
		t.Fatalf("restore schg: %v", err)
	}
	if restored := runIntegrationAudit(t, pkgPath, fixed, abi); restored.ExitCode != 0 {
		t.Fatalf("audit failed after restoring schg: exit=%d\n%s", restored.ExitCode, restored.Output)
	}

	gname := manifestGnameForPath(t, filepath.Join(fixed.root, "var", "cache", "pkg"), "/var/mail")
	if gname == "" {
		t.Fatal("manifest does not declare a group for /var/mail")
	}
	targetGID, ok := targetGroupID(t, fixed.root, gname)
	if !ok {
		t.Fatalf("target root does not define group %q", gname)
	}
	mailStat := statIntegrationPath(t, filepath.Join(fixed.root, "var", "mail"))
	if int(mailStat.Gid) != targetGID {
		t.Fatalf("target-resolved gid = %d, file gid = %d", targetGID, mailStat.Gid)
	}

	groupPath := filepath.Join(fixed.root, "etc", "group")
	groupStat := statIntegrationPath(t, groupPath)
	groupData, err := os.ReadFile(groupPath)
	if err != nil {
		t.Fatal(err)
	}
	diverged := strings.Replace(
		string(groupData),
		fmt.Sprintf("%s:*:%d:", gname, targetGID),
		fmt.Sprintf("%s:*:65000:", gname),
		1,
	)
	if diverged == string(groupData) {
		t.Fatalf("failed to inject target group divergence for %q", gname)
	}
	if err := os.WriteFile(groupPath, []byte(diverged), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(
		groupPath,
		time.Unix(groupStat.Atimespec.Sec, groupStat.Atimespec.Nsec),
		time.Unix(groupStat.Mtimespec.Sec, groupStat.Mtimespec.Nsec),
	); err != nil {
		t.Fatal(err)
	}

	if hostAudit := runIntegrationAudit(t, pkgPath, fixed, abi); hostAudit.ExitCode != 0 {
		t.Fatalf("host-resolved audit changed after target group divergence: %s", hostAudit.Output)
	}
	divergedGID, ok := targetGroupID(t, fixed.root, gname)
	if !ok || divergedGID == targetGID {
		t.Fatalf("target group divergence not detected: gid=%d", divergedGID)
	}
}

func runIntegrationAudit(t *testing.T, pkgPath string, target integrationRoot, abi string) utils.CommandResult {
	t.Helper()

	result, err := utils.RunCommandWithEnvContext(
		context.Background(),
		target.env,
		pkgPath,
		buildBootstrapAuditArgs(target.root, target.pkgDBDir, abi)...,
	)
	if err != nil && result.Output == "" && result.ExitCode == 0 {
		t.Fatalf("audit execution failed without output: %v", err)
	}
	return result
}

func statIntegrationPath(t *testing.T, path string) syscall.Stat_t {
	t.Helper()

	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return st
}

func assertIntegrationStat(t *testing.T, root, rel string, uid, gid uint32, mode uint32) {
	t.Helper()

	st := statIntegrationPath(t, filepath.Join(root, rel))
	if st.Uid != uid || st.Gid != gid || uint32(st.Mode)&07777 != mode {
		t.Fatalf(
			"%s: uid=%d gid=%d mode=%#o, want uid=%d gid=%d mode=%#o",
			rel, st.Uid, st.Gid, uint32(st.Mode)&07777, uid, gid, mode,
		)
	}
}

func manifestGnameForPath(t *testing.T, cacheDir, target string) string {
	t.Helper()

	archives, err := filepath.Glob(filepath.Join(cacheDir, "FreeBSD-runtime-*.pkg"))
	if err != nil || len(archives) == 0 {
		t.Fatalf("runtime archive not found in %s: %v", cacheDir, err)
	}

	output, err := utils.RunCommand("tar", "-xOf", archives[0], "+MANIFEST")
	if err != nil {
		t.Fatalf("read manifest %s: %v", archives[0], err)
	}

	entryPattern := regexp.MustCompile(`(?s)"` + regexp.QuoteMeta(target) + `"\s*:\s*\{(.*?)\}`)
	entry := entryPattern.FindStringSubmatch(output)
	if entry == nil {
		t.Fatalf("manifest lacks an entry for %s", target)
	}
	gnamePattern := regexp.MustCompile(`"gname"\s*:\s*"([^"]*)"`)
	gname := gnamePattern.FindStringSubmatch(entry[1])
	if gname == nil {
		return ""
	}
	return gname[1]
}

func targetGroupID(t *testing.T, root, name string) (int, bool) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, "etc", "group"))
	if err != nil {
		t.Fatalf("read target group file: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 3 || fields[0] != name {
			continue
		}
		gid, err := strconv.Atoi(fields[2])
		if err != nil {
			return 0, false
		}
		return gid, true
	}
	return 0, false
}

func clearIntegrationFlags(root string) {
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		_ = syscall.Chflags(path, 0)
		return nil
	})
}
