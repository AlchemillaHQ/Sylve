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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type jailCreateTestSystemService struct {
	systemServiceInterfaces.SystemServiceInterface
	pools []*gzfs.ZPool
	err   error
}

type jailCreateGuestIdentityCheckerStub struct {
	guestIDs []uint
	err      error
}

type blockingJailCreateGuestIdentityChecker struct {
	mu            sync.Mutex
	calls         int
	firstEntered  chan struct{}
	secondEntered chan struct{}
	releaseFirst  chan struct{}
}

func newBlockingJailCreateGuestIdentityChecker() *blockingJailCreateGuestIdentityChecker {
	return &blockingJailCreateGuestIdentityChecker{
		firstEntered:  make(chan struct{}),
		secondEntered: make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
}

func (s *blockingJailCreateGuestIdentityChecker) RequireGuestIDAvailable(ctx context.Context, guestID uint) error {
	return s.RequireGuestIDsAvailable(ctx, []uint{guestID})
}

func (s *blockingJailCreateGuestIdentityChecker) RequireGuestIDsAvailable(_ context.Context, _ []uint) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()

	switch call {
	case 1:
		close(s.firstEntered)
		<-s.releaseFirst
	case 2:
		close(s.secondEntered)
	}
	return nil
}

func (s *blockingJailCreateGuestIdentityChecker) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *jailCreateGuestIdentityCheckerStub) RequireGuestIDAvailable(ctx context.Context, guestID uint) error {
	return s.RequireGuestIDsAvailable(ctx, []uint{guestID})
}

func (s *jailCreateGuestIdentityCheckerStub) RequireGuestIDsAvailable(_ context.Context, guestIDs []uint) error {
	s.guestIDs = append(s.guestIDs, guestIDs...)
	return s.err
}

func (f jailCreateTestSystemService) GetUsablePools(_ context.Context) ([]*gzfs.ZPool, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.pools, nil
}

type jailCreateTestZFSDataset struct {
	guid       string
	mountpoint string
}

type jailCreateTestZFSRunner struct {
	mu           sync.Mutex
	datasets     map[string]jailCreateTestZFSDataset
	createCalls  int
	destroyCalls int
}

func newJailCreateTestZFSRunner(existingDatasets []string) *jailCreateTestZFSRunner {
	datasets := make(map[string]jailCreateTestZFSDataset, len(existingDatasets))
	for i, datasetName := range existingDatasets {
		datasets[datasetName] = jailCreateTestZFSDataset{
			guid:       strconv.Itoa(i + 1),
			mountpoint: "/" + strings.TrimPrefix(datasetName, "/"),
		}
	}

	return &jailCreateTestZFSRunner{
		datasets: datasets,
	}
}

func (r *jailCreateTestZFSRunner) getCreateCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.createCalls
}

func (r *jailCreateTestZFSRunner) hasDataset(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.datasets[name]
	return ok
}

func (r *jailCreateTestZFSRunner) Run(_ context.Context, _ io.Reader, stdout, _ io.Writer, name string, args ...string) error {
	if name != "zfs" {
		return fmt.Errorf("unsupported command: %s", name)
	}
	if len(args) == 0 {
		return fmt.Errorf("missing zfs args")
	}

	switch args[0] {
	case "list":
		return r.runList(stdout, args)
	case "create":
		return r.runCreate(args)
	case "destroy":
		return r.runDestroy(args)
	default:
		return fmt.Errorf("unsupported zfs subcommand: %s", args[0])
	}
}

