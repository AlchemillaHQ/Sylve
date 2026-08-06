// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	mdnsInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/mdns"
	"github.com/alchemillahq/sylve/internal/testutil"
	dnssd "github.com/alchemillahq/sylve/pkg/network/mdns"
	"gorm.io/gorm"
)

type fakeServiceHandle struct {
	service dnssd.Service
}

func (h *fakeServiceHandle) UpdateText(map[string]string, dnssd.Responder) {}

func (h *fakeServiceHandle) Service() dnssd.Service {
	return h.service
}

type fakeResponder struct {
	started   chan struct{}
	stopped   chan struct{}
	closed    chan struct{}
	readyErr  error
	startOnce sync.Once
	stopOnce  sync.Once
	closeOnce sync.Once
}

func newFakeResponder() *fakeResponder {
	return &fakeResponder{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (r *fakeResponder) Add(service dnssd.Service) (dnssd.ServiceHandle, error) {
	return &fakeServiceHandle{service: service}, nil
}

func (r *fakeResponder) Remove(dnssd.ServiceHandle) {}

func (r *fakeResponder) Respond(ctx context.Context) error {
	return r.respond(ctx, nil)
}

func (r *fakeResponder) RespondReady(ctx context.Context, ready chan<- error) error {
	return r.respond(ctx, ready)
}

func (r *fakeResponder) respond(ctx context.Context, ready chan<- error) error {
	if r.readyErr != nil {
		if ready != nil {
			ready <- r.readyErr
		}
		return r.readyErr
	}
	r.startOnce.Do(func() { close(r.started) })
	if ready != nil {
		ready <- nil
	}
	<-ctx.Done()
	r.stopOnce.Do(func() { close(r.stopped) })
	return ctx.Err()
}

func (r *fakeResponder) Debug(context.Context, dnssd.ReadFunc) {}

func (r *fakeResponder) Close() {
	r.closeOnce.Do(func() { close(r.closed) })
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

type mdnsMutationFixture struct {
	db       *gorm.DB
	service  *Service
	settings mdnsModels.MdnsSettings
	records  []mdnsModels.MdnsRecord
	initial  *fakeResponder
}

func newMDNSMutationFixture(t *testing.T, responders ...*fakeResponder) *mdnsMutationFixture {
	t.Helper()

	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&mdnsModels.MdnsSettings{},
		&mdnsModels.MdnsRecord{},
	)
	if err := db.Create(&models.BasicSettings{
		Services: []models.AvailableService{models.Mdns},
	}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}
	settings := mdnsModels.MdnsSettings{Interfaces: "em0", Hostname: "old-host"}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("seed mDNS settings: %v", err)
	}
	records := []mdnsModels.MdnsRecord{
		{Name: "printer", Type: "_ipp._tcp", Port: 631, Txt: map[string]string{"note": "old"}},
		{Name: "scanner", Type: "_scanner._tcp", Port: 8080},
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatalf("seed mDNS records: %v", err)
	}

	nextResponder := 0
	service := &Service{
		DB: db,
		responderFactory: func() (dnssd.Responder, error) {
			if nextResponder >= len(responders) {
				return nil, errors.New("unexpected responder factory call")
			}
			responder := responders[nextResponder]
			nextResponder++
			return responder, nil
		},
	}
	if err := service.Rebuild(); err != nil {
		t.Fatalf("initial mDNS rebuild: %v", err)
	}
	waitForSignal(t, responders[0].started, "initial responder startup")

	t.Cleanup(func() {
		service.mu.Lock()
		_, _ = service.unpublishLocked()
		service.mu.Unlock()
	})

	return &mdnsMutationFixture{
		db:       db,
		service:  service,
		settings: settings,
		records:  records,
		initial:  responders[0],
	}
}

func assertResponderRollback(
	t *testing.T,
	fixture *mdnsMutationFixture,
	attempted, restored *fakeResponder,
) {
	t.Helper()

	waitForSignal(t, restored.started, "restored responder startup")
	if attempted.readyErr == nil {
		waitForSignal(t, attempted.stopped, "attempted responder shutdown")
	}
	waitForSignal(t, attempted.closed, "attempted responder close")
	waitForSignal(t, fixture.initial.stopped, "initial responder shutdown")
	waitForSignal(t, fixture.initial.closed, "initial responder close")
	if fixture.service.responder != restored {
		t.Fatal("rollback did not install the restored responder")
	}
}

type mdnsMutationTestCase struct {
	name      string
	operation string
	schema    string
	mutate    func(*mdnsMutationFixture) error
	assertDB  func(*testing.T, *mdnsMutationFixture)
}

func mdnsMutationTestCases() []mdnsMutationTestCase {
	return []mdnsMutationTestCase{
		{
			name:      "settings",
			operation: "update",
			schema:    "MdnsSettings",
			mutate: func(fixture *mdnsMutationFixture) error {
				return fixture.service.SetSettings("em1", "new-host")
			},
			assertDB: func(t *testing.T, fixture *mdnsMutationFixture) {
				var settings mdnsModels.MdnsSettings
				if err := fixture.db.First(&settings, fixture.settings.ID).Error; err != nil {
					t.Fatalf("reload mDNS settings: %v", err)
				}
				if settings.Interfaces != fixture.settings.Interfaces || settings.Hostname != fixture.settings.Hostname {
					t.Fatalf("failed settings mutation changed the database: %+v", settings)
				}
			},
		},
		{
			name:      "create record",
			operation: "create",
			schema:    "MdnsRecord",
			mutate: func(fixture *mdnsMutationFixture) error {
				_, err := fixture.service.CreateRecord("new-service", "_new._tcp", 9999, map[string]string{}, "")
				return err
			},
			assertDB: func(t *testing.T, fixture *mdnsMutationFixture) {
				var count int64
				if err := fixture.db.Model(&mdnsModels.MdnsRecord{}).
					Where("name = ? AND type = ?", "new-service", "_new._tcp").
					Count(&count).Error; err != nil {
					t.Fatalf("count created mDNS record: %v", err)
				}
				if count != 0 {
					t.Fatalf("failed create left %d records in the database", count)
				}
			},
		},
		{
			name:      "update record",
			operation: "update",
			schema:    "MdnsRecord",
			mutate: func(fixture *mdnsMutationFixture) error {
				return fixture.service.UpdateRecord(
					fixture.records[0].ID,
					"renamed-printer",
					"_printer._tcp",
					9100,
					map[string]string{"note": "new"},
					"em1",
				)
			},
			assertDB: func(t *testing.T, fixture *mdnsMutationFixture) {
				var record mdnsModels.MdnsRecord
				if err := fixture.db.First(&record, fixture.records[0].ID).Error; err != nil {
					t.Fatalf("reload updated mDNS record: %v", err)
				}
				original := fixture.records[0]
				if record.Name != original.Name || record.Type != original.Type ||
					record.Port != original.Port || record.Interfaces != original.Interfaces ||
					record.Txt["note"] != original.Txt["note"] {
					t.Fatalf("failed update changed the database: %+v", record)
				}
			},
		},
		{
			name:      "delete record",
			operation: "delete",
			schema:    "MdnsRecord",
			mutate: func(fixture *mdnsMutationFixture) error {
				return fixture.service.DeleteRecord(fixture.records[0].ID)
			},
			assertDB: func(t *testing.T, fixture *mdnsMutationFixture) {
				var record mdnsModels.MdnsRecord
				if err := fixture.db.First(&record, fixture.records[0].ID).Error; err != nil {
					t.Fatalf("failed delete removed the database record: %v", err)
				}
			},
		},
	}
}

func registerMDNSPersistenceFailure(
	t *testing.T,
	db *gorm.DB,
	operation, schema string,
	injected error,
) {
	t.Helper()

	callbackName := "test:fail_mdns_" + operation + "_" + schema
	callback := func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == schema {
			tx.AddError(injected)
		}
	}

	var err error
	switch operation {
	case "create":
		err = db.Callback().Create().Before("gorm:create").Register(callbackName, callback)
	case "update":
		err = db.Callback().Update().Before("gorm:update").Register(callbackName, callback)
	case "delete":
		err = db.Callback().Delete().Before("gorm:delete").Register(callbackName, callback)
	default:
		t.Fatalf("unsupported callback operation %q", operation)
	}
	if err != nil {
		t.Fatalf("register persistence failure callback: %v", err)
	}

	t.Cleanup(func() {
		switch operation {
		case "create":
			_ = db.Callback().Create().Remove(callbackName)
		case "update":
			_ = db.Callback().Update().Remove(callbackName)
		case "delete":
			_ = db.Callback().Delete().Remove(callbackName)
		}
	})
}

