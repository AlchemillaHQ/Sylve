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
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type fakeSystemService struct {
	systemServiceInterfaces.SystemServiceInterface
	pools []*gzfs.ZPool
	err   error
}

type jailTemplateGuestIdentityCheckerStub struct {
	batches [][]uint
	err     error
}

func (s *jailTemplateGuestIdentityCheckerStub) RequireGuestIDAvailable(ctx context.Context, guestID uint) error {
	return s.RequireGuestIDsAvailable(ctx, []uint{guestID})
}

func (s *jailTemplateGuestIdentityCheckerStub) RequireGuestIDsAvailable(_ context.Context, guestIDs []uint) error {
	s.batches = append(s.batches, append([]uint(nil), guestIDs...))
	return s.err
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
		"pool": strings.SplitN(name, "/", 2)[0],
		"type": string(gzfs.DatasetTypeFilesystem),
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

func TestSelectJailTemplateCPUSet(t *testing.T) {
	t.Run("persists a fresh CPU set that config generation accepts", func(t *testing.T) {
		dbConn := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
		svc := &Service{
			DB:             dbConn,
			hardwareOps:    &fakeJailHardwareOps{logicalCores: 4},
			ctidHashByCTID: make(map[uint]string),
		}
		enabled := true
		jails := []jailModels.Jail{
			{CTID: 401, Name: "j401", ResourceLimits: &enabled, Cores: 2, Memory: 2 * 1024 * 1024 * 1024},
			{CTID: 402, Name: "j402", ResourceLimits: &enabled, Cores: 2, Memory: 2 * 1024 * 1024 * 1024},
		}
		wantCPUSet := [][]int{{0, 1}, {2, 3}}

		for i := range jails {
			cpuSet, err := svc.selectJailTemplateCPUSet(jailModels.JailTemplate{
				ResourceLimits: jails[i].ResourceLimits,
				Cores:          jails[i].Cores,
			}, jails[i].CTID)
			if err != nil {
				t.Fatalf("select CPU set for jail %d: %v", jails[i].CTID, err)
			}
			if !reflect.DeepEqual(cpuSet, wantCPUSet[i]) {
				t.Fatalf("jail %d CPU set = %v, want %v", jails[i].CTID, cpuSet, wantCPUSet[i])
			}
			jails[i].CPUSet = cpuSet
			if err := dbConn.Create(&jails[i]).Error; err != nil {
				t.Fatalf("persist jail %d: %v", jails[i].CTID, err)
			}

			var reloaded jailModels.Jail
			if err := dbConn.Where("ct_id = ?", jails[i].CTID).First(&reloaded).Error; err != nil {
				t.Fatalf("reload jail %d: %v", jails[i].CTID, err)
			}
			if _, _, err := svc.CreateHardwareConfig(reloaded); err != nil {
				t.Fatalf("create hardware config for jail %d: %v", jails[i].CTID, err)
			}
		}
	})

	t.Run("disabled or zero-core limits need no allocation", func(t *testing.T) {
		disabled := false
		svc := &Service{hardwareOps: &fakeJailHardwareOps{logicalCores: 0}}
		for _, tt := range []struct {
			ctID     uint
			template jailModels.JailTemplate
		}{
			{ctID: 403, template: jailModels.JailTemplate{ResourceLimits: &disabled, Cores: 2}},
			{ctID: 404, template: jailModels.JailTemplate{Cores: 0}},
		} {
			cpuSet, err := svc.selectJailTemplateCPUSet(tt.template, tt.ctID)
			if err != nil {
				t.Fatalf("unexpected allocation error for jail %d: %v", tt.ctID, err)
			}
			if len(cpuSet) != 0 {
				t.Fatalf("jail %d CPU set = %v, want empty", tt.ctID, cpuSet)
			}
		}
	})

	t.Run("rejects an impossible core count", func(t *testing.T) {
		dbConn := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
		enabled := true
		template := jailModels.JailTemplate{ResourceLimits: &enabled, Cores: 5}
		svc := &Service{DB: dbConn, hardwareOps: &fakeJailHardwareOps{logicalCores: 4}}
		if _, err := svc.selectJailTemplateCPUSet(template, 405); err == nil || !strings.Contains(err.Error(), "invalid_cores") {
			t.Fatalf("expected invalid_cores, got %v", err)
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

func TestPreflightTemplateTargetsUsesLiveGuestIDBatch(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&jailModels.Jail{},
		&vmModels.VM{},
	)
	checker := &jailTemplateGuestIdentityCheckerStub{err: fmt.Errorf("guest_id_already_in_use")}
	svc := &Service{DB: dbConn}
	svc.SetGuestIdentityAvailabilityChecker(checker)
	err := svc.preflightTemplateTargets(t.Context(), jailModels.JailTemplate{}, []createTarget{
		{CTID: 350, Name: "j350", Pool: "zroot"},
		{CTID: 351, Name: "j351", Pool: "zroot"},
	})

	if err == nil || !strings.Contains(err.Error(), "guest_id_already_in_use") {
		t.Fatalf("expected guest_id_already_in_use, got %v", err)
	}
	if len(checker.batches) != 1 || !reflect.DeepEqual(checker.batches[0], []uint{350, 351}) {
		t.Fatalf("identity batches = %v, want one [350 351] batch", checker.batches)
	}
}

func TestCreateJailsFromTemplateRechecksGuestIDsBeforeStorageWork(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&jailModels.JailTemplate{},
		&jailModels.Jail{},
		&vmModels.VM{},
	)
	template := jailModels.JailTemplate{
		Name:           "Template 360",
		SourceJailName: "source-360",
		Pool:           "zroot",
		RootDataset:    "zroot/sylve/jails/templates/template-360",
		Type:           jailModels.JailTypeFreeBSD,
	}
	if err := dbConn.Create(&template).Error; err != nil {
		t.Fatalf("failed to seed jail template: %v", err)
	}

	checker := &jailTemplateGuestIdentityCheckerStub{err: fmt.Errorf("guest_id_already_in_use")}
	svc := newTemplateTestService(t, dbConn, nil, "zroot")
	svc.SetGuestIdentityAvailabilityChecker(checker)

	err := svc.CreateJailsFromTemplate(t.Context(), template.ID, CreateFromTemplateRequest{
		Mode: "single",
		CTID: 360,
		Name: "j360",
		Pool: "zroot",
	})
	if err == nil || !strings.Contains(err.Error(), "guest_id_already_in_use") {
		t.Fatalf("expected execution preflight failure, got %v", err)
	}
	if len(checker.batches) != 1 || !reflect.DeepEqual(checker.batches[0], []uint{360}) {
		t.Fatalf("identity batches = %v, want one [360] execution batch", checker.batches)
	}
}

func TestPreflightConvertJailToTemplateInsufficientPoolSpace(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&jailModels.JailTemplate{},
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&taskModels.GuestLifecycleTask{},
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
	svc.liveStateByCTID = map[uint]jailServiceInterfaces.State{
		106: {CTID: 106, State: "INACTIVE"},
	}
	err := svc.PreflightConvertJailToTemplate(context.Background(), 106, ConvertToTemplateRequest{Name: "tmpl-106"})
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
		)

		tpl := jailModels.JailTemplate{
			Name:           "Template 106",
			SourceJailName: "source-106",
			Pool:           "zroot",
			RootDataset:    "zroot/sylve/jails/templates/template-106",
			Type:           jailModels.JailTypeFreeBSD,
		}
		if err := dbConn.Create(&tpl).Error; err != nil {
			t.Fatalf("failed to create template: %v", err)
		}

		runner := &fakeGZFSRunner{
			datasets: map[string]fakeDatasetInfo{
				"zroot/sylve/jails/templates/template-106": {Used: 80, Referenced: 80},
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

func TestGetJailTemplateValidationAndNotFound(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.JailTemplate{})
	svc := &Service{DB: dbConn}

	if _, err := svc.GetJailTemplate(0); err == nil || !strings.Contains(err.Error(), "invalid_template_id") {
		t.Fatalf("expected invalid_template_id, got %v", err)
	}

	if _, err := svc.GetJailTemplate(999); err == nil || !strings.Contains(err.Error(), "template_not_found") {
		t.Fatalf("expected template_not_found, got %v", err)
	}
}

func TestDeleteJailTemplateValidationAndNotFound(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.JailTemplate{})
	svc := &Service{DB: dbConn}

	if err := svc.DeleteJailTemplate(context.Background(), 0); err == nil || !strings.Contains(err.Error(), "invalid_template_id") {
		t.Fatalf("expected invalid_template_id, got %v", err)
	}

	if err := svc.DeleteJailTemplate(context.Background(), 999); err == nil || !strings.Contains(err.Error(), "template_not_found") {
		t.Fatalf("expected template_not_found, got %v", err)
	}
}

func TestPreflightCreateJailsFromTemplateTemplateValidation(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.JailTemplate{})
	svc := &Service{DB: dbConn}

	if err := svc.PreflightCreateJailsFromTemplate(context.Background(), 0, CreateFromTemplateRequest{Mode: "single", CTID: 100}); err == nil || !strings.Contains(err.Error(), "invalid_template_id") {
		t.Fatalf("expected invalid_template_id, got %v", err)
	}

	if err := svc.PreflightCreateJailsFromTemplate(context.Background(), 777, CreateFromTemplateRequest{Mode: "single", CTID: 100}); err == nil || !strings.Contains(err.Error(), "template_not_found") {
		t.Fatalf("expected template_not_found, got %v", err)
	}
}

func TestEnsureUniqueJailTemplateName(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.JailTemplate{})
	svc := &Service{DB: dbConn}

	if err := svc.ensureUniqueJailTemplateName(""); err == nil || !strings.Contains(err.Error(), "template_name_required") {
		t.Fatalf("expected template_name_required, got %v", err)
	}

	if err := dbConn.Create(&jailModels.JailTemplate{
		Name:        "Base Template",
		Pool:        "zroot",
		RootDataset: "zroot/sylve/jails/templates/base-template-1",
		Type:        jailModels.JailTypeFreeBSD,
	}).Error; err != nil {
		t.Fatalf("failed to seed template: %v", err)
	}

	if err := svc.ensureUniqueJailTemplateName("base template"); err == nil || !strings.Contains(err.Error(), "template_name_already_in_use") {
		t.Fatalf("expected template_name_already_in_use, got %v", err)
	}
}

func TestValidateJailTemplateDatasetPath(t *testing.T) {
	tests := []struct {
		name    string
		pool    string
		dataset string
		valid   bool
	}{
		{name: "template child", pool: "zroot", dataset: "zroot/sylve/jails/templates/base-1", valid: true},
		{name: "shared parent", pool: "zroot", dataset: "zroot/sylve/jails/templates", valid: false},
		{name: "jail dataset", pool: "zroot", dataset: "zroot/sylve/jails/101", valid: false},
		{name: "different pool", pool: "zroot", dataset: "tank/sylve/jails/templates/base-1", valid: false},
		{name: "invalid pool", pool: "zroot/child", dataset: "zroot/child/sylve/jails/templates/base-1", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJailTemplateDatasetPath(tt.pool, tt.dataset)
			if tt.valid && err != nil {
				t.Fatalf("expected valid path, got %v", err)
			}
			if !tt.valid && err == nil {
				t.Fatal("expected invalid_template_dataset_path")
			}
		})
	}
}