func (r *jailCreateTestZFSRunner) runList(stdout io.Writer, args []string) error {
	target := parseJailCreateZFSTargetArg(args)

	r.mu.Lock()
	defer r.mu.Unlock()

	datasets := map[string]any{}
	for datasetName, dataset := range r.datasets {
		if target != "" && datasetName != target {
			continue
		}

		datasets[datasetName] = map[string]any{
			"name": datasetName,
			"pool": jailCreateDatasetPoolName(datasetName),
			"properties": map[string]any{
				"guid": map[string]any{
					"value":  dataset.guid,
					"source": map[string]any{"type": "default", "data": ""},
				},
				"mountpoint": map[string]any{
					"value":  dataset.mountpoint,
					"source": map[string]any{"type": "default", "data": ""},
				},
				"type": map[string]any{
					"value":  "filesystem",
					"source": map[string]any{"type": "default", "data": ""},
				},
				"used": map[string]any{
					"value":  "0",
					"source": map[string]any{"type": "default", "data": ""},
				},
				"available": map[string]any{
					"value":  "0",
					"source": map[string]any{"type": "default", "data": ""},
				},
				"referenced": map[string]any{
					"value":  "0",
					"source": map[string]any{"type": "default", "data": ""},
				},
				"compressratio": map[string]any{
					"value":  "1.00x",
					"source": map[string]any{"type": "default", "data": ""},
				},
			},
		}
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

func (r *jailCreateTestZFSRunner) runCreate(args []string) error {
	datasetName := parseJailCreateZFSLastNonFlagArg(args[1:])
	if datasetName == "" {
		return fmt.Errorf("dataset name missing in create args: %v", args)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.createCalls++
	if _, exists := r.datasets[datasetName]; !exists {
		r.datasets[datasetName] = jailCreateTestZFSDataset{
			guid:       strconv.Itoa(len(r.datasets) + 1),
			mountpoint: "/" + strings.TrimPrefix(datasetName, "/"),
		}
	}

	return nil
}

func (r *jailCreateTestZFSRunner) runDestroy(args []string) error {
	datasetName := parseJailCreateZFSLastNonFlagArg(args[1:])
	if datasetName == "" {
		return fmt.Errorf("dataset name missing in destroy args: %v", args)
	}

	recursive := false
	for _, arg := range args {
		if arg == "-r" {
			recursive = true
			break
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.destroyCalls++
	delete(r.datasets, datasetName)
	if recursive {
		prefix := datasetName + "/"
		for name := range r.datasets {
			if strings.HasPrefix(name, prefix) {
				delete(r.datasets, name)
			}
		}
	}

	return nil
}

func parseJailCreateZFSTargetArg(args []string) string {
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

		switch arg {
		case "-o", "-t":
			skip = 1
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}

		target = arg
	}

	return target
}

func parseJailCreateZFSLastNonFlagArg(args []string) string {
	last := ""
	skip := 0

	for _, arg := range args {
		if skip > 0 {
			skip--
			continue
		}
		switch arg {
		case "-o", "-t":
			skip = 1
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		last = arg
	}

	return last
}

func jailCreateDatasetPoolName(datasetName string) string {
	trimmed := strings.TrimPrefix(datasetName, "/")
	if trimmed == "" {
		return ""
	}

	parts := strings.SplitN(trimmed, "/", 2)
	return parts[0]
}

func newJailCreateTestService(db *gorm.DB, runner *jailCreateTestZFSRunner, pools ...string) *Service {
	usablePools := make([]*gzfs.ZPool, 0, len(pools))
	for _, poolName := range pools {
		usablePools = append(usablePools, &gzfs.ZPool{Name: poolName})
	}

	return &Service{
		DB:     db,
		System: jailCreateTestSystemService{pools: usablePools},
		GZFS: gzfs.NewClient(gzfs.Options{
			Runner:   runner,
			ZFSBin:   "zfs",
			ZpoolBin: "zpool",
			ZDBBin:   "zdb",
		}),
		ctidHashByCTID: make(map[uint]string),
	}
}

func seedSwitch(t *testing.T, db *gorm.DB, name string) {
	t.Helper()
	if err := db.Create(&networkModels.StandardSwitch{Name: name}).Error; err != nil {
		t.Fatalf("failed to seed switch %q: %v", name, err)
	}
}

func seedBaseDownload(t *testing.T, db *gorm.DB, uuid string, extractedPath string) {
	t.Helper()

	record := utilitiesModels.Downloads{
		UUID:          uuid,
		Path:          filepath.Join(extractedPath, ".source"),
		Name:          filepath.Base(extractedPath),
		Type:          utilitiesModels.DownloadTypePath,
		URL:           "https://example.invalid/" + uuid,
		Progress:      100,
		Size:          1,
		UType:         utilitiesModels.DownloadUTypeBase,
		ExtractedPath: extractedPath,
		Status:        utilitiesModels.DownloadStatusDone,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("failed to seed base download row: %v", err)
	}
}

func jailCreateRequest(ctid uint, pool string, baseUUID string) jailServiceInterfaces.CreateJailRequest {
	resourceLimits := false

	return jailServiceInterfaces.CreateJailRequest{
		Name:           fmt.Sprintf("jail-%d", ctid),
		CTID:           &ctid,
		Pool:           pool,
		Base:           baseUUID,
		SwitchName:     "none",
		Type:           jailModels.JailTypeFreeBSD,
		ResourceLimits: &resourceLimits,
	}
}

func assertModelCount(t *testing.T, db *gorm.DB, model any, want int64, query string, args ...any) {
	t.Helper()

	var count int64
	q := db.Model(model)
	if strings.TrimSpace(query) != "" {
		q = q.Where(query, args...)
	}

	if err := q.Count(&count).Error; err != nil {
		t.Fatalf("failed to count model rows: %v", err)
	}
	if count != want {
		t.Fatalf("expected %d rows, found %d for query %q", want, count, query)
	}
}

func TestValidateCreate_FailsWhenCTIDAlreadyExists(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&utilitiesModels.Downloads{},
	)

	runner := newJailCreateTestZFSRunner(nil)
	svc := newJailCreateTestService(db, runner, "tank")

	baseDir := filepath.Join(t.TempDir(), "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create base directory: %v", err)
	}
	seedBaseDownload(t, db, "base-existing-ctid", baseDir)

	const ctid uint = 700
	if err := db.Create(&jailModels.Jail{
		Name: "existing-jail-700",
		CTID: ctid,
		Type: jailModels.JailTypeFreeBSD,
	}).Error; err != nil {
		t.Fatalf("failed to seed existing jail row: %v", err)
	}

	req := jailCreateRequest(ctid, "tank", "base-existing-ctid")
	err := svc.ValidateCreate(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "jail_with_ctid_already_exists") {
		t.Fatalf("expected jail_with_ctid_already_exists error, got %v", err)
	}
}

func TestValidateCreate_FailsWhenStaleCTIDDatasetExists(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&utilitiesModels.Downloads{},
	)

	const ctid uint = 701
	staleDataset := fmt.Sprintf("tank/sylve/jails/%d", ctid)
	runner := newJailCreateTestZFSRunner([]string{staleDataset})
	svc := newJailCreateTestService(db, runner, "tank")

	baseDir := filepath.Join(t.TempDir(), "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create base directory: %v", err)
	}
	seedBaseDownload(t, db, "base-stale-dataset", baseDir)

	req := jailCreateRequest(ctid, "tank", "base-stale-dataset")
	err := svc.ValidateCreate(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "jail_create_stale_artifacts_detected") {
		t.Fatalf("expected stale artifact error, got %v", err)
	}
}

func TestCreateJailStopsBeforeProvisioningWhenGuestIDCheckFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&utilitiesModels.Downloads{},
	)
	runner := newJailCreateTestZFSRunner(nil)
	svc := newJailCreateTestService(db, runner, "tank")
	checker := &jailCreateGuestIdentityCheckerStub{err: fmt.Errorf("guest_id_already_in_use")}
	svc.SetGuestIdentityAvailabilityChecker(checker)

	baseDir := filepath.Join(t.TempDir(), "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("create base directory: %v", err)
	}
	seedBaseDownload(t, db, "base-identity-conflict", baseDir)
	req := jailCreateRequest(702, "tank", "base-identity-conflict")

	err := svc.CreateJail(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "guest_id_already_in_use") {
		t.Fatalf("guest identity rejection error = %v", err)
	}
	if len(checker.guestIDs) != 1 || checker.guestIDs[0] != 702 {
		t.Fatalf("checked guest IDs = %v, want [702]", checker.guestIDs)
	}
	runner.mu.Lock()
	createCalls := runner.createCalls
	runner.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("ZFS create calls after rejected jail create = %d, want 0", createCalls)
	}
	assertModelCount(t, db, &jailModels.Jail{}, 0, "")
}