func TestGetSettingsDoesNotCreateMissingSettings(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &mdnsModels.MdnsSettings{})
	service := &Service{DB: db}

	_, err := service.GetSettings()
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected wrapped record-not-found error, got %v", err)
	}

	var count int64
	if err := db.Model(&mdnsModels.MdnsSettings{}).Count(&count).Error; err != nil {
		t.Fatalf("count mDNS settings: %v", err)
	}
	if count != 0 {
		t.Fatalf("GET settings created %d rows, want 0", count)
	}
}

func TestValidateRecordInputClassifiesInvalidRecords(t *testing.T) {
	tests := []struct {
		name       string
		recordName string
		recordType string
		port       int
	}{
		{name: "missing name", recordType: "_test._tcp", port: 1234},
		{name: "invalid type", recordName: "test", recordType: "test", port: 1234},
		{name: "invalid port", recordName: "test", recordType: "_test._tcp", port: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRecordInput(test.recordName, test.recordType, test.port)
			if err == nil || !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("expected ErrInvalidRecord, got %v", err)
			}
		})
	}
}

func TestMissingRecordMutationsReturnNotFound(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &mdnsModels.MdnsRecord{})
	service := &Service{DB: db}

	updateErr := service.UpdateRecord(42, "test", "_test._tcp", 1234, map[string]string{}, "")
	if updateErr == nil || !errors.Is(updateErr, ErrRecordNotFound) {
		t.Fatalf("expected update to return ErrRecordNotFound, got %v", updateErr)
	}

	deleteErr := service.DeleteRecord(42)
	if deleteErr == nil || !errors.Is(deleteErr, ErrRecordNotFound) {
		t.Fatalf("expected delete to return ErrRecordNotFound, got %v", deleteErr)
	}
}

