// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
)

func cleanupPool(name string, paths ...string) *gzfs.ZPoolStatusPool {
	vdevs := map[string]*gzfs.ZPoolStatusVDEV{}
	for _, path := range paths {
		vdevs[path] = &gzfs.ZPoolStatusVDEV{Name: path, Path: path, VdevType: "disk"}
	}
	return &gzfs.ZPoolStatusPool{Name: name, PoolGUID: name + "-guid", Vdevs: vdevs}
}

type testInstallPools struct {
	pools                  map[string]*gzfs.ZPoolStatusPool
	listErr, destroyErr    error
	statusErr              map[string]error
	reads                  map[string]int
	destroyed              []string
	changeGUIDBeforeDelete bool
}

func (p *testInstallPools) GetPoolNames(context.Context) ([]string, error) {
	var names []string
	for name := range p.pools {
		names = append(names, name)
	}
	return names, p.listErr
}

func (p *testInstallPools) GetPoolStatus(_ context.Context, name string) (*gzfs.ZPoolStatusPool, error) {
	if err := p.statusErr[name]; err != nil {
		return nil, err
	}
	if p.reads == nil {
		p.reads = map[string]int{}
	}
	p.reads[name]++
	pool := p.pools[name]
	if pool == nil {
		return nil, fmt.Errorf("missing pool %s", name)
	}
	copy := *pool
	if p.changeGUIDBeforeDelete && p.reads[name] > 1 {
		copy.PoolGUID = "different-pool"
	}
	return &copy, nil
}

func (p *testInstallPools) Destroy(_ context.Context, name string, force bool) error {
	if !force {
		return errors.New("cleanup must request unmount")
	}
	p.destroyed = append(p.destroyed, name)
	return p.destroyErr
}

func TestCleanupTargetPools(t *testing.T) {
	storage := ZFSStorage{Encrypt: true, Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0", "nda1p3"}}}}
	cases := []struct {
		name    string
		plain   bool
		pools   map[string]*gzfs.ZPoolStatusPool
		want    []string
		wantErr bool
	}{
		{name: "no imported pools"},
		{
			name: "selected leftovers permit retry",
			pools: map[string]*gzfs.ZPoolStatusPool{
				"zroot":    cleanupPool("zroot", "/dev/nda0p5.eli", "/dev/nda1p3"),
				"bootpool": cleanupPool("bootpool", "/dev/nda0p3"),
			},
			want: []string{"zroot", "bootpool"},
		},
		{
			name:    "same name on foreign disks",
			pools:   map[string]*gzfs.ZPoolStatusPool{"zroot": cleanupPool("zroot", "/dev/nda9p5.eli")},
			wantErr: true,
		},
		{
			name:    "mixed selected and foreign members",
			pools:   map[string]*gzfs.ZPoolStatusPool{"zroot": cleanupPool("zroot", "/dev/nda0p5.eli", "/dev/nda9p5.eli")},
			wantErr: true,
		},
		{
			name:    "selected partition does not authorize its sibling",
			pools:   map[string]*gzfs.ZPoolStatusPool{"zroot": cleanupPool("zroot", "/dev/nda1p4")},
			wantErr: true,
		},
		{
			name: "check bootpool before destroying root",
			pools: map[string]*gzfs.ZPoolStatusPool{
				"zroot":    cleanupPool("zroot", "/dev/nda0p5.eli"),
				"bootpool": cleanupPool("bootpool", "/dev/nda9p3"),
			},
			wantErr: true,
		},
		{
			name:  "plain install leaves unrelated bootpool alone",
			plain: true,
			pools: map[string]*gzfs.ZPoolStatusPool{
				"zroot":    cleanupPool("zroot", "/dev/nda0p4"),
				"bootpool": cleanupPool("bootpool", "/dev/nda9p3"),
			},
			want: []string{"zroot"},
		},
		{
			name:  "plain install removes selected old bootpool",
			plain: true,
			pools: map[string]*gzfs.ZPoolStatusPool{"bootpool": cleanupPool("bootpool", "/dev/nda0p3")},
			want:  []string{"bootpool"},
		},
		{
			name:    "plain install refuses mixed bootpool",
			plain:   true,
			pools:   map[string]*gzfs.ZPoolStatusPool{"bootpool": cleanupPool("bootpool", "/dev/nda0p3", "/dev/nda9p3")},
			wantErr: true,
		},
		{
			name:  "unrelated pool names are untouched",
			pools: map[string]*gzfs.ZPoolStatusPool{"tank": cleanupPool("tank", "/dev/nda9p3")},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			z := storage
			z.Encrypt = !c.plain
			pools := &testInstallPools{pools: c.pools}
			err := cleanupTargetPools(context.Background(), pools, z, resolvePoolProvider)
			if (err != nil) != c.wantErr {
				t.Fatalf("cleanup = %v, wantErr %t", err, c.wantErr)
			}
			if !slices.Equal(pools.destroyed, c.want) {
				t.Fatalf("destroyed %v, want %v", pools.destroyed, c.want)
			}
		})
	}
}