func TestRunJailTemplateCreatePlanRollsBackInReverseWithCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	created := make([]uint, 0)
	cleaned := make([]uint, 0)

	err := runJailTemplateCreatePlan(
		ctx,
		[]createTarget{{CTID: 101}, {CTID: 102}, {CTID: 103}},
		func(_ context.Context, target createTarget) error {
			if target.CTID == 103 {
				cancel()
				return fmt.Errorf("target_create_failed")
			}
			created = append(created, target.CTID)
			return nil
		},
		func(cleanupCtx context.Context, target createTarget) error {
			if cleanupCtx.Err() != nil {
				return fmt.Errorf("cleanup_context_cancelled")
			}
			cleaned = append(cleaned, target.CTID)
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "target_create_failed") {
		t.Fatalf("expected target_create_failed, got %v", err)
	}
	if !reflect.DeepEqual(created, []uint{101, 102}) {
		t.Fatalf("created targets = %v, want [101 102]", created)
	}
	if !reflect.DeepEqual(cleaned, []uint{102, 101}) {
		t.Fatalf("cleanup order = %v, want [102 101]", cleaned)
	}
}

func newJailTemplateCapturePreflightService(t *testing.T, state string) (*Service, *gorm.DB) {
	t.Helper()

	dbConn := testutil.NewSQLiteTestDB(t,
		&jailModels.JailTemplate{},
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&taskModels.GuestLifecycleTask{},
	)
	j := jailModels.Jail{CTID: 106, Name: "j106", Type: jailModels.JailTypeFreeBSD}
	if err := dbConn.Create(&j).Error; err != nil {
		t.Fatalf("failed to seed jail: %v", err)
	}

	svc := &Service{
		DB: dbConn,
		liveStateByCTID: map[uint]jailServiceInterfaces.State{
			106: {CTID: 106, State: state},
		},
	}
	return svc, dbConn
}

