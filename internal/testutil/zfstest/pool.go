// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfstest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
)

const (
	poolPrefix        = "sylve-test-"
	fixtureStateRoot  = "/tmp"
	stateDirPrefix    = "sylve-zfstest-"
	ownerProperty     = "org.alchemilla:sylve-test-owner"
	vdevSize          = 200 * 1024 * 1024
	commandTimeout    = 30 * time.Second
	fixtureMetadata   = "fixture.txt"
	fixtureVdev       = "vdev"
	fixtureMountpoint = "mnt"
)

type ownedPool struct {
	name     string
	owner    string
	stateDir string
}

var sharedPackagePool struct {
	sync.Mutex

	runnerActive bool
	pool         *ownedPool
	poisoned     error
}

var dedicatedPools sync.Map

// Run owns the lifecycle of a package-scoped shared pool. Packages that call
// SharedPool must invoke Run from TestMain.
func Run(m *testing.M) (code int) {
	sharedPackagePool.Lock()
	sharedPackagePool.runnerActive = true
	sharedPackagePool.Unlock()

	defer func() {
		if !tearDownSharedPool() {
			code = 1
		}
	}()
	return m.Run()
}

func tearDownSharedPool() bool {
	sharedPackagePool.Lock()
	defer sharedPackagePool.Unlock()

	pool := sharedPackagePool.pool
	poisoned := sharedPackagePool.poisoned
	sharedPackagePool.pool = nil
	sharedPackagePool.runnerActive = false

	if pool != nil {
		if err := pool.destroy(); err != nil {
			fmt.Fprintf(os.Stderr, "zfstest: shared pool teardown failed: %v\n", err)
			return false
		}
	}

	if poisoned != nil {
		fmt.Fprintf(os.Stderr, "zfstest: shared pool was poisoned: %v\n", poisoned)
		return false
	}
	return true
}

// DedicatedPool creates an independently owned pool for tests that exercise
// pool identity, topology, import/export, or destructive pool-root behavior.
func DedicatedPool(t testing.TB) (poolName string, client *gzfs.Client) {
	t.Helper()
	SkipIfUnavailable(t)

	pool, err := newOwnedPool()
	if err != nil {
		t.Fatalf("create dedicated ZFS pool: %v", err)
	}
	dedicatedPools.Store(pool.name, pool)

	t.Cleanup(func() {
		if err := pool.destroy(); err != nil {
			t.Errorf("clean up dedicated ZFS pool %s: %v", pool.name, err)
		}
	})

	return pool.name, gzfs.NewClient(gzfs.Options{})
}

// ExportDedicatedPool exports a pool created by DedicatedPool after checking
// its in-process identity and on-pool ownership marker.
func ExportDedicatedPool(t testing.TB, poolName string) {
	t.Helper()
	pool := lookupDedicatedPool(t, poolName)
	if err := pool.verifyOwnership(); err != nil {
		t.Fatalf("verify dedicated pool before export: %v", err)
	}
	if _, err := runCommand("zpool", "export", pool.name); err != nil {
		t.Fatalf("export dedicated pool %s: %v", pool.name, err)
	}
}

// ImportDedicatedPool imports a pool created by DedicatedPool from its exact
// recorded state directory and verifies its ownership marker.
func ImportDedicatedPool(t testing.TB, poolName string) {
	t.Helper()
	pool := lookupDedicatedPool(t, poolName)
	if _, err := runCommand(
		"zpool", "import", "-o", "cachefile=none", "-d", pool.stateDir, pool.name,
	); err != nil {
		t.Fatalf("import dedicated pool %s: %v", pool.name, err)
	}
	if err := pool.verifyOwnership(); err != nil {
		t.Fatalf("verify imported dedicated pool: %v", err)
	}
}

func lookupDedicatedPool(t testing.TB, poolName string) *ownedPool {
	t.Helper()
	value, ok := dedicatedPools.Load(poolName)
	if !ok {
		t.Fatalf("dedicated pool %s is not registered in this process", poolName)
	}
	return value.(*ownedPool)
}