func TestCreateRecordRejectsDuplicateIdentity(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&mdnsModels.MdnsRecord{},
	)
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}
	existing := mdnsModels.MdnsRecord{Name: "printer", Type: "_ipp._tcp", Port: 631}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("seed mDNS record: %v", err)
	}

	service := &Service{DB: db}
	_, err := service.CreateRecord("printer", "_ipp._tcp", 8631, map[string]string{}, "")
	if err == nil || !errors.Is(err, ErrRecordConflict) {
		t.Fatalf("expected duplicate create to return ErrRecordConflict, got %v", err)
	}

	created, err := service.CreateRecord("printer", "_printer._tcp", 9100, map[string]string{}, "")
	if err != nil {
		t.Fatalf("same name with a different type should be allowed: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected the distinct record to be persisted")
	}

	directDuplicate := mdnsModels.MdnsRecord{Name: existing.Name, Type: existing.Type, Port: 9999}
	if err := db.Create(&directDuplicate).Error; err == nil {
		t.Fatal("expected the database identity index to reject a duplicate")
	}
}

func TestUpdateRecordRejectsDuplicateIdentity(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&mdnsModels.MdnsRecord{},
	)
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}
	first := mdnsModels.MdnsRecord{Name: "printer", Type: "_ipp._tcp", Port: 631}
	second := mdnsModels.MdnsRecord{Name: "scanner", Type: "_scanner._tcp", Port: 8080}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("seed first mDNS record: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("seed second mDNS record: %v", err)
	}

	service := &Service{DB: db}
	err := service.UpdateRecord(second.ID, first.Name, first.Type, 9100, map[string]string{}, "")
	if err == nil || !errors.Is(err, ErrRecordConflict) {
		t.Fatalf("expected duplicate update to return ErrRecordConflict, got %v", err)
	}

	var unchanged mdnsModels.MdnsRecord
	if err := db.First(&unchanged, second.ID).Error; err != nil {
		t.Fatalf("reload second mDNS record: %v", err)
	}
	if unchanged.Name != second.Name || unchanged.Type != second.Type || unchanged.Port != second.Port {
		t.Fatalf("conflicting update changed the record: %+v", unchanged)
	}

	if err := service.UpdateRecord(first.ID, first.Name, first.Type, 8631, map[string]string{}, ""); err != nil {
		t.Fatalf("updating a record without changing its identity failed: %v", err)
	}
}

