// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type fakeSystemService struct {
	systemServiceInterfaces.SystemServiceInterface
	pools []*gzfs.ZPool
	err   error
}

func (f fakeSystemService) GetUsablePools(_ context.Context) ([]*gzfs.ZPool, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.pools, nil
}

type fakeDatasetInfo struct {
	Used       uint64
	Referenced uint64
}

type fakeGZFSRunner struct {
	datasets map[string]fakeDatasetInfo
	pools    map[string]uint64
}

func (r *fakeGZFSRunner) Run(_ context.Context, _ io.Reader, stdout, _ io.Writer, name string, args ...string) error {
	switch name {
	case "zfs":
		return r.runZFS(stdout, args)
	case "zpool":
		return r.runZpool(stdout, args)
	default:
		return fmt.Errorf("unsupported command: %s", name)
	}
}

func (r *fakeGZFSRunner) runZFS(stdout io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("unsupported zfs args: %v", args)
	}

	target := parseTargetArg(args, map[string]int{
		"-o": 1,
		"-t": 1,
	})

	datasets := map[string]any{}
	if target == "" {
		for name, ds := range r.datasets {
			datasets[name] = fakeDatasetJSON(name, ds)
		}
	} else if ds, ok := r.datasets[target]; ok {
		datasets[target] = fakeDatasetJSON(target, ds)
	}

	resp := map[string]any{
		"output_version": map[string]any{
			"command":    "zfs",
			"vers_major": 0,
			"vers_minor": 0,
		},
		"datasets": datasets,
	}

	return json.NewEncoder(stdout).Encode(resp)
}

func (r *fakeGZFSRunner) runZpool(stdout io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("unsupported zpool args: %v", args)
	}

	target := parseTargetArg(args, map[string]int{
		"-o": 1,
	})

	pools := map[string]any{}
	if target == "" {
		for name, free := range r.pools {
			pools[name] = fakePoolJSON(name, free)
		}
	} else if free, ok := r.pools[target]; ok {
		pools[target] = fakePoolJSON(target, free)
	}

	resp := map[string]any{
		"output_version": map[string]any{
			"command":    "zpool",
			"vers_major": 0,
			"vers_minor": 0,
		},
		"pools": pools,
	}

	return json.NewEncoder(stdout).Encode(resp)
}