func TestCreateJail_FailsWhenBaseIsNotDirectoryBeforeProvisioningSideEffects(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.Network{},
		&utilitiesModels.Downloads{},
	)

	runner := newJailCreateTestZFSRunner(nil)
	svc := newJailCreateTestService(db, runner, "tank")

	baseFile := filepath.Join(t.TempDir(), "base-file")
	if err := os.WriteFile(baseFile, []byte("not-a-directory"), 0644); err != nil {
		t.Fatalf("failed to create base file: %v", err)
	}
	seedBaseDownload(t, db, "base-file-download", baseFile)

	const ctid uint = 702
	req := jailCreateRequest(ctid, "tank", "base-file-download")

	err := svc.CreateJail(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "base_is_not_a_directory") {
		t.Fatalf("expected base_is_not_a_directory error, got %v", err)
	}

	if runner.getCreateCalls() != 0 {
		t.Fatalf("expected no dataset create calls when base precheck fails, got %d", runner.getCreateCalls())
	}

	assertModelCount(t, db, &jailModels.Jail{}, 0, "")
	assertModelCount(t, db, &jailModels.Storage{}, 0, "")
	assertModelCount(t, db, &jailModels.Network{}, 0, "")
}

func TestCreateJail_LinuxPersistsResolvConf(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.Network{},
		&jailModels.JailHooks{},
		&jailModels.JailStats{},
		&jailModels.JailSnapshot{},
		&utilitiesModels.Downloads{},
	)

	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, "pool")
	if err := os.MkdirAll(poolDir, 0755); err != nil {
		t.Fatalf("failed to create pool directory: %v", err)
	}

	baseDir := filepath.Join(tmp, "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create base directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "README"), []byte("seed"), 0644); err != nil {
		t.Fatalf("failed to seed base content: %v", err)
	}

	seedBaseDownload(t, db, "base-linux-resolv", baseDir)

	runner := newJailCreateTestZFSRunner(nil)
	svc := newJailCreateTestService(db, runner, poolDir)

	const ctid uint = 770
	const resolvConf = "nameserver 1.1.1.1\nnameserver 1.0.0.1\n"

	req := jailCreateRequest(ctid, poolDir, "base-linux-resolv")
	req.Type = jailModels.JailTypeLinux
	req.ResolvConf = resolvConf

	if err := svc.CreateJail(context.Background(), req); err != nil {
		t.Fatalf("expected linux jail create to succeed, got %v", err)
	}

	var created jailModels.Jail
	if err := db.Where("ct_id = ?", ctid).First(&created).Error; err != nil {
		t.Fatalf("failed to query created jail row: %v", err)
	}
	if created.ResolvConf != resolvConf {
		t.Fatalf("expected resolv_conf %q, got %q", resolvConf, created.ResolvConf)
	}

	resolvPath := filepath.Join(poolDir, "sylve", "jails", fmt.Sprintf("%d", ctid), "etc", "resolv.conf")
	gotResolv, err := os.ReadFile(resolvPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", resolvPath, err)
	}
	if string(gotResolv) != resolvConf {
		t.Fatalf("expected resolv.conf content %q, got %q", resolvConf, string(gotResolv))
	}
}

