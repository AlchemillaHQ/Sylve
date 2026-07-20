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
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	mdnsInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/mdns"
	"github.com/alchemillahq/sylve/internal/testutil"
	dnssd "github.com/alchemillahq/sylve/pkg/network/mdns"
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
	r.startOnce.Do(func() { close(r.started) })
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
	records, err := service.gatherManagedRecords()
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
		_ = service.unpublishLocked()
		service.mu.Unlock()
	})
	records := []mdnsInterfaces.MdnsRecordWithManaged{{
		MdnsRecord: mdnsModels.MdnsRecord{
			Name: "test",
			Type: "_test._tcp",
			Port: 1234,
		},
	}}

	if err := service.publishLocked(records, mdnsModels.MdnsSettings{}); err != nil {
		t.Fatalf("first publish failed: %v", err)
	}
	waitForSignal(t, first.started, "first responder startup")

	if err := service.publishLocked(records, mdnsModels.MdnsSettings{}); err != nil {
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