// SharedPool leases the package-scoped pool exclusively and resets all of its
// owned descendants when the test finishes. The returned cleanup must be
// deferred immediately so it can observe a panic before testing runs Cleanup.
func SharedPool(t testing.TB) (poolName string, client *gzfs.Client, cleanup func()) {
	t.Helper()
	SkipIfUnavailable(t)

	sharedPackagePool.Lock()
	if !sharedPackagePool.runnerActive {
		sharedPackagePool.Unlock()
		t.Fatal("zfstest.SharedPool requires TestMain to call zfstest.Run")
	}

	var pool *ownedPool
	var once sync.Once
	cleanup = func() {
		panicValue := recover()
		once.Do(func() {
			defer sharedPackagePool.Unlock()
			if pool == nil {
				return
			}
			if panicValue != nil || t.Failed() {
				if err := pool.destroy(); err != nil {
					if sharedPackagePool.poisoned == nil {
						sharedPackagePool.poisoned = err
					}
					t.Errorf("destroy shared ZFS pool %s after test failure: %v", pool.name, err)
					return
				}
				sharedPackagePool.pool = nil
				return
			}
			if err := pool.reset(); err != nil {
				if sharedPackagePool.poisoned == nil {
					sharedPackagePool.poisoned = err
				}
				t.Errorf("reset shared ZFS pool %s: %v", pool.name, err)
			}
		})
		if panicValue != nil {
			panic(panicValue)
		}
	}
	t.Cleanup(cleanup)

	pool = sharedPackagePool.pool
	if sharedPackagePool.poisoned != nil {
		t.Fatalf("shared ZFS pool is unavailable after cleanup failure: %v", sharedPackagePool.poisoned)
	}

	if pool == nil {
		created, err := newOwnedPool()
		if err != nil {
			t.Fatalf("create shared ZFS pool: %v", err)
		}
		pool = created
		sharedPackagePool.pool = pool
	}

	if err := pool.assertClean(); err != nil {
		if sharedPackagePool.poisoned == nil {
			sharedPackagePool.poisoned = err
		}
		t.Fatalf("shared ZFS pool is not clean: %v", err)
	}

	return pool.name, gzfs.NewClient(gzfs.Options{}), cleanup
}

func newOwnedPool() (*ownedPool, error) {
	token := rand.Text()
	poolName := fmt.Sprintf("%s%d-%s", poolPrefix, os.Getpid(), token)
	owner := fmt.Sprintf(
		"run=%s,package=%s,pid=%d,created=%s",
		token,
		filepath.Base(os.Args[0]),
		os.Getpid(),
		time.Now().UTC().Format("20060102T150405Z"),
	)

	stateDir, err := os.MkdirTemp(fixtureStateRoot, stateDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("create fixture state directory: %w", err)
	}
	pool := &ownedPool{
		name:     poolName,
		owner:    owner,
		stateDir: stateDir,
	}
	if err := pool.validate(); err != nil {
		_ = os.Remove(stateDir)
		return nil, err
	}

	metadata := fmt.Appendf(nil, "pool=%q\nowner=%q\nvdev=%q\nmountpoint=%q\n",
		pool.name, pool.owner, pool.statePath(fixtureVdev), pool.statePath(fixtureMountpoint))
	if err := os.WriteFile(pool.statePath(fixtureMetadata), metadata, 0o600); err != nil {
		_ = os.Remove(stateDir)
		return nil, fmt.Errorf("write fixture metadata: %w", err)
	}
	if err := os.Mkdir(pool.statePath(fixtureMountpoint), 0o700); err != nil {
		pool.removeUncreatedState()
		return nil, fmt.Errorf("create fixture mountpoint: %w", err)
	}

	vdevPath := pool.statePath(fixtureVdev)
	if err := os.WriteFile(vdevPath, nil, 0o600); err != nil {
		pool.removeUncreatedState()
		return nil, fmt.Errorf("create fixture vdev: %w", err)
	}
	if err := os.Truncate(vdevPath, vdevSize); err != nil {
		pool.removeUncreatedState()
		return nil, fmt.Errorf("size fixture vdev: %w", err)
	}

	if _, err := runCommand(
		"zpool", "create",
		"-o", "cachefile=none",
		"-O", ownerProperty+"="+pool.owner,
		"-m", pool.statePath(fixtureMountpoint),
		pool.name,
		vdevPath,
	); err != nil {
		return nil, fmt.Errorf("create pool %s (state preserved at %s): %w", pool.name, pool.stateDir, err)
	}
	if err := pool.verifyOwnership(); err != nil {
		return nil, fmt.Errorf("verify new pool %s (state preserved at %s): %w", pool.name, pool.stateDir, err)
	}
	return pool, nil
}

