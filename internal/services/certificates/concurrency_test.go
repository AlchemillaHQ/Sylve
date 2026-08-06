// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificates

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	sylveCrypto "github.com/alchemillahq/sylve/pkg/crypto"
)

func TestSlowRenewalOnlyBlocksTheSameCertificate(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	initialCertificate, initialKey, err := sylveCrypto.GenerateSelfSignedCertificateForDomain("slow.example.com")
	if err != nil {
		t.Fatalf("generate initial certificate: %v", err)
	}
	service.issueCertificate = func(context.Context, string, bool) ([]byte, []byte, error) {
		return initialCertificate, initialKey, nil
	}
	slow, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Slow Renewal",
		Type:   models.CertificateTypeLetsEncrypt,
		Domain: "slow.example.com",
	})
	if err != nil {
		t.Fatalf("create renewable certificate: %v", err)
	}
	fast, err := service.CreateCertificate(ctx, CertificateInput{
		Name:   "Fast Update",
		Type:   models.CertificateTypeSelfSigned,
		Domain: "fast.example.com",
	})
	if err != nil {
		t.Fatalf("create unrelated certificate: %v", err)
	}
	if err := service.DB.Model(&models.Certificate{}).Where("id = ?", slow.ID).
		Update("not_after", now.Add(renewalWindow)).Error; err != nil {
		t.Fatalf("make certificate renewable: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	releaseRenewal := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRenewal()
	service.issueCertificate = func(ctx context.Context, _ string, _ bool) ([]byte, []byte, error) {
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
			return initialCertificate, initialKey, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}

	renewDone := make(chan error, 1)
	go func() {
		_, err := service.RenewCertificate(ctx, slow.ID)
		renewDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("renewal did not reach the issuer")
	}

	updateDone := make(chan error, 1)
	go func() {
		_, err := service.UpdateCertificate(ctx, fast.ID, CertificateInput{
			Name:   "Fast Update Completed",
			Type:   models.CertificateTypeSelfSigned,
			Domain: "fast.example.com",
		})
		updateDone <- err
	}()
	select {
	case err := <-updateDone:
		if err != nil {
			t.Fatalf("unrelated update failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow renewal blocked an unrelated certificate update")
	}

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- service.DeleteCertificate(ctx, slow.ID) }()
	select {
	case err := <-deleteDone:
		t.Fatalf("same-certificate delete completed during renewal: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseRenewal()
	select {
	case err := <-renewDone:
		if err != nil {
			t.Fatalf("renew certificate: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("renewal did not finish after issuer release")
	}
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("same-certificate delete did not resume: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("same-certificate delete remained blocked")
	}
	assertCertificateLocksReleased(t, service)
}

func TestManagedBrokerRequestDoesNotBlockDifferentCertificate(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	service, first := newPendingManagedTestCertificate(t, now)
	second, err := service.CreateCertificate(context.Background(), CertificateInput{
		Name:              "Managed Two",
		Type:              models.CertificateTypeSylveManaged,
		DynamicDNSEntryID: first.DynamicDNSEntryID,
	})
	if err != nil {
		t.Fatalf("create second managed certificate: %v", err)
	}
	secondOrder := loadManagedTestOrder(t, service, second.ID)
	if err := service.DB.Model(&secondOrder).Updates(map[string]any{
		"status": models.ManagedCertificateOrderStatusFailed,
		"error":  "issuance_failed",
	}).Error; err != nil {
		t.Fatalf("fail second managed order: %v", err)
	}
	firstOrder := loadManagedTestOrder(t, service, first.ID)

	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	releaseBroker := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBroker()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, ok := decodeManagedTestRequest(writer, request)
		if !ok {
			return
		}
		if request.Method != http.MethodPost || payload.ID != firstOrder.OrderID {
			http.NotFound(writer, request)
			return
		}
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
			writeManagedTestOrder(writer, http.StatusAccepted, managedBrokerOrder{
				ID: payload.ID, Hostname: managedTestHostname, Status: string(models.ManagedCertificateOrderStatusQueued),
			})
		case <-request.Context().Done():
		}
	}))
	defer server.Close()
	service.managedBrokerURL = server.URL
	service.managedHTTPClient = server.Client()

	processDone := make(chan error, 1)
	go func() { processDone <- service.processManagedOrder(context.Background(), firstOrder.ID) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("managed order did not reach the broker")
	}

	retryDone := make(chan error, 1)
	go func() {
		_, err := service.RetryManagedCertificate(context.Background(), second.ID)
		retryDone <- err
	}()
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatalf("retry different managed certificate: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("broker request blocked a different managed certificate")
	}

	releaseBroker()
	select {
	case err := <-processDone:
		if err != nil {
			t.Fatalf("process managed certificate: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("managed order did not finish after broker release")
	}
	assertCertificateLocksReleased(t, service)
}

func TestConcurrentActivationAndDeletePreserveSelection(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if err := service.Initialize(ctx, nil); err != nil {
		t.Fatalf("initialize certificates: %v", err)
	}
	activate, err := service.CreateCertificate(ctx, CertificateInput{
		Name: "Activate", Type: models.CertificateTypeSelfSigned, Domain: "activate.example.com",
	})
	if err != nil {
		t.Fatalf("create activation certificate: %v", err)
	}
	previous, err := service.CreateCertificate(ctx, CertificateInput{
		Name: "Previous Pending", Type: models.CertificateTypeSelfSigned, Domain: "previous.example.com",
	})
	if err != nil {
		t.Fatalf("create previous pending certificate: %v", err)
	}
	if _, err := service.ActivateCertificate(ctx, previous.ID); err != nil {
		t.Fatalf("set previous pending certificate: %v", err)
	}

	service.selectionMu.Lock()
	selectionLocked := true
	defer func() {
		if selectionLocked {
			service.selectionMu.Unlock()
		}
	}()
	deleteDone := make(chan error, 1)
	activateDone := make(chan error, 1)
	go func() { deleteDone <- service.DeleteCertificate(ctx, previous.ID) }()
	go func() {
		_, err := service.ActivateCertificate(ctx, activate.ID)
		activateDone <- err
	}()
	waitForCertificateLocks(t, service, previous.ID, activate.ID)
	service.selectionMu.Unlock()
	selectionLocked = false

	deleteErr := waitForCertificateResult(t, deleteDone, "delete previous pending certificate")
	if deleteErr != nil && !errors.Is(deleteErr, ErrCertificateConflict) {
		t.Fatalf("delete previous pending certificate: %v", deleteErr)
	}
	if err := waitForCertificateResult(t, activateDone, "activate replacement certificate"); err != nil {
		t.Fatalf("activate replacement certificate: %v", err)
	}
	selection, err := service.certificateSelection(ctx)
	if err != nil {
		t.Fatalf("load certificate selection: %v", err)
	}
	if selection.pendingID != activate.ID {
		t.Fatalf("pending certificate=%d want=%d", selection.pendingID, activate.ID)
	}
	_, previousErr := service.getCertificate(ctx, previous.ID)
	if deleteErr == nil && !errors.Is(previousErr, ErrCertificateNotFound) {
		t.Fatalf("successful delete left previous certificate present: %v", previousErr)
	}
	if errors.Is(deleteErr, ErrCertificateConflict) && previousErr != nil {
		t.Fatalf("conflicted delete removed previous certificate: %v", previousErr)
	}
	assertCertificateLocksReleased(t, service)
}

func waitForCertificateResult(t *testing.T, result <-chan error, operation string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting to %s", operation)
		return nil
	}
}

func waitForCertificateLocks(t *testing.T, service *Service, ids ...uint) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		service.certificateLocksMu.Lock()
		ready := true
		for _, id := range ids {
			if service.certificateLocks[id] == nil {
				ready = false
				break
			}
		}
		service.certificateLocksMu.Unlock()
		if ready {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("certificate operations did not reach their keyed locks")
		case <-time.After(time.Millisecond):
		}
	}
}

func assertCertificateLocksReleased(t *testing.T, service *Service) {
	t.Helper()
	service.certificateLocksMu.Lock()
	remaining := len(service.certificateLocks)
	service.certificateLocksMu.Unlock()
	if remaining != 0 {
		t.Fatalf("certificate lock registry retained %d unused locks", remaining)
	}
}