func TestRecordMutationsRejectManagedIdentity(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&mdnsModels.MdnsRecord{},
		&sambaModels.SambaSettings{},
		&sambaModels.SambaShare{},
	)
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}
	if err := db.Create(&sambaModels.SambaSettings{}).Error; err != nil {
		t.Fatalf("seed Samba settings: %v", err)
	}
	if err := db.Create(&sambaModels.SambaShare{Name: "documents", Dataset: "tank/documents", Enabled: true}).Error; err != nil {
		t.Fatalf("seed Samba share: %v", err)
	}
	userRecord := mdnsModels.MdnsRecord{Name: "custom", Type: "_custom._tcp", Port: 1234}
	if err := db.Create(&userRecord).Error; err != nil {
		t.Fatalf("seed user mDNS record: %v", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("get hostname: %v", err)
	}
	service := &Service{DB: db}
	_, err = service.CreateRecord(hostname, "_smb._tcp", 1445, map[string]string{}, "")
	if err == nil || !errors.Is(err, ErrRecordConflict) {
		t.Fatalf("expected managed create conflict, got %v", err)
	}

	err = service.UpdateRecord(userRecord.ID, hostname, "_smb._tcp", 1445, map[string]string{}, "")
	if err == nil || !errors.Is(err, ErrRecordConflict) {
		t.Fatalf("expected managed update conflict, got %v", err)
	}

	var unchanged mdnsModels.MdnsRecord
	if err := db.First(&unchanged, userRecord.ID).Error; err != nil {
		t.Fatalf("reload user mDNS record: %v", err)
	}
	if unchanged.Name != userRecord.Name || unchanged.Type != userRecord.Type {
		t.Fatalf("managed conflict changed the user record: %+v", unchanged)
	}
}

func TestGatherManagedRecordsForAppleSambaShares(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&mdnsModels.MdnsSettings{},
		&mdnsModels.MdnsRecord{},
		&sambaModels.SambaSettings{},
		&sambaModels.SambaShare{},
	)

	if err := db.Create(&sambaModels.SambaSettings{AppleExtensions: true}).Error; err != nil {
		t.Fatalf("failed to create samba settings: %v", err)
	}
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}
	if err := db.Create(&sambaModels.SambaShare{Name: "documents", Dataset: "dataset-1"}).Error; err != nil {
		t.Fatalf("failed to create samba share: %v", err)
	}
	if err := db.Create(&sambaModels.SambaShare{Name: "backups", Dataset: "dataset-2", TimeMachine: true}).Error; err != nil {
		t.Fatalf("failed to create time machine share: %v", err)
	}

	service := &Service{DB: db}
	records, err := service.gatherManagedRecords(db)
	if err != nil {
		t.Fatalf("gathering managed records failed: %v", err)
	}

	byType := make(map[string]mdnsModels.MdnsRecord, len(records))
	for _, record := range records {
		byType[record.Type] = record.MdnsRecord
	}

	smb, ok := byType["_smb._tcp"]
	if !ok || smb.Port != 445 {
		t.Fatalf("expected SMB record on port 445, got %+v", smb)
	}
	if smb.Txt == nil || len(smb.Txt) != 0 {
		t.Fatalf("expected SMB TXT to be an empty object, got %#v", smb.Txt)
	}
	payload, err := json.Marshal(smb)
	if err != nil {
		t.Fatalf("failed to marshal SMB record: %v", err)
	}
	if !bytes.Contains(payload, []byte(`"txt":{}`)) {
		t.Fatalf("expected SMB TXT to serialize as an object, got %s", payload)
	}

	device, ok := byType["_device-info._tcp"]
	if !ok || device.Txt["model"] != "RackMac" {
		t.Fatalf("expected Apple device-info record, got %+v", device)
	}

	adisk, ok := byType["_adisk._tcp"]
	if !ok || adisk.Txt["dk0"] != "adVN=backups,adVF=0x82" {
		t.Fatalf("expected Time Machine adisk record, got %+v", adisk)
	}
}