func (pool *ownedPool) assertClean() error {
	if err := pool.verifyOwnership(); err != nil {
		return err
	}
	resources, err := pool.listResources()
	if err != nil {
		return err
	}
	if len(resources) != 1 || resources[0] != pool.name {
		return fmt.Errorf("unexpected pool resources: %s", strings.Join(resources, ", "))
	}
	return nil
}

func (pool *ownedPool) reset() error {
	for {
		if err := pool.verifyOwnership(); err != nil {
			return err
		}
		resources, err := pool.listResources()
		if err != nil {
			return err
		}
		targets, err := cleanupTargets(pool.name, resources)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil
		}

		var failures []error
		destroyed := false
		for _, target := range targets {
			args := []string{"destroy", "-f", target}
			if !strings.Contains(target, "@") {
				args = []string{"destroy", "-r", "-f", target}
			}
			if _, err := runCommand("zfs", args...); err != nil {
				failures = append(failures, fmt.Errorf("destroy %s: %w", target, err))
				continue
			}
			destroyed = true
			break
		}
		if !destroyed {
			return fmt.Errorf("destroy owned resources in %s: %w", pool.name, errors.Join(failures...))
		}
	}
}

func (pool *ownedPool) listResources() ([]string, error) {
	output, err := runCommand(
		"zfs", "list", "-H", "-r", "-d", "1", "-t", "filesystem,volume,snapshot", "-o", "name", pool.name,
	)
	if err != nil {
		return nil, fmt.Errorf("list resources in %s: %w", pool.name, err)
	}
	if output == "" {
		return nil, fmt.Errorf("list resources in %s returned no pool root", pool.name)
	}
	return strings.Split(output, "\n"), nil
}

func cleanupTargets(poolName string, resources []string) ([]string, error) {
	if err := validatePoolName(poolName); err != nil {
		return nil, err
	}
	var targets []string
	for _, resource := range resources {
		if resource == poolName {
			continue
		}
		suffix, ok := strings.CutPrefix(resource, poolName)
		if !ok || len(suffix) < 2 || (suffix[0] != '/' && suffix[0] != '@') ||
			strings.ContainsAny(suffix[1:], "/@#") {
			return nil, fmt.Errorf("resource %q is not directly owned by pool %q", resource, poolName)
		}
		targets = append(targets, resource)
	}
	return targets, nil
}

func (pool *ownedPool) destroy() error {
	if err := pool.validate(); err != nil {
		return err
	}
	imported, err := poolImported(pool.name)
	if err != nil {
		return err
	}
	if !imported {
		if _, err := runCommand(
			"zpool", "import", "-N", "-o", "cachefile=none", "-d", pool.stateDir, pool.name,
		); err != nil {
			return fmt.Errorf(
				"import owned pool %s from %s for cleanup: %w",
				pool.name,
				pool.stateDir,
				err,
			)
		}
	}
	if err := pool.verifyOwnership(); err != nil {
		return err
	}
	if _, err := runCommand("zpool", "destroy", "-f", pool.name); err != nil {
		return fmt.Errorf("destroy owned pool %s: %w", pool.name, err)
	}
	imported, err = poolImported(pool.name)
	if err != nil {
		return err
	}
	if imported {
		return fmt.Errorf("pool %s is still imported after destroy", pool.name)
	}
	if err := pool.removeState(); err != nil {
		return fmt.Errorf("remove state for destroyed pool %s: %w", pool.name, err)
	}
	dedicatedPools.Delete(pool.name)
	return nil
}