func TestPreflightConvertJailToTemplateRequiresStoppedSource(t *testing.T) {
	svc, _ := newJailTemplateCapturePreflightService(t, "ACTIVE")
	err := svc.PreflightConvertJailToTemplate(t.Context(), 106, ConvertToTemplateRequest{Name: "tmpl-106"})
	if err == nil || !strings.Contains(err.Error(), "jail_must_be_stopped") {
		t.Fatalf("expected jail_must_be_stopped, got %v", err)
	}
}

func TestPreflightConvertJailToTemplateRejectsActiveJailTask(t *testing.T) {
	svc, dbConn := newJailTemplateCapturePreflightService(t, "INACTIVE")
	if err := dbConn.Create(&taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeJail,
		GuestID:   106,
		Action:    "start",
		Status:    taskModels.LifecycleTaskStatusQueued,
	}).Error; err != nil {
		t.Fatalf("failed to seed lifecycle task: %v", err)
	}

	err := svc.PreflightConvertJailToTemplate(t.Context(), 106, ConvertToTemplateRequest{Name: "tmpl-106"})
	if err == nil || !strings.Contains(err.Error(), "jail_has_active_lifecycle_task") {
		t.Fatalf("expected jail_has_active_lifecycle_task, got %v", err)
	}
}

func TestDeleteJailTemplateRejectsActiveCreation(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &jailModels.JailTemplate{}, &taskModels.GuestLifecycleTask{})
	template := jailModels.JailTemplate{
		Name:        "Template 7",
		Pool:        "zroot",
		RootDataset: "zroot/sylve/jails/templates/template-7",
		Type:        jailModels.JailTypeFreeBSD,
	}
	if err := dbConn.Create(&template).Error; err != nil {
		t.Fatalf("failed to seed template: %v", err)
	}
	if err := dbConn.Create(&taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeJailTemplate,
		GuestID:   template.ID,
		Action:    "create",
		Status:    taskModels.LifecycleTaskStatusRunning,
	}).Error; err != nil {
		t.Fatalf("failed to seed lifecycle task: %v", err)
	}

	svc := &Service{DB: dbConn}
	err := svc.DeleteJailTemplate(t.Context(), template.ID)
	if err == nil || !strings.Contains(err.Error(), "jail_template_in_use") {
		t.Fatalf("expected jail_template_in_use, got %v", err)
	}
}

