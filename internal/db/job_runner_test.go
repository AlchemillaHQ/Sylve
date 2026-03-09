// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"maragu.dev/goqite"
)

func TestJobRunnerAllowsInFlightJobsToFinishAfterShutdown(t *testing.T) {
	sqlDB, err := sql.Open("sqlite3", ":memory:?_journal=WAL&_timeout=5000&_fk=true")
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	defer sqlDB.Close()

	if err := goqite.Setup(context.Background(), sqlDB); err != nil {
		t.Fatalf("failed to setup goqite schema: %v", err)
	}

	queue := goqite.New(goqite.NewOpts{
		DB:      sqlDB,
		Name:    "jobs",
		Timeout: 25 * time.Millisecond,
	})

	runner := newJobRunner(jobRunnerOpts{
		Queue:        queue,
		PollInterval: 5 * time.Millisecond,
		Extend:       25 * time.Millisecond,
	})

	started := make(chan struct{})
	finished := make(chan struct{})
	unblock := make(chan struct{})
	errCh := make(chan error, 1)

	runner.Register("long-job", func(ctx context.Context, _ []byte) error {
		close(started)
		<-unblock
		if ctx.Err() != nil {
			errCh <- ctx.Err()
		}
		close(finished)
		return nil
	})

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runnerDone := make(chan struct{})
	go func() {
		runner.Start(runCtx)
		close(runnerDone)
	}()

	if err := createJobMessage(context.Background(), queue, "long-job", nil); err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for job to start")
	}

	cancel()

	select {
	case <-runnerDone:
		t.Fatal("runner exited before in-flight job finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(unblock)

	select {
	case <-finished:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for in-flight job to finish")
	}

	select {
	case err := <-errCh:
		t.Fatalf("expected in-flight job context to remain alive after shutdown, got %v", err)
	default:
	}

	select {
	case <-runnerDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for runner shutdown")
	}
}