func parseTargetArg(args []string, flagsWithValues map[string]int) string {
	target := ""
	skip := 0

	for i, arg := range args {
		if i == 0 {
			continue
		}
		if skip > 0 {
			skip--
			continue
		}
		if n, ok := flagsWithValues[arg]; ok {
			skip = n
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		target = arg
	}

	return target
}

func fakeDatasetJSON(name string, ds fakeDatasetInfo) map[string]any {
	return map[string]any{
		"name": name,
		"properties": map[string]any{
			"guid": map[string]any{
				"value":  "1",
				"source": map[string]any{"type": "default", "data": ""},
			},
			"mountpoint": map[string]any{
				"value":  "/" + name,
				"source": map[string]any{"type": "default", "data": ""},
			},
			"used": map[string]any{
				"value":  strconv.FormatUint(ds.Used, 10),
				"source": map[string]any{"type": "default", "data": ""},
			},
			"referenced": map[string]any{
				"value":  strconv.FormatUint(ds.Referenced, 10),
				"source": map[string]any{"type": "default", "data": ""},
			},
			"compressratio": map[string]any{
				"value":  "1.00x",
				"source": map[string]any{"type": "default", "data": ""},
			},
		},
	}
}

func fakePoolJSON(name string, free uint64) map[string]any {
	return map[string]any{
		"name": name,
		"properties": map[string]any{
			"free": map[string]any{
				"value":  strconv.FormatUint(free, 10),
				"source": map[string]any{"type": "default", "data": ""},
			},
			"size": map[string]any{
				"value":  strconv.FormatUint(free*2, 10),
				"source": map[string]any{"type": "default", "data": ""},
			},
			"allocated": map[string]any{
				"value":  strconv.FormatUint(free, 10),
				"source": map[string]any{"type": "default", "data": ""},
			},
		},
	}
}

func newTemplateTestService(t *testing.T, db *gorm.DB, runner gzfs.Runner, poolNames ...string) *Service {
	t.Helper()

	usablePools := make([]*gzfs.ZPool, 0, len(poolNames))
	for _, name := range poolNames {
		usablePools = append(usablePools, &gzfs.ZPool{Name: name})
	}

	var client *gzfs.Client
	if runner != nil {
		client = gzfs.NewClient(gzfs.Options{
			Runner:   runner,
			ZFSBin:   "zfs",
			ZpoolBin: "zpool",
			ZDBBin:   "zdb",
		})
	}

	return &Service{
		DB:     db,
		System: fakeSystemService{pools: usablePools},
		GZFS:   client,
	}
}

func TestBuildCreateTargetsValidationAndPoolSelection(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t)
	svc := newTemplateTestService(t, dbConn, nil, "zroot", "tank")

	template := jailModels.JailTemplate{
		Name:           "Base Template",
		Pool:           "zroot",
		SourceJailName: "basejail",
	}

	t.Run("invalid single ctid", func(t *testing.T) {
		_, err := svc.buildCreateTargets(context.Background(), template, CreateFromTemplateRequest{
			Mode: "single",
			CTID: 10000,
			Pool: "zroot",
		})
		if err == nil || !strings.Contains(err.Error(), "invalid_ctid") {
			t.Fatalf("expected invalid_ctid, got %v", err)
		}
	})

	t.Run("invalid single name", func(t *testing.T) {
		_, err := svc.buildCreateTargets(context.Background(), template, CreateFromTemplateRequest{
			Mode: "single",
			CTID: 105,
			Name: "bad name",
			Pool: "zroot",
		})
		if err == nil || !strings.Contains(err.Error(), "invalid_jail_name") {
			t.Fatalf("expected invalid_jail_name, got %v", err)
		}
	})

	t.Run("invalid multiple ctid range", func(t *testing.T) {
		_, err := svc.buildCreateTargets(context.Background(), template, CreateFromTemplateRequest{
			Mode:      "multiple",
			StartCTID: 9999,
			Count:     2,
			Pool:      "zroot",
		})
		if err == nil || !strings.Contains(err.Error(), "invalid_ctid_range") {
			t.Fatalf("expected invalid_ctid_range, got %v", err)
		}
	})

	t.Run("invalid multiple prefix", func(t *testing.T) {
		_, err := svc.buildCreateTargets(context.Background(), template, CreateFromTemplateRequest{
			Mode:       "multiple",
			StartCTID:  200,
			Count:      2,
			NamePrefix: "prefix-too-long-16",
			Pool:       "zroot",
		})
		if err == nil || !strings.Contains(err.Error(), "invalid_name_prefix") {
			t.Fatalf("expected invalid_name_prefix, got %v", err)
		}
	})

	t.Run("pool override is applied", func(t *testing.T) {
		targets, err := svc.buildCreateTargets(context.Background(), template, CreateFromTemplateRequest{
			Mode: "single",
			CTID: 300,
			Name: "j300",
			Pool: "tank",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(targets))
		}
		if targets[0].Pool != "tank" {
			t.Fatalf("expected pool override tank, got %q", targets[0].Pool)
		}
	})

	t.Run("defaults to template pool", func(t *testing.T) {
		targets, err := svc.buildCreateTargets(context.Background(), template, CreateFromTemplateRequest{
			Mode: "single",
			CTID: 301,
			Name: "j301",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("expected 1 target, got %d", len(targets))
		}
		if targets[0].Pool != "zroot" {
			t.Fatalf("expected template pool zroot, got %q", targets[0].Pool)
		}
	})
}

func TestPreflightTemplateTargetsRejectsVMRIDCollision(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &vmModels.VM{})

	if err := dbConn.Create(&vmModels.VM{RID: 240, Name: "vm-240"}).Error; err != nil {
		t.Fatalf("failed to create vm: %v", err)
	}

	svc := &Service{DB: dbConn}
	err := svc.preflightTemplateTargets(context.Background(), jailModels.JailTemplate{}, []createTarget{
		{CTID: 240, Name: "j240", Pool: "zroot"},
	})

	if err == nil || !strings.Contains(err.Error(), "ctid_range_contains_used_values") {
		t.Fatalf("expected ctid_range_contains_used_values, got %v", err)
	}
}