func TestValidateJailTemplateNetworksRejectsLinuxAutomaticConfiguration(t *testing.T) {
	svc := &Service{}
	for _, network := range []jailModels.JailTemplateNetwork{
		{DHCP: true},
		{SLAAC: true},
	} {
		err := svc.validateJailTemplateNetworks(jailModels.JailTypeLinux, []jailModels.JailTemplateNetwork{network})
		if err == nil || !strings.Contains(err.Error(), "cannot_set_dhcp_or_slaac_when_linux_jail") {
			t.Fatalf("expected Linux automatic configuration to be rejected, got %v", err)
		}
	}
}

func TestValidateJailTemplateNetworksAllowsLinuxStaticConfiguration(t *testing.T) {
	svc := &Service{}
	err := svc.validateJailTemplateNetworks(jailModels.JailTypeLinux, []jailModels.JailTemplateNetwork{
		{DefaultGateway: true},
		{},
	})
	if err != nil {
		t.Fatalf("expected Linux static-only networks to be accepted, got %v", err)
	}
}

func TestValidateJailTemplateNetworksRejectsMultipleDefaultGateways(t *testing.T) {
	svc := &Service{}
	err := svc.validateJailTemplateNetworks(jailModels.JailTypeFreeBSD, []jailModels.JailTemplateNetwork{
		{DHCP: true, DefaultGateway: true},
		{DHCP: true, DefaultGateway: true},
	})
	if err == nil || !strings.Contains(err.Error(), "jail_default_gateway_exists") {
		t.Fatalf("expected multiple default gateways to be rejected, got %v", err)
	}
}

func TestValidateJailTemplateNetworksAllowsMultipleDHCPInterfaces(t *testing.T) {
	svc := &Service{}
	err := svc.validateJailTemplateNetworks(jailModels.JailTypeFreeBSD, []jailModels.JailTemplateNetwork{
		{DHCP: true, DefaultGateway: true},
		{DHCP: true},
	})
	if err != nil {
		t.Fatalf("expected multiple DHCP interfaces with one default gateway to be accepted, got %v", err)
	}
}