func TestCreateJailSerializesConcurrentSameCTIDRequests(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.Network{},
		&jailModels.JailHooks{},
		&jailModels.JailStats{},
		&jailModels.JailSnapshot{},
		&utilitiesModels.Downloads{},
	)
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, "pool")
	if err := os.MkdirAll(poolDir, 0755); err != nil {
		t.Fatalf("create pool directory: %v", err)
	}
	baseDir := filepath.Join(tmp, "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("create base directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "README"), []byte("seed"), 0644); err != nil {
		t.Fatalf("seed base content: %v", err)
	}
	seedBaseDownload(t, db, "base-concurrent-create", baseDir)

	runner := newJailCreateTestZFSRunner(nil)
	svc := newJailCreateTestService(db, runner, poolDir)
	checker := newBlockingJailCreateGuestIdentityChecker()
	svc.SetGuestIdentityAvailabilityChecker(checker)
	var releaseOnce sync.Once
	releaseFirst := func() {
		releaseOnce.Do(func() { close(checker.releaseFirst) })
	}
	defer releaseFirst()

	const ctid uint = 771
	req := jailCreateRequest(ctid, poolDir, "base-concurrent-create")
	req.Type = jailModels.JailTypeLinux
	results := make(chan error, 2)

	go func() {
		results <- svc.CreateJail(context.Background(), req)
	}()
	select {
	case <-checker.firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first create did not reach identity validation")
	}

	go func() {
		results <- svc.CreateJail(context.Background(), req)
	}()
	select {
	case <-checker.secondEntered:
		releaseFirst()
		t.Fatal("second create entered validation before first create completed")
	case <-time.After(250 * time.Millisecond):
	}
	releaseFirst()

	successes := 0
	conflicts := 0
	for range 2 {
		select {
		case err := <-results:
			switch {
			case err == nil:
				successes++
			case strings.Contains(err.Error(), "jail_with_ctid_already_exists"):
				conflicts++
			default:
				t.Fatalf("unexpected concurrent create error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent jail creates did not finish")
		}
	}

	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes=%d conflicts=%d", successes, conflicts)
	}
	if checker.callCount() != 1 {
		t.Fatalf("identity checks = %d, want 1", checker.callCount())
	}
	if runner.getCreateCalls() != 1 {
		t.Fatalf("ZFS create calls = %d, want 1", runner.getCreateCalls())
	}
	runner.mu.Lock()
	destroyCalls := runner.destroyCalls
	runner.mu.Unlock()
	if destroyCalls != 0 {
		t.Fatalf("ZFS destroy calls = %d, want 0", destroyCalls)
	}
	assertModelCount(t, db, &jailModels.Jail{}, 1, "ct_id = ?", ctid)
	rootDataset := fmt.Sprintf("%s/sylve/jails/%d", poolDir, ctid)
	if !runner.hasDataset(rootDataset) {
		t.Fatalf("successful jail dataset %q was removed", rootDataset)
	}
}

