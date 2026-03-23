// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"bytes"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/dgraph-io/badger/v4"
)

func TestSetupCacheAssignsGlobalAndPersistsRoundTrip(t *testing.T) {
	prev := CacheDB
	cfg := &internal.SylveConfig{
		DataPath: t.TempDir(),
	}

	cache := SetupCache(cfg)
	if cache == nil {
		t.Fatal("expected non-nil cache instance")
	}

	if CacheDB != cache {
		t.Fatal("expected SetupCache to assign CacheDB global")
	}

	t.Cleanup(func() {
		_ = cache.Close()
		CacheDB = prev
	})

	if err := SetValue("setup/probe", []byte("ok"), 30); err != nil {
		t.Fatalf("failed_to_set_probe_value: %v", err)
	}

	got, ok := GetValue("setup/probe")
	if !ok {
		t.Fatal("expected probe key to exist")
	}
	if !bytes.Equal(got, []byte("ok")) {
		t.Fatalf("expected probe value %q, got %q", "ok", string(got))
	}
}

func TestSetValueAndGetValueRoundTrip(t *testing.T) {
	_ = installTempCacheDB(t)

	want := []byte("hello-cache")
	if err := SetValue("greeting", want, 60); err != nil {
		t.Fatalf("set_value_failed: %v", err)
	}

	got, ok := GetValue("greeting")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("expected value %q, got %q", string(want), string(got))
	}
}

func TestGetValueMissingKeyReturnsFalse(t *testing.T) {
	_ = installTempCacheDB(t)

	got, ok := GetValue("missing")
	if ok {
		t.Fatal("expected missing key lookup to return ok=false")
	}
	if got != nil {
		t.Fatalf("expected nil value for missing key, got %q", string(got))
	}
}

func TestSetValueWithTTLExpires(t *testing.T) {
	_ = installTempCacheDB(t)

	if err := SetValue("ephemeral", []byte("soon-gone"), 1); err != nil {
		t.Fatalf("set_value_failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	got, ok := GetValue("ephemeral")
	if ok {
		t.Fatalf("expected key to expire, got value=%q", string(got))
	}
}

func TestSetValueReturnsErrorAfterCacheClose(t *testing.T) {
	cache := installTempCacheDB(t)

	if err := cache.Close(); err != nil {
		t.Fatalf("failed_to_close_cache: %v", err)
	}

	if err := SetValue("closed", []byte("x"), 10); err == nil {
		t.Fatal("expected SetValue to fail on closed cache")
	}
}

func TestRunCacheGCDoesNotHang(t *testing.T) {
	_ = installTempCacheDB(t)

	done := make(chan struct{})
	go func() {
		RunCacheGC()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunCacheGC did not return in time")
	}
}

func installTempCacheDB(t *testing.T) *badger.DB {
	t.Helper()

	prev := CacheDB
	opts := badger.DefaultOptions(t.TempDir()).
		WithLoggingLevel(badger.ERROR).
		WithDetectConflicts(false)

	cache, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("failed_to_open_badger_test_cache: %v", err)
	}

	CacheDB = cache
	t.Cleanup(func() {
		_ = cache.Close()
		CacheDB = prev
	})

	return cache
}