func TestGatherManagedRecordsSkipsDisabledSambaShares(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&mdnsModels.MdnsSettings{},
		&mdnsModels.MdnsRecord{},
		&sambaModels.SambaSettings{},
		&sambaModels.SambaShare{},
	)
	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}
	if err := db.Create(&sambaModels.SambaSettings{AppleExtensions: true}).Error; err != nil {
		t.Fatalf("failed to create samba settings: %v", err)
	}
	share := sambaModels.SambaShare{Name: "backups", Dataset: "missing", Enabled: true, TimeMachine: true}
	if err := db.Create(&share).Error; err != nil {
		t.Fatalf("failed to create samba share: %v", err)
	}
	if err := db.Model(&share).Update("enabled", false).Error; err != nil {
		t.Fatalf("failed to disable samba share: %v", err)
	}

	records, err := (&Service{DB: db}).gatherManagedRecords(db)
	if err != nil {
		t.Fatalf("gathering managed records failed: %v", err)
	}
	if len(records) != 1 || records[0].Type != "_device-info._tcp" {
		t.Fatalf("disabled share produced service records: %+v", records)
	}
}

func TestGetRecordsSkipsManagedRecordsWhenSambaIsDisabled(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&mdnsModels.MdnsSettings{},
		&mdnsModels.MdnsRecord{},
		&sambaModels.SambaSettings{},
		&sambaModels.SambaShare{},
	)

	if err := db.Create(&models.BasicSettings{Services: []models.AvailableService{models.Mdns}}).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}
	if err := db.Create(&sambaModels.SambaSettings{AppleExtensions: true}).Error; err != nil {
		t.Fatalf("failed to create samba settings: %v", err)
	}
	if err := db.Create(&sambaModels.SambaShare{Name: "backups", Dataset: "dataset-1", TimeMachine: true}).Error; err != nil {
		t.Fatalf("failed to create samba share: %v", err)
	}
	if err := db.Create(&mdnsModels.MdnsRecord{Name: "custom", Type: "_custom._tcp", Port: 1234}).Error; err != nil {
		t.Fatalf("failed to create user mDNS record: %v", err)
	}

	service := &Service{DB: db}
	records, err := service.GetRecords()
	if err != nil {
		t.Fatalf("getting mDNS records failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected only the user-created record, got %d records", len(records))
	}
	if records[0].Managed || records[0].Name != "custom" {
		t.Fatalf("unexpected record while Samba is disabled: %+v", records[0])
	}
}

func TestPublishLockedReplacesRunningResponder(t *testing.T) {
	first := newFakeResponder()
	second := newFakeResponder()
	responders := []dnssd.Responder{first, second}
	factoryCalls := 0

	service := &Service{
		responderFactory: func() (dnssd.Responder, error) {
			responder := responders[factoryCalls]
			factoryCalls++
			return responder, nil
		},
	}
	t.Cleanup(func() {
		service.mu.Lock()
		_, _ = service.unpublishLocked()
		service.mu.Unlock()
	})
	records := []mdnsInterfaces.MdnsRecordWithManaged{{
		MdnsRecord: mdnsModels.MdnsRecord{
			Name: "test",
			Type: "_test._tcp",
			Port: 1234,
		},
	}}

	if _, err := service.publishLocked(records, mdnsModels.MdnsSettings{}); err != nil {
		t.Fatalf("first publish failed: %v", err)
	}
	waitForSignal(t, first.started, "first responder startup")

	if _, err := service.publishLocked(records, mdnsModels.MdnsSettings{}); err != nil {
		t.Fatalf("second publish failed: %v", err)
	}
	waitForSignal(t, second.started, "second responder startup")

	if factoryCalls != 2 {
		t.Fatalf("expected a fresh responder for each publish, got %d factory calls", factoryCalls)
	}
	if service.responder != second {
		t.Fatal("second publish did not install the replacement responder")
	}
	select {
	case <-first.stopped:
	default:
		t.Fatal("first responder was not stopped before replacement")
	}
	select {
	case <-first.closed:
	default:
		t.Fatal("first responder was not closed before replacement")
	}
}