func TestValidateCreate_AcceptsRawIPv4(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&utilitiesModels.Downloads{},
	)

	runner := newJailCreateTestZFSRunner(nil)
	svc := newJailCreateTestService(db, runner, "tank")

	baseDir := filepath.Join(t.TempDir(), "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create base directory: %v", err)
	}
	seedBaseDownload(t, db, "base-raw-ipv4", baseDir)

	ctid := uint(790)
	req := jailServiceInterfaces.CreateJailRequest{
		Name:       fmt.Sprintf("jail-%d", ctid),
		CTID:       &ctid,
		Pool:       "tank",
		Base:       "base-raw-ipv4",
		SwitchName: "none",
		Type:       jailModels.JailTypeFreeBSD,
	}

	err := svc.ValidateCreate(context.Background(), req)
	if err != nil {
		t.Fatalf("expected validation to pass, got %v", err)
	}
}

func TestValidateCreate_RejectsInvalidRawIPv4CIDR(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&utilitiesModels.Downloads{},
	)
	seedSwitch(t, db, "test-switch")

	runner := newJailCreateTestZFSRunner(nil)
	svc := newJailCreateTestService(db, runner, "tank")

	baseDir := filepath.Join(t.TempDir(), "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create base directory: %v", err)
	}
	seedBaseDownload(t, db, "base-bad-raw-ipv4", baseDir)

	ctid := uint(791)
	req := jailServiceInterfaces.CreateJailRequest{
		Name:       fmt.Sprintf("jail-%d", ctid),
		CTID:       &ctid,
		Pool:       "tank",
		Base:       "base-bad-raw-ipv4",
		SwitchName: "test-switch",
		Type:       jailModels.JailTypeFreeBSD,
		IPv4Raw:    "not-a-cidr",
	}

	err := svc.ValidateCreate(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "invalid_ipv4_cidr") {
		t.Fatalf("expected invalid_ipv4_cidr error, got %v", err)
	}
}