func TestCleanupFailsClosedOnInspectionOrDestroyErrors(t *testing.T) {
	storage := ZFSStorage{Encrypt: true, Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}}
	for _, failure := range []string{"list", "status", "identity changed", "destroy"} {
		t.Run(failure, func(t *testing.T) {
			pools := &testInstallPools{pools: map[string]*gzfs.ZPoolStatusPool{
				"zroot":    cleanupPool("zroot", "/dev/nda0p5.eli"),
				"bootpool": cleanupPool("bootpool", "/dev/nda0p3"),
			}}
			switch failure {
			case "list":
				pools.listErr = errors.New("cannot list pools")
			case "status":
				pools.statusErr = map[string]error{"bootpool": errors.New("cannot read status")}
			case "identity changed":
				pools.changeGUIDBeforeDelete = true
			case "destroy":
				pools.destroyErr = errors.New("cannot unmount")
			}
			if err := cleanupTargetPools(context.Background(), pools, storage, resolvePoolProvider); err == nil {
				t.Fatal("cleanup failure ignored")
			}
			want := []string(nil)
			if failure == "destroy" {
				want = []string{"zroot"}
			}
			if !slices.Equal(pools.destroyed, want) {
				t.Fatalf("destroy attempts = %v, want %v", pools.destroyed, want)
			}
		})
	}
}

func TestPoolScopeChecksAuxiliaryDevices(t *testing.T) {
	storage := ZFSStorage{Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}}
	for _, group := range []string{"logs", "spares", "cache", "special", "dedup"} {
		t.Run(group, func(t *testing.T) {
			pool := cleanupPool("zroot", "/dev/nda0p5")
			foreign := cleanupPool("aux", "/dev/nda9p1").Vdevs
			switch group {
			case "logs":
				pool.Logs = foreign
			case "spares":
				pool.Spares = foreign
			case "cache":
				pool.L2Cache = foreign
			case "special":
				pool.Special = foreign
			case "dedup":
				pool.Dedup = foreign
			}
			all, any, err := poolTargetScope(pool, storage, resolvePoolProvider)
			if err != nil || all || !any {
				t.Fatalf("scope = (%t, %t, %v), want mixed-device pool", all, any, err)
			}
		})
	}
}

func TestPoolScopeHandlesAliasesAndUnknownTopology(t *testing.T) {
	storage := ZFSStorage{Vdevs: []ZFSVdev{{Type: "stripe", Disks: []string{"nda0"}}}}
	resolve := func(path string) string {
		if path == "/dev/gpt/zfs0.eli" {
			return "nda0p5"
		}
		if strings.Contains(path, "/gptid/") {
			return ""
		}
		return resolvePoolProvider(path)
	}
	pool := cleanupPool("zroot", "/dev/gpt/zfs0.eli")
	all, any, err := poolTargetScope(pool, storage, resolve)
	if err != nil || !all || !any {
		t.Fatalf("resolved alias rejected: (%t, %t, %v)", all, any, err)
	}
	for _, pool := range []*gzfs.ZPoolStatusPool{
		nil,
		{Name: "zroot"},
		cleanupPool("zroot"),
		cleanupPool("zroot", "/dev/gptid/unknown"),
		cleanupPool("zroot", "123456789"),
		{Name: "zroot", PoolGUID: "id", Vdevs: map[string]*gzfs.ZPoolStatusVDEV{"missing": nil}},
		{Name: "zroot", PoolGUID: "id", Vdevs: map[string]*gzfs.ZPoolStatusVDEV{"file": {Name: "nda0", VdevType: "file"}}},
	} {
		if _, _, err := poolTargetScope(pool, storage, resolve); err == nil {
			t.Fatalf("unknown topology accepted: %+v", pool)
		}
	}
}