func (pool *ownedPool) verifyOwnership() error {
	if err := pool.validate(); err != nil {
		return err
	}
	output, err := runCommand(
		"zfs", "get", "-H", "-o", "value,source", ownerProperty, pool.name,
	)
	if err != nil {
		return fmt.Errorf("read ownership marker from %s: %w", pool.name, err)
	}
	fields := strings.Split(output, "\t")
	if len(fields) != 2 {
		return fmt.Errorf("invalid ownership marker output for %s: %q", pool.name, output)
	}
	if fields[0] != pool.owner || fields[1] != "local" {
		return fmt.Errorf(
			"refusing to mutate pool %s: ownership marker value=%q source=%q",
			pool.name,
			fields[0],
			fields[1],
		)
	}
	return nil
}

func poolImported(poolName string) (bool, error) {
	if err := validatePoolName(poolName); err != nil {
		return false, err
	}
	output, err := runCommand("zpool", "list", "-H", "-o", "name")
	if err != nil {
		return false, fmt.Errorf("list imported pools: %w", err)
	}
	for _, name := range strings.Split(output, "\n") {
		if name == poolName {
			return true, nil
		}
	}
	return false, nil
}

func validatePoolName(poolName string) error {
	if poolName == "" || poolName == "/" || poolName == "zroot" {
		return fmt.Errorf("unsafe test pool name %q", poolName)
	}
	if !strings.HasPrefix(poolName, poolPrefix) {
		return fmt.Errorf("test pool name %q lacks required prefix", poolName)
	}
	if strings.ContainsAny(poolName, "/@#") {
		return fmt.Errorf("test pool name %q contains a dataset or snapshot separator", poolName)
	}
	for i := range len(poolName) {
		char := poolName[i]
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '.' && char != '_' && char != '-' {
			return fmt.Errorf("test pool name %q contains an invalid character", poolName)
		}
	}
	return nil
}

func (pool *ownedPool) validate() error {
	if pool == nil {
		return errors.New("nil owned pool")
	}
	if err := validatePoolName(pool.name); err != nil {
		return err
	}
	if pool.owner == "" {
		return fmt.Errorf("pool %s has an empty ownership marker", pool.name)
	}
	stateDir := filepath.Clean(pool.stateDir)
	if stateDir != pool.stateDir || filepath.Dir(stateDir) != fixtureStateRoot ||
		!strings.HasPrefix(filepath.Base(stateDir), stateDirPrefix) {
		return fmt.Errorf("unsafe fixture state directory %q", pool.stateDir)
	}
	return nil
}

func (pool *ownedPool) statePath(name string) string {
	return filepath.Join(pool.stateDir, name)
}

func (pool *ownedPool) removeState() error {
	if err := pool.validate(); err != nil {
		return err
	}
	info, err := os.Lstat(pool.stateDir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fixture state path %q is not a real directory", pool.stateDir)
	}
	for _, name := range []string{fixtureVdev, fixtureMountpoint} {
		if err := os.Remove(pool.statePath(name)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(pool.stateDir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != fixtureMetadata {
		return fmt.Errorf("unexpected entries remain in fixture state directory %q", pool.stateDir)
	}
	if err := os.Remove(pool.statePath(fixtureMetadata)); err != nil {
		return err
	}
	return os.Remove(pool.stateDir)
}

func (pool *ownedPool) removeUncreatedState() {
	_ = os.Remove(pool.statePath(fixtureVdev))
	_ = os.Remove(pool.statePath(fixtureMountpoint))
	_ = os.Remove(pool.statePath(fixtureMetadata))
	_ = os.Remove(pool.stateDir)
}

func runCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if ctx.Err() != nil {
		return trimmed, fmt.Errorf("%s timed out: %w", name, ctx.Err())
	}
	if err != nil {
		if trimmed == "" {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return trimmed, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, trimmed)
	}
	return trimmed, nil
}