func TestCleanupFailedJailCreate_RemovesArtifactsAndOnlyAutoCreatedMACs(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.Network{},
		&jailModels.JailHooks{},
		&jailModels.JailStats{},
		&jailModels.JailSnapshot{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.DHCPStaticLease{},
		&vmModels.Network{},
	)

	const ctid uint = 740
	rootDataset := fmt.Sprintf("tank/sylve/jails/%d", ctid)
	runner := newJailCreateTestZFSRunner([]string{rootDataset})
	svc := newJailCreateTestService(db, runner, "tank")

	autoMAC := networkModels.Object{Name: "auto-mac-740", Type: "Mac"}
	if err := db.Create(&autoMAC).Error; err != nil {
		t.Fatalf("failed to seed auto MAC object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: autoMAC.ID, Value: "02:00:00:00:74:00"}).Error; err != nil {
		t.Fatalf("failed to seed auto MAC entry: %v", err)
	}
	if err := db.Create(&networkModels.ObjectResolution{ObjectID: autoMAC.ID, ResolvedIP: "192.0.2.74"}).Error; err != nil {
		t.Fatalf("failed to seed auto MAC resolution: %v", err)
	}

	userMAC := networkModels.Object{Name: "user-mac-740", Type: "Mac"}
	if err := db.Create(&userMAC).Error; err != nil {
		t.Fatalf("failed to seed user MAC object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: userMAC.ID, Value: "02:00:00:00:74:01"}).Error; err != nil {
		t.Fatalf("failed to seed user MAC entry: %v", err)
	}

	jail := jailModels.Jail{
		Name: "jail-740",
		CTID: ctid,
		Type: jailModels.JailTypeFreeBSD,
	}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("failed to seed jail row: %v", err)
	}

	if err := db.Create(&jailModels.Storage{
		JailID: jail.ID,
		Pool:   "tank",
		GUID:   "guid-jail-740",
		Name:   "Base Filesystem",
		IsBase: true,
	}).Error; err != nil {
		t.Fatalf("failed to seed jail storage row: %v", err)
	}

	if err := db.Create(&jailModels.Network{
		JailID:     jail.ID,
		Name:       "Initial",
		SwitchID:   1,
		SwitchType: "manual",
		MacID:      &autoMAC.ID,
	}).Error; err != nil {
		t.Fatalf("failed to seed jail network row: %v", err)
	}

	if err := db.Create(&jailModels.JailHooks{
		JailID:  jail.ID,
		Phase:   jailModels.JailHookPhaseStart,
		Enabled: true,
		Script:  "echo test",
	}).Error; err != nil {
		t.Fatalf("failed to seed jail hook row: %v", err)
	}

	if err := db.Create(&jailModels.JailStats{
		JailID:      jail.ID,
		CPUUsage:    0.1,
		MemoryUsage: 0.2,
	}).Error; err != nil {
		t.Fatalf("failed to seed jail stats row: %v", err)
	}

	if err := db.Create(&jailModels.JailSnapshot{
		JailID:       jail.ID,
		CTID:         ctid,
		Name:         "snap-1",
		SnapshotName: "jail-740@snap-1",
		RootDataset:  rootDataset,
	}).Error; err != nil {
		t.Fatalf("failed to seed jail snapshot row: %v", err)
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		t.Fatalf("failed to resolve jails path: %v", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctid))
	if err := os.MkdirAll(jailDir, 0755); err != nil {
		t.Fatalf("failed to seed jail runtime directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jailDir, "740.conf"), []byte("config"), 0644); err != nil {
		t.Fatalf("failed to seed jail runtime config file: %v", err)
	}

	svc.cleanupFailedJailCreate(ctid, "tank", []uint{autoMAC.ID})

	if _, statErr := os.Stat(jailDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected jail runtime directory to be removed, statErr=%v", statErr)
	}

	if runner.hasDataset(rootDataset) {
		t.Fatalf("expected jail root dataset %q to be removed by rollback cleanup", rootDataset)
	}

	assertModelCount(t, db, &jailModels.Jail{}, 0, "ct_id = ?", ctid)
	assertModelCount(t, db, &jailModels.Storage{}, 0, "jid = ?", jail.ID)
	assertModelCount(t, db, &jailModels.Network{}, 0, "jid = ?", jail.ID)
	assertModelCount(t, db, &jailModels.JailHooks{}, 0, "jid = ?", jail.ID)
	assertModelCount(t, db, &jailModels.JailStats{}, 0, "jid = ?", jail.ID)
	assertModelCount(t, db, &jailModels.JailSnapshot{}, 0, "jid = ?", jail.ID)

	assertModelCount(t, db, &networkModels.Object{}, 0, "id = ?", autoMAC.ID)
	assertModelCount(t, db, &networkModels.ObjectEntry{}, 0, "object_id = ?", autoMAC.ID)
	assertModelCount(t, db, &networkModels.ObjectResolution{}, 0, "object_id = ?", autoMAC.ID)

	assertModelCount(t, db, &networkModels.Object{}, 1, "id = ?", userMAC.ID)
	assertModelCount(t, db, &networkModels.ObjectEntry{}, 1, "object_id = ?", userMAC.ID)
}

func TestCreateJail_PostCommitFailureCleansResidualArtifacts(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.Network{},
		&jailModels.JailHooks{},
		&jailModels.JailStats{},
		&jailModels.JailSnapshot{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.DHCPStaticLease{},
		&vmModels.Network{},
		&utilitiesModels.Downloads{},
	)

	tmp := t.TempDir()
	poolAsFile := filepath.Join(tmp, "pool-as-file")
	if err := os.WriteFile(poolAsFile, []byte("not-a-directory"), 0644); err != nil {
		t.Fatalf("failed to create pool blocker file: %v", err)
	}

	const ctid uint = 760
	rootDataset := fmt.Sprintf("%s/sylve/jails/%d", poolAsFile, ctid)
	runner := newJailCreateTestZFSRunner(nil)
	svc := newJailCreateTestService(db, runner, poolAsFile)

	baseDir := filepath.Join(tmp, "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create base directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "README"), []byte("seed"), 0644); err != nil {
		t.Fatalf("failed to seed base content: %v", err)
	}
	seedBaseDownload(t, db, "base-post-commit-failure", baseDir)

	req := jailCreateRequest(ctid, poolAsFile, "base-post-commit-failure")
	err := svc.CreateJail(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "failed_to_copy_base") {
		t.Fatalf("expected failed_to_copy_base error, got %v", err)
	}

	assertModelCount(t, db, &jailModels.Jail{}, 0, "ct_id = ?", ctid)
	assertModelCount(t, db, &jailModels.Storage{}, 0, "")
	assertModelCount(t, db, &jailModels.Network{}, 0, "")

	if runner.hasDataset(rootDataset) {
		t.Fatalf("expected rollback to remove root dataset %q after post-commit failure", rootDataset)
	}
}

func TestValidateCreateRejectsInvalidVLAN(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&utilitiesModels.Downloads{},
	)

	svc := &Service{
		DB:     db,
		System: jailCreateTestSystemService{pools: []*gzfs.ZPool{{Name: "tank"}}},
	}

	baseDir := filepath.Join(t.TempDir(), "base")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("failed to create base directory: %v", err)
	}
	seedBaseDownload(t, db, "base-vlan-validate", baseDir)

	ctid := uint(780)
	vlan := 5000
	req := jailServiceInterfaces.CreateJailRequest{
		Name:       fmt.Sprintf("jail-%d", ctid),
		CTID:       &ctid,
		Pool:       "tank",
		Base:       "base-vlan-validate",
		SwitchName: "none",
		Type:       jailModels.JailTypeFreeBSD,
		VLAN:       &vlan,
	}

	err := svc.ValidateCreate(context.Background(), req)
	if err == nil {
		t.Fatal("expected invalid_vlan error, got nil")
	}
	if err.Error() != "invalid_vlan" {
		t.Fatalf("expected invalid_vlan error, got %q", err.Error())
	}
}