func TestPreflightTemplateTargetsRejectsClusterGuestIDCollision(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&jailModels.Jail{},
		&vmModels.VM{},
		&clusterModels.Cluster{},
		&clusterModels.ClusterNode{},
	)

	if err := dbConn.Create(&clusterModels.Cluster{Enabled: true}).Error; err != nil {
		t.Fatalf("failed to create cluster row: %v", err)
	}
	if err := dbConn.Create(&clusterModels.ClusterNode{
		NodeUUID: "node-1",
		GuestIDs: []uint{350},
	}).Error; err != nil {
		t.Fatalf("failed to create cluster node: %v", err)
	}

	svc := &Service{DB: dbConn}
	err := svc.preflightTemplateTargets(context.Background(), jailModels.JailTemplate{}, []createTarget{
		{CTID: 350, Name: "j350", Pool: "zroot"},
	})

	if err == nil || !strings.Contains(err.Error(), "ctid_range_contains_used_values") {
		t.Fatalf("expected ctid_range_contains_used_values, got %v", err)
	}
}

func TestPreflightConvertJailToTemplateInsufficientPoolSpace(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	j := jailModels.Jail{CTID: 106, Name: "j106", Type: jailModels.JailTypeFreeBSD}
	if err := dbConn.Create(&j).Error; err != nil {
		t.Fatalf("failed to create jail: %v", err)
	}
	if err := dbConn.Create(&jailModels.Storage{
		JailID: j.ID,
		Pool:   "zroot",
		GUID:   "guid-j106",
		Name:   "Base Filesystem",
		IsBase: true,
	}).Error; err != nil {
		t.Fatalf("failed to create jail storage: %v", err)
	}

	runner := &fakeGZFSRunner{
		datasets: map[string]fakeDatasetInfo{
			"zroot/sylve/jails/106": {Used: 200, Referenced: 200},
		},
		pools: map[string]uint64{
			"zroot": 100,
		},
	}

	svc := newTemplateTestService(t, dbConn, runner, "zroot")
	err := svc.PreflightConvertJailToTemplate(context.Background(), 106)
	if err == nil || !strings.Contains(err.Error(), "insufficient_pool_space") {
		t.Fatalf("expected insufficient_pool_space, got %v", err)
	}
}

func TestPreflightCreateFromTemplateInsufficientPoolSpaceSingleAndMultiple(t *testing.T) {
	newServiceAndTemplate := func(t *testing.T, free uint64) (*Service, uint) {
		t.Helper()

		dbConn := testutil.NewSQLiteTestDB(t,
			&jailModels.JailTemplate{},
			&jailModels.Jail{},
			&vmModels.VM{},
			&clusterModels.Cluster{},
			&clusterModels.ClusterNode{},
		)

		tpl := jailModels.JailTemplate{
			Name:           "Template 106",
			SourceCTID:     106,
			SourceJailName: "source-106",
			Pool:           "zroot",
			RootDataset:    "zroot/sylve/jails/clones/106",
			Type:           jailModels.JailTypeFreeBSD,
		}
		if err := dbConn.Create(&tpl).Error; err != nil {
			t.Fatalf("failed to create template: %v", err)
		}

		runner := &fakeGZFSRunner{
			datasets: map[string]fakeDatasetInfo{
				"zroot/sylve/jails/clones/106": {Used: 80, Referenced: 80},
			},
			pools: map[string]uint64{
				"zroot": free,
			},
		}

		return newTemplateTestService(t, dbConn, runner, "zroot"), tpl.ID
	}

	t.Run("single mode", func(t *testing.T) {
		svc, templateID := newServiceAndTemplate(t, 50)
		err := svc.PreflightCreateJailsFromTemplate(context.Background(), templateID, CreateFromTemplateRequest{
			Mode: "single",
			CTID: 501,
			Name: "j501",
			Pool: "zroot",
		})
		if err == nil || !strings.Contains(err.Error(), "insufficient_pool_space") {
			t.Fatalf("expected insufficient_pool_space, got %v", err)
		}
	})

	t.Run("multiple mode", func(t *testing.T) {
		svc, templateID := newServiceAndTemplate(t, 150)
		err := svc.PreflightCreateJailsFromTemplate(context.Background(), templateID, CreateFromTemplateRequest{
			Mode:       "multiple",
			StartCTID:  600,
			Count:      2,
			NamePrefix: "j",
			Pool:       "zroot",
		})
		if err == nil || !strings.Contains(err.Error(), "insufficient_pool_space") {
			t.Fatalf("expected insufficient_pool_space, got %v", err)
		}
	})
}