func TestMutationsDoNotPersistWhenResponderActivationFails(t *testing.T) {
	for _, test := range mdnsMutationTestCases() {
		t.Run(test.name, func(t *testing.T) {
			injected := errors.New("injected responder registration failure")
			initial := newFakeResponder()
			attempted := newFakeResponder()
			attempted.readyErr = injected
			restored := newFakeResponder()
			fixture := newMDNSMutationFixture(t, initial, attempted, restored)

			err := test.mutate(fixture)
			if err == nil || !errors.Is(err, injected) {
				t.Fatalf("mutation error = %v, want injected activation failure", err)
			}
			test.assertDB(t, fixture)
			assertResponderRollback(t, fixture, attempted, restored)
		})
	}
}

func TestMutationsRestoreResponderWhenPersistenceFails(t *testing.T) {
	for _, test := range mdnsMutationTestCases() {
		t.Run(test.name, func(t *testing.T) {
			injected := errors.New("injected database persistence failure")
			initial := newFakeResponder()
			attempted := newFakeResponder()
			restored := newFakeResponder()
			fixture := newMDNSMutationFixture(t, initial, attempted, restored)
			registerMDNSPersistenceFailure(
				t,
				fixture.db,
				test.operation,
				test.schema,
				injected,
			)

			err := test.mutate(fixture)
			if err == nil || !errors.Is(err, injected) {
				t.Fatalf("mutation error = %v, want injected persistence failure", err)
			}
			test.assertDB(t, fixture)
			assertResponderRollback(t, fixture, attempted, restored)
		})
	}
}

func TestRebuildRestoresPreviousActiveStateAfterActivationFailure(t *testing.T) {
	injected := errors.New("injected responder registration failure")
	initial := newFakeResponder()
	attempted := newFakeResponder()
	attempted.readyErr = injected
	restored := newFakeResponder()
	fixture := newMDNSMutationFixture(t, initial, attempted, restored)

	if err := fixture.db.Model(&mdnsModels.MdnsSettings{}).
		Where("id = ?", fixture.settings.ID).
		Update("hostname", "externally-changed-host").Error; err != nil {
		t.Fatalf("change persisted settings before rebuild: %v", err)
	}

	err := fixture.service.Rebuild()
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("rebuild error = %v, want injected activation failure", err)
	}
	assertResponderRollback(t, fixture, attempted, restored)
	if fixture.service.activeState == nil ||
		fixture.service.activeState.settings.Hostname != fixture.settings.Hostname {
		t.Fatalf("rebuild did not restore the previous active state: %+v", fixture.service.activeState)
	}

	var persisted mdnsModels.MdnsSettings
	if err := fixture.db.First(&persisted, fixture.settings.ID).Error; err != nil {
		t.Fatalf("reload externally changed settings: %v", err)
	}
	if persisted.Hostname != "externally-changed-host" {
		t.Fatalf("failed rebuild changed persisted settings: %+v", persisted)
	}
}

func TestPublishValidatesAllServicesBeforeStoppingResponder(t *testing.T) {
	initial := newFakeResponder()
	service := &Service{
		responderFactory: func() (dnssd.Responder, error) {
			return initial, nil
		},
	}
	t.Cleanup(func() {
		service.mu.Lock()
		_, _ = service.unpublishLocked()
		service.mu.Unlock()
	})

	valid := []mdnsInterfaces.MdnsRecordWithManaged{{
		MdnsRecord: mdnsModels.MdnsRecord{Name: "valid", Type: "_valid._tcp", Port: 1234},
	}}
	if _, err := service.publishLocked(valid, mdnsModels.MdnsSettings{}); err != nil {
		t.Fatalf("publish valid service: %v", err)
	}
	waitForSignal(t, initial.started, "initial responder startup")

	invalid := append(valid, mdnsInterfaces.MdnsRecordWithManaged{
		MdnsRecord: mdnsModels.MdnsRecord{Type: "_invalid._tcp", Port: 1234},
	})
	changed, err := service.publishLocked(invalid, mdnsModels.MdnsSettings{})
	if err == nil {
		t.Fatal("expected invalid service to fail before responder replacement")
	}
	if changed {
		t.Fatal("invalid service reported a responder state change")
	}
	if service.responder != initial {
		t.Fatal("invalid service displaced the running responder")
	}
	select {
	case <-initial.stopped:
		t.Fatal("invalid service stopped the running responder")
	default:
	}
	select {
	case <-initial.closed:
		t.Fatal("invalid service closed the running responder")
	default:
	}
}
