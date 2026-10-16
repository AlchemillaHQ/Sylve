// SPDX-License-Identifier: BSD-2-Clause
//go:build freebsd

package pkgmanager

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestIntegrationJailPackages(t *testing.T) {
	if testing.Short() || os.Geteuid() != 0 {
		t.Skip("requires root on FreeBSD, isolated fixture jail")
	}
	if err := Probe(""); err != nil {
		t.Skip(err)
	}
	if err := os.MkdirAll("/tmp/opencode", 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp/opencode", "sylve-pkg-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	pool, zfsClient := zfstest.DedicatedPool(t)
	datasetName := pool + "/sylve/jails/999"
	zfstest.EnsureDataset(t, zfsClient, datasetName)
	dataset, err := zfsClient.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatal(err)
	}
	root := dataset.Mountpoint
	name := "spkg" + strconv.Itoa(os.Getpid())
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(binary string, args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = Environment()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", binary, args, err, out)
		}
		return string(out)
	}
	for _, p := range []string{"usr/bin", "bin", "etc/pkg", "usr/local/etc/pkg/repos", "var/db/pkg", "var/cache/pkg", "repo", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"/usr/bin/uname", "/bin/sh", "/bin/sleep"} {
		source := p
		if p == "/bin/sh" {
			source = "/rescue/sh"
		}
		if p == "/bin/sleep" {
			source = "/rescue/sleep"
		}
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(root, p), data)
		_ = os.Chmod(filepath.Join(root, p), 0755)
	}
	write(filepath.Join(root, "etc/passwd"), []byte("root:*:0:0:root:/root:/bin/sh\nnobody:*:65534:65534:Unprivileged:/nonexistent:/usr/sbin/nologin\n"))
	write(filepath.Join(root, "etc/group"), []byte("wheel:*:0:root\n"))
	write(filepath.Join(root, "etc/master.passwd"), []byte("root:*:0:0::0:0:root:/root:/bin/sh\nnobody:*:65534:65534::0:0:Unprivileged:/nonexistent:/usr/sbin/nologin\n"))
	run("/usr/sbin/pwd_mkdb", "-d", filepath.Join(root, "etc"), filepath.Join(root, "etc/master.passwd"))
	config := filepath.Join(dir, "host.conf")
	write(config, []byte("PKG_DBDIR: \""+filepath.Join(dir, "host-pkgdb")+"\"\nREPOS_DIR: []\n"))
	create := func(version, out string) {
		t.Helper()
		stage := filepath.Join(dir, "stage-"+version)
		file := "/usr/local/share/sylve-pkg-test/value"
		write(filepath.Join(stage, file), []byte(version))
		manifest := map[string]any{"name": "sylve-pkg-test", "version": version, "origin": "misc/sylve-pkg-test", "comment": "Isolated Sylve test", "desc": "Isolated test", "maintainer": "test@example.invalid", "www": "https://example.invalid", "prefix": "/usr/local", "files": map[string]string{file: "-"}}
		if version == "2.0" {
			manifest["scripts"] = map[string]string{"pre-install": "echo ready > /var/db/pkg/sylve-test-ready; /bin/sleep 3"}
		}
		data, _ := json.Marshal(manifest)
		manifestPath := filepath.Join(dir, "manifest-"+version+".json")
		write(manifestPath, data)
		run(DefaultBinary, "-C", config, "create", "-M", manifestPath, "-r", stage, "-o", out)
	}
	create("1.0", dir)
	create("2.0", filepath.Join(root, "repo"))
	run(DefaultBinary, "-C", config, "repo", filepath.Join(root, "repo"))
	data, err := os.ReadFile(filepath.Join(dir, "sylve-pkg-test-1.0.pkg"))
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "initial.pkg"), data)
	write(filepath.Join(root, "usr/local/etc/pkg/repos/test.conf"), []byte(`test: {url: "file:///repo", enabled: yes, signature_type: "none"}`))
	feed := []byte(`<?xml version="1.0"?><vuxml xmlns="http://www.vuxml.org/apps/vuxml-1"><vuln vid="11111111-2222-3333-4444-555555555555"><topic>Fixture vulnerability</topic><affects><package><name>sylve-pkg-test</name><range><lt>2.0</lt></range></package></affects><description><body xmlns="http://www.w3.org/1999/xhtml"><p>Test only</p></body></description><references><cvename>CVE-2099-1234</cvename></references><dates><discovery>2026-10-15</discovery><entry>2026-10-15</entry></dates></vuln></vuxml>`)
	write(filepath.Join(root, "var/db/pkg/vuln.xml"), feed)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writer := gzip.NewWriter(w)
		defer writer.Close()
		_, _ = writer.Write(feed)
	}))
	t.Cleanup(server.Close)
	write(filepath.Join(root, "usr/local/etc/pkg.conf"), []byte("VULNXML_SITE: \""+server.URL+"/vuln.xml.gz\"\n"))
	run("/usr/sbin/jail", "-c", "name="+name, "path="+root, "host.hostname="+name, "persist", "ip4=disable", "ip6=disable")
	jailRunning := true
	t.Cleanup(func() {
		if !jailRunning {
			return
		}
		cmd := exec.Command("/usr/sbin/jail", "-r", name)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("remove fixture jail: %v %s", err, out)
		}
	})
	jidText := strings.TrimSpace(run("/usr/sbin/jls", "-j", name, "jid"))
	jid, err := strconv.Atoi(jidText)
	if err != nil {
		t.Fatal(err)
	}
	run(DefaultBinary, "-j", name, "add", "/initial.pkg")
	c := Client{Binary: DefaultBinary, Target: Target{JID: jid, Name: name, Root: root, Dataset: datasetName, GUID: dataset.GUID}, Log: os.Stdout}
	ctx := context.Background()
	if err := ValidateTarget(ctx, c.Target); err != nil {
		t.Fatal(err)
	}
	inventory, err := c.Inventory(ctx)
	if err != nil || len(inventory) != 1 {
		t.Fatalf("inventory %+v %v", inventory, err)
	}
	audit, err := c.Audit(ctx, inventory, false)
	if err != nil || len(audit.Packages) != 1 {
		t.Fatalf("audit %+v %v", audit, err)
	}
	repos, repoHash, err := c.Repositories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Refresh(ctx, repos); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CatalogDigest(ctx, repos); err != nil {
		t.Fatal(err)
	}
	plan, err := c.Preview(ctx, repos, []string{"sylve-pkg-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 {
		t.Fatalf("plan %+v", plan)
	}
	if err = c.VerifyCandidates(ctx, plan, repos); err != nil {
		t.Fatal(err)
	}
	t.Run("offline inventory audit refresh and preview never install", func(t *testing.T) {
		run("/usr/sbin/jail", "-r", name)
		jailRunning = false
		offline := c
		offline.Target.JID, offline.Target.Offline = 0, true
		current, err := offline.Inventory(ctx)
		if err != nil || InventoryDigest(current) != InventoryDigest(inventory) {
			t.Fatalf("offline inventory %+v: %v", current, err)
		}
		if _, err = offline.Dependencies(ctx, "sylve-pkg-test"); err != nil {
			t.Fatal(err)
		}
		report, err := offline.Audit(ctx, current, true)
		if err != nil || len(report.Packages) != 1 {
			t.Fatalf("offline audit %+v: %v", report, err)
		}
		offlineRepos, hash, err := offline.Repositories(ctx)
		if err != nil || hash != repoHash {
			t.Fatalf("repository identity changed between jail and chroot: %s != %s: %v", hash, repoHash, err)
		}
		if err = offline.Refresh(ctx, offlineRepos); err != nil {
			t.Fatal(err)
		}
		preview, err := offline.Preview(ctx, offlineRepos, []string{"sylve-pkg-test"})
		if err != nil || preview.Digest != plan.Digest {
			t.Fatalf("offline preview %+v: %v", preview, err)
		}
		if err = offline.VerifyInstalled(ctx, preview, current); err != nil {
			t.Fatal(err)
		}
		if err = offline.VerifyCandidates(ctx, preview, offlineRepos); err != nil {
			t.Fatal(err)
		}
		if _, err = offline.CatalogDigest(ctx, offlineRepos); err != nil {
			t.Fatal(err)
		}
		if err = offline.Apply(ctx, UpgradeArgs(offlineRepos, []string{"sylve-pkg-test"}, false), preview); err == nil {
			t.Fatal("accepted offline installer")
		}
		if _, err = offline.Run(ctx, UpgradeArgs(offlineRepos, []string{"sylve-pkg-test"}, false)...); err == nil {
			t.Fatal("accepted raw offline upgrade")
		}
		if value, err := os.ReadFile(filepath.Join(root, "usr/local/share/sylve-pkg-test/value")); err != nil || string(value) != "1.0" {
			t.Fatalf("offline preview modified installed files: %q %v", value, err)
		}
		if _, err = os.Stat(filepath.Join(root, "var/db/pkg/sylve-test-ready")); !os.IsNotExist(err) {
			t.Fatalf("offline preview executed an installation script: %v", err)
		}
		changed := offline
		changed.Target.GUID = "changed-guid"
		if _, err := changed.Inventory(ctx); err == nil {
			t.Fatal("accepted offline target with changed dataset identity")
		}
		write(filepath.Join(root, "usr/bin/ssh"), []byte("#!/bin/sh\necho unsafe > /var/db/pkg/sylve-test-ssh\n"))
		if err = os.Chmod(filepath.Join(root, "usr/bin/ssh"), 0755); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(root, "usr/local/etc/pkg/repos/test.conf"), []byte(`test: {url: "ssh://example.invalid/repo", enabled: yes, signature_type: "none"}`))
		if err = offline.Refresh(ctx, []Repository{{Name: "test"}}); err == nil {
			t.Fatal("accepted an offline SSH fetch after repository configuration changed")
		}
		if _, err = os.Stat(filepath.Join(root, "var/db/pkg/sylve-test-ssh")); !os.IsNotExist(err) {
			t.Fatalf("offline fetch executed a jail binary: %v", err)
		}
		write(filepath.Join(root, "usr/local/etc/pkg/repos/test.conf"), []byte(`test: {url: "file:///repo", enabled: yes, signature_type: "none"}`))
		run("/usr/sbin/jail", "-c", "name="+name, "path="+root, "host.hostname="+name, "persist", "ip4=disable", "ip6=disable")
		jailRunning = true
		c.Target.JID, err = strconv.Atoi(strings.TrimSpace(run("/usr/sbin/jls", "-j", name, "jid")))
		if err != nil {
			t.Fatal(err)
		}
		if err = ValidateTarget(ctx, offline.Target); err == nil {
			t.Fatal("accepted offline mode for a running jail")
		}
		offline.Target.Name = "different-record"
		if err = ValidateTarget(ctx, offline.Target); err == nil {
			t.Fatal("accepted an offline root used by another running jail")
		}
	})
	t.Run("changed final plan rejected before writes", func(t *testing.T) {
		altered := plan
		altered.Changes = append([]Change(nil), plan.Changes...)
		altered.Changes[0].To = "3.0"
		altered.Seal()
		if err := c.Apply(ctx, UpgradeArgs(repos, []string{"sylve-pkg-test"}, false), altered); err == nil {
			t.Fatal("accepted unapproved final plan")
		}
		current, err := c.Inventory(ctx)
		if err != nil || current[0].Version != "1.0" {
			t.Fatal("mutated before approval", current, err)
		}
	})
	t.Run("unknown and subsequent prompts default to rejection", func(t *testing.T) {
		for _, multiple := range []bool{false, true} {
			receipt := filepath.Join(dir, fmt.Sprintf("receipt-%v", multiple))
			script := filepath.Join(dir, fmt.Sprintf("prompt-%v", multiple))
			text := "#!/bin/sh\nprintf 'Unexpected question? [Y/n]: '\nread answer\nprintf '%s' \"$answer\" > " + receipt + "\n"
			if multiple {
				text = "#!/bin/sh\ncat <<'PLAN'\n" + "The following 1 package(s) will be affected (of 0 checked):\nInstalled packages to be UPGRADED:\n\tsylve-pkg-test: 1.0 -> 2.0\nNumber of packages to be upgraded: 1\nPLAN\nprintf 'Proceed with this action? [y/N]: '\nread answer\nprintf '%s' \"$answer\" > " + receipt + "\ncat <<'PLAN'\nThe following 1 package(s) will be affected (of 0 checked):\nInstalled packages to be UPGRADED:\n\tsylve-pkg-test: 1.0 -> 2.0\nNumber of packages to be upgraded: 1\nPLAN\nprintf 'Proceed with this action? [y/N]: '\nread answer\nprintf '%s' \"$answer\" >> " + receipt + "\n"
			}
			if err := os.WriteFile(script, []byte(text), 0700); err != nil {
				t.Fatal(err)
			}
			fake := Client{Binary: script, Target: c.Target}
			if err := fake.Apply(ctx, []string{"upgrade"}, plan); err == nil {
				t.Fatal("accepted unexpected confirmation")
			}
			answer, err := os.ReadFile(receipt)
			want := "n"
			if multiple {
				want = "yn"
			}
			if err != nil || string(answer) != want {
				t.Fatalf("answers %q want %q: %v", answer, want, err)
			}
		}
	})
	t.Run("installer retains guard and ignores request cancellation", func(t *testing.T) {
		lockPath := filepath.Join(dir, "installer.lock")
		guard, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer guard.Close()
		if err = syscall.Flock(int(guard.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		applyCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		result := make(chan error, 1)
		client := c
		client.Guard = guard
		go func() { result <- client.Apply(applyCtx, UpgradeArgs(repos, []string{"sylve-pkg-test"}, false), plan) }()
		deadline := time.Now().Add(10 * time.Second)
		for {
			if data, err := os.ReadFile(filepath.Join(root, "var/db/pkg/sylve-test-ready")); err == nil && bytes.Contains(data, []byte("ready")) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("fixture pre-upgrade script did not start")
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		_ = guard.Close()
		observer, err := os.OpenFile(lockPath, os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		if err = syscall.Flock(int(observer.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != syscall.EWOULDBLOCK {
			t.Fatalf("installer lost inherited guard: %v", err)
		}
		if err = <-result; err != nil {
			t.Fatal(err)
		}
		if err = syscall.Flock(int(observer.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal("guard retained after installer exit", err)
		}
	})
	inventory, err = c.Inventory(ctx)
	if err != nil || inventory[0].Version != "2.0" {
		t.Fatalf("inventory %+v %v", inventory, err)
	}
	audit, err = c.Audit(ctx, inventory, false)
	if err != nil || len(audit.Packages) != 0 {
		t.Fatalf("audit %+v %v", audit, err)
	}
	t.Run("pkgbase system scope and detached worker", func(t *testing.T) {
		osVersion, err := hostOSVersion()
		if err != nil {
			t.Fatal(err)
		}
		release := fmt.Sprintf("%d.%d", osVersion/100000, osVersion%100000/1000)
		updated := release + "p1"
		abi := strings.TrimSpace(run(DefaultBinary, "-j", name, "config", "ABI"))
		baseNames := []string{"FreeBSD-clibs", "FreeBSD-runtime", "FreeBSD-kernel-man"}
		const configFile = "/etc/sylve-pkgbase-fixture.conf"
		const scriptMarker = "/var/db/pkg/sylve-base-test-script"
		createBase := func(pkgName, version, repository string) {
			t.Helper()
			archiveDir := filepath.Join(root, repository, "All")
			if err := os.MkdirAll(archiveDir, 0755); err != nil {
				t.Fatal(err)
			}
			stage := filepath.Join(dir, "base-stage-"+pkgName+"-"+version)
			file := "/usr/share/sylve-pkgbase-test/" + pkgName
			write(filepath.Join(stage, file), []byte(version))
			manifest := map[string]any{
				"name": pkgName, "version": version, "origin": "base/" + pkgName, "abi": abi,
				"comment": "Isolated pkgbase fixture", "desc": "Isolated pkgbase fixture", "maintainer": "test@example.invalid", "www": "https://example.invalid", "prefix": "/",
				"files": map[string]string{file: "-"}, "annotations": map[string]string{"FreeBSD_version": strconv.FormatUint(uint64(osVersion), 10)},
			}
			if pkgName == "FreeBSD-runtime" {
				value := "vendor=initial\n"
				if version != release {
					value = "vendor=updated\n"
					manifest["scripts"] = map[string]string{"post-install": "echo updated > " + scriptMarker}
				}
				write(filepath.Join(stage, configFile), []byte(value))
				manifest["files"].(map[string]string)[configFile] = "-"
				manifest["config"] = []string{configFile}
			}
			data, _ := json.Marshal(manifest)
			manifestPath := filepath.Join(dir, "base-"+pkgName+"-"+version+".json")
			write(manifestPath, data)
			run(DefaultBinary, "-C", config, "create", "-M", manifestPath, "-r", stage, "-o", archiveDir)
		}
		for _, pkgName := range baseNames {
			createBase(pkgName, release, "base-initial")
			createBase(pkgName, updated, "base-repo")
		}
		run(DefaultBinary, "-C", config, "repo", filepath.Join(root, "base-initial"))
		run(DefaultBinary, "-C", config, "repo", filepath.Join(root, "base-repo"))
		repoFile := filepath.Join(root, "usr/local/etc/pkg/repos/base.conf")
		write(repoFile, []byte(`FreeBSD-base: {url: "file:///base-initial", enabled: yes, signature_type: "none"}`))
		run(DefaultBinary, "-j", name, "update", "-r", "FreeBSD-base")
		install := append([]string{"-j", name, "-o", "REPO_AUTOUPDATE=false", "install", "-y", "-r", "FreeBSD-base"}, baseNames...)
		run(DefaultBinary, install...)
		write(filepath.Join(root, configFile), []byte("vendor=local\n"))
		write(repoFile, []byte(`FreeBSD-base: {url: "file:///base-repo", enabled: yes, signature_type: "none"}`))
		systemClient := c
		systemClient.Scope = ScopeSystem
		systemClient.ManagedPaths = nil
		current, err := systemClient.Inventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		info, err := systemClient.InspectSystem(ctx, current)
		if err != nil {
			t.Fatal(err)
		}
		systemClient.System = &info
		systemRepos, configHash, err := systemClient.Repositories(ctx)
		if err != nil || len(systemRepos) != 1 || systemRepos[0].Name != "FreeBSD-base" {
			t.Fatalf("system repositories %+v: %v", systemRepos, err)
		}
		applicationRepos, _, err := c.Repositories(ctx)
		if err != nil || len(applicationRepos) != 1 || applicationRepos[0].Name != "test" {
			t.Fatalf("application scope included a base repository: %+v %v", applicationRepos, err)
		}
		if err = systemClient.Refresh(ctx, systemRepos); err != nil {
			t.Fatal(err)
		}
		systemPlan, err := systemClient.Preview(ctx, systemRepos, baseNames)
		if err != nil || len(systemPlan.Changes) != len(baseNames) {
			t.Fatalf("system plan %+v: %v", systemPlan, err)
		}
		if err = c.VerifyCandidates(ctx, systemPlan, applicationRepos); err == nil {
			t.Fatal("application policy accepted a base transaction")
		}
		if err = systemClient.VerifyInstalled(ctx, systemPlan, current); err != nil {
			t.Fatal(err)
		}
		if err = systemClient.VerifyCandidates(ctx, systemPlan, systemRepos); err != nil {
			t.Fatal(err)
		}
		t.Run("offline system inspection never installs", func(t *testing.T) {
			run("/usr/sbin/jail", "-r", name)
			jailRunning = false
			offline := systemClient
			offline.Target.JID, offline.Target.Offline = 0, true
			offlineInventory, err := offline.Inventory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			offlineInfo, err := offline.InspectSystem(ctx, offlineInventory)
			if err != nil || offlineInfo != info {
				t.Fatalf("offline system identity %+v: %v", offlineInfo, err)
			}
			preview, err := offline.Preview(ctx, systemRepos, baseNames)
			if err != nil || preview.Digest != systemPlan.Digest {
				t.Fatalf("offline system plan %+v: %v", preview, err)
			}
			if err = offline.VerifyInstalled(ctx, preview, offlineInventory); err != nil {
				t.Fatal(err)
			}
			if err = offline.VerifyCandidates(ctx, preview, systemRepos); err != nil {
				t.Fatal(err)
			}
			if err = offline.Apply(ctx, UpgradeArgs(systemRepos, baseNames, false), preview); err == nil {
				t.Fatal("offline system installer was admitted")
			}
			if value, err := os.ReadFile(filepath.Join(root, configFile)); err != nil || string(value) != "vendor=local\n" {
				t.Fatalf("offline inspection changed configuration: %q %v", value, err)
			}
			if _, err = os.Stat(filepath.Join(root, scriptMarker)); !os.IsNotExist(err) {
				t.Fatalf("offline inspection ran a package script: %v", err)
			}
			run("/usr/sbin/jail", "-c", "name="+name, "path="+root, "host.hostname="+name, "persist", "ip4=disable", "ip6=disable")
			jailRunning = true
			systemClient.Target.JID, err = strconv.Atoi(strings.TrimSpace(run("/usr/sbin/jls", "-j", name, "jid")))
			if err != nil {
				t.Fatal(err)
			}
			c.Target.JID = systemClient.Target.JID
		})
		catalogHash, err := systemClient.CatalogDigest(ctx, systemRepos)
		if err != nil {
			t.Fatal(err)
		}
		work := Work{Binary: systemClient.Binary, Target: systemClient.Target, Arguments: UpgradeArgs(systemRepos, baseNames, false), Plan: systemPlan, InventoryDigest: InventoryDigest(current), RepositoryHash: configHash, CatalogHash: catalogHash, ManagedPaths: systemClient.ManagedPaths, Scope: ScopeSystem, System: &info}
		runWorker := func(label string, work Work, wantError string) {
			t.Helper()
			workerDir := filepath.Join(dir, "system-worker-"+label)
			if err := os.Mkdir(workerDir, 0700); err != nil {
				t.Fatal(err)
			}
			manifest, _ := json.Marshal(work)
			manifestPath := filepath.Join(workerDir, "work.json")
			if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
				t.Fatal(err)
			}
			guard, err := os.OpenFile(filepath.Join(workerDir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			if err = syscall.Flock(int(guard.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			worker := exec.Command(os.Args[0], "-test.run=^TestIntegrationJailPackagesWorker$")
			worker.Env = append(Environment(), "SYLVE_PKG_WORKER_MANIFEST="+manifestPath)
			worker.ExtraFiles = []*os.File{guard}
			output, commandErr := worker.CombinedOutput()
			data, err := os.ReadFile(filepath.Join(workerDir, "result.json"))
			var result WorkResult
			if err != nil || json.Unmarshal(data, &result) != nil {
				t.Fatalf("missing worker result: %v %s", err, output)
			}
			if wantError == "" && (commandErr != nil || result.Error != "") || wantError != "" && (commandErr == nil || !strings.Contains(result.Error, wantError)) {
				logs, _ := os.ReadFile(filepath.Join(workerDir, "output.log"))
				t.Fatalf("worker expected %q, got %q: %v\n%s\n%s", wantError, result.Error, commandErr, output, logs)
			}
		}
		changedHost := info
		changedHost.HostOSVersion++
		invalidWork := work
		invalidWork.System = &changedHost
		runWorker("changed-host", invalidWork, "package_plan_stale")
		invalidWork = work
		invalidWork.Scope, invalidWork.System = "", nil
		invalidWork.CatalogHash, err = c.CatalogDigest(ctx, applicationRepos)
		if err != nil {
			t.Fatal(err)
		}
		runWorker("default-application-scope", invalidWork, "protected_package_change")
		if _, err = os.Stat(filepath.Join(root, scriptMarker)); !os.IsNotExist(err) {
			t.Fatal("rejected workers ran an installation script")
		}
		runWorker("approved", work, "")
		updatedInventory, err := systemClient.Inventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, pkg := range updatedInventory {
			if pkg.Kind == KindBase && pkg.Version != updated || pkg.Name == "sylve-pkg-test" && pkg.Version != "2.0" {
				t.Fatalf("system worker changed the wrong package/version: %+v", pkg)
			}
		}
		if value, err := os.ReadFile(filepath.Join(root, configFile)); err != nil || string(value) != "vendor=local\n" {
			t.Fatalf("system upgrade lost local configuration: %q %v", value, err)
		}
		if value, err := os.ReadFile(filepath.Join(root, configFile+".pkgnew")); err != nil || string(value) != "vendor=updated\n" {
			t.Fatalf("missing configuration conflict result: %q %v", value, err)
		}
		if _, err = os.Stat(filepath.Join(root, scriptMarker)); err != nil {
			t.Fatalf("online system script did not execute: %v", err)
		}
		if _, err = os.Stat(configFile); !os.IsNotExist(err) {
			t.Fatal("system worker wrote a host configuration file")
		}
		t.Run("pkg self upgrade is rejected before any installation", func(t *testing.T) {
			manifest := map[string]any{"name": "pkg", "version": "1", "origin": "ports-mgmt/pkg", "abi": abi, "comment": "Isolated package manager fixture", "desc": "Isolated fixture", "maintainer": "test@example.invalid", "www": "https://example.invalid", "prefix": "/usr/local"}
			data, _ := json.Marshal(manifest)
			write(filepath.Join(root, "pkg-fixture.json"), data)
			run(DefaultBinary, "-j", name, "register", "-M", "/pkg-fixture.json")
			manifest["version"] = "2"
			data, _ = json.Marshal(manifest)
			manifestPath := filepath.Join(dir, "pkg-fixture-new.json")
			write(manifestPath, data)
			run(DefaultBinary, "-C", config, "create", "-M", manifestPath, "-o", filepath.Join(root, "base-repo"))
			if err := os.Remove(filepath.Join(root, "base-repo", "All", "FreeBSD-runtime-"+updated+".pkg")); err != nil {
				t.Fatal(err)
			}
			createBase("FreeBSD-runtime", release+"p2", "base-repo")
			run(DefaultBinary, "-C", config, "repo", filepath.Join(root, "base-repo"))
			run(DefaultBinary, "-j", name, "update", "-f", "-r", "FreeBSD-base")
			preview, err := systemClient.Preview(ctx, systemRepos, baseNames)
			if err != nil || len(preview.Changes) != 1 || preview.Changes[0].Name != "FreeBSD-runtime" {
				t.Fatalf("dry-run changed beyond the base transaction: %+v %v", preview, err)
			}
			if err = systemClient.Apply(ctx, UpgradeArgs(systemRepos, baseNames, false), preview); err == nil || !strings.Contains(err.Error(), "package_plan_changed") {
				t.Fatalf("accepted pkg's self-upgrade before the base transaction: %v", err)
			}
			inventory, err := systemClient.Inventory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, pkg := range inventory {
				if pkg.Name == "pkg" && pkg.Version != "1" || pkg.Kind == KindBase && pkg.Version != updated {
					t.Fatalf("rejected self-upgrade changed installed state: %+v", pkg)
				}
			}
		})
	})
	if _, err = os.Stat("/usr/local/share/sylve-pkg-test/value"); !os.IsNotExist(err) {
		t.Fatal(fmt.Errorf("fixture unexpectedly wrote outside jail: %v", err))
	}
}

func TestIntegrationJailPackagesWorker(t *testing.T) {
	manifest := os.Getenv("SYLVE_PKG_WORKER_MANIFEST")
	if manifest == "" {
		t.Skip("internal subprocess fixture")
	}
	if err := RunWorker(manifest); err != nil {
		t.Fatal(err)
	}
}
