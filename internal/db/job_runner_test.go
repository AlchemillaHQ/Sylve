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
	"errors"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"maragu.dev/goqite"
)

type recordedJobLog struct {
	message string
	args    []any
}

type recordingJobLogger struct {
	mu   sync.Mutex
	logs []recordedJobLog
}

func (l *recordingJobLogger) Info(message string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, recordedJobLog{message: message, args: append([]any(nil), args...)})
}

func (l *recordingJobLogger) snapshot() []recordedJobLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]recordedJobLog(nil), l.logs...)
}

func newJobRunnerErrorPolicyTestHarness(t *testing.T) (*sql.DB, *goqite.Queue, *jobRunner) {
	t.Helper()

	sqlDB, err := sql.Open("sqlite3", ":memory:?_journal=WAL&_timeout=5000&_fk=true")
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := goqite.Setup(context.Background(), sqlDB); err != nil {
		t.Fatalf("failed to setup goqite schema: %v", err)
	}

	queue := goqite.New(goqite.NewOpts{
		DB:         sqlDB,
		Name:       "jobs",
		Timeout:    5 * time.Millisecond,
		MaxReceive: 3,
	})
	runner := newJobRunner(jobRunnerOpts{
		Queue:        queue,
		PollInterval: time.Millisecond,
		Extend:       5 * time.Millisecond,
	})
	return sqlDB, queue, runner
}

func runOneQueuedJobAttempt(t *testing.T, runner *jobRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	runner.receiveAndRun(ctx, &wg)
	wg.Wait()
}

func queuedMessageCount(t *testing.T, sqlDB *sql.DB) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRowContext(context.Background(), "select count(*) from goqite").Scan(&count); err != nil {
		t.Fatalf("count queued messages: %v", err)
	}
	return count
}

func TestJobRunnerHandlerErrorRetriesByDefault(t *testing.T) {
	sqlDB, queue, runner := newJobRunnerErrorPolicyTestHarness(t)
	attempts := 0
	runner.Register("retry-job", func(context.Context, []byte) error {
		attempts++
		return errors.New("retry me")
	})
	if err := createJobMessage(context.Background(), queue, "retry-job", nil); err != nil {
		t.Fatalf("enqueue retry job: %v", err)
	}

	runOneQueuedJobAttempt(t, runner)
	if attempts != 1 || queuedMessageCount(t, sqlDB) != 1 {
		t.Fatalf("after first attempt: attempts=%d queued=%d", attempts, queuedMessageCount(t, sqlDB))
	}

	time.Sleep(10 * time.Millisecond)
	runOneQueuedJobAttempt(t, runner)
	if attempts != 2 || queuedMessageCount(t, sqlDB) != 1 {
		t.Fatalf("default policy did not retry: attempts=%d queued=%d", attempts, queuedMessageCount(t, sqlDB))
	}
}

func TestJobRunnerLogsHumanReadableDuration(t *testing.T) {
	_, queue, runner := newJobRunnerErrorPolicyTestHarness(t)
	log := &recordingJobLogger{}
	runner.log = log
	runner.Register("duration-job", func(context.Context, []byte) error { return nil })
	if err := createJobMessage(context.Background(), queue, "duration-job", nil); err != nil {
		t.Fatalf("enqueue duration job: %v", err)
	}

	runOneQueuedJobAttempt(t, runner)

	for _, entry := range log.snapshot() {
		if entry.message != "Ran job" {
			continue
		}
		for i := 0; i+1 < len(entry.args); i += 2 {
			if entry.args[i] != "duration" {
				continue
			}
			duration, ok := entry.args[i+1].(string)
			if !ok {
				t.Fatalf("duration type = %T, want string", entry.args[i+1])
			}
			if _, err := time.ParseDuration(duration); err != nil {
				t.Fatalf("duration %q is not human-readable: %v", duration, err)
			}
			return
		}
	}

	t.Fatal("Ran job log did not contain a duration")
}

func TestJobRunnerHandlerErrorConsumeDeletesMessage(t *testing.T) {
	sqlDB, queue, runner := newJobRunnerErrorPolicyTestHarness(t)
	attempts := 0
	runner.RegisterWithPolicy("one-attempt-job", QueueHandlerErrorConsume, func(context.Context, []byte) error {
		attempts++
		return errors.New("recorded operational failure")
	})
	if err := createJobMessage(context.Background(), queue, "one-attempt-job", nil); err != nil {
		t.Fatalf("enqueue one-attempt job: %v", err)
	}

	runOneQueuedJobAttempt(t, runner)
	if attempts != 1 {
		t.Fatalf("attempts=%d, want 1", attempts)
	}
	if queued := queuedMessageCount(t, sqlDB); queued != 0 {
		t.Fatalf("consumed failed message remained queued: %d", queued)
	}

	time.Sleep(10 * time.Millisecond)
	runOneQueuedJobAttempt(t, runner)
	if attempts != 1 {
		t.Fatalf("consumed failed message was retried: attempts=%d", attempts)
	}
}

func TestJobRunnerHandlerPanicRetriesByDefault(t *testing.T) {
	sqlDB, queue, runner := newJobRunnerErrorPolicyTestHarness(t)
	attempts := 0
	runner.Register("retry-panic-job", func(context.Context, []byte) error {
		attempts++
		panic("retry panic")
	})
	if err := createJobMessage(context.Background(), queue, "retry-panic-job", nil); err != nil {
		t.Fatalf("enqueue retry panic job: %v", err)
	}

	runOneQueuedJobAttempt(t, runner)
	if attempts != 1 || queuedMessageCount(t, sqlDB) != 1 {
		t.Fatalf("after first panic: attempts=%d queued=%d", attempts, queuedMessageCount(t, sqlDB))
	}

	time.Sleep(10 * time.Millisecond)
	runOneQueuedJobAttempt(t, runner)
	if attempts != 2 || queuedMessageCount(t, sqlDB) != 1 {
		t.Fatalf("default panic policy did not retry: attempts=%d queued=%d", attempts, queuedMessageCount(t, sqlDB))
	}
}

func TestJobRunnerHandlerPanicConsumeDeletesMessage(t *testing.T) {
	sqlDB, queue, runner := newJobRunnerErrorPolicyTestHarness(t)
	attempts := 0
	runner.RegisterWithPolicy("consume-panic-job", QueueHandlerErrorConsume, func(context.Context, []byte) error {
		attempts++
		panic("consume panic")
	})
	if err := createJobMessage(context.Background(), queue, "consume-panic-job", nil); err != nil {
		t.Fatalf("enqueue consume panic job: %v", err)
	}

	runOneQueuedJobAttempt(t, runner)
	if attempts != 1 {
		t.Fatalf("attempts=%d, want 1", attempts)
	}
	if queued := queuedMessageCount(t, sqlDB); queued != 0 {
		t.Fatalf("consumed panicked message remained queued: %d", queued)
	}

	time.Sleep(10 * time.Millisecond)
	runOneQueuedJobAttempt(t, runner)
	if attempts != 1 {
		t.Fatalf("consumed panicked message was retried: attempts=%d", attempts)
	}
}

func TestJobRunnerConsumesCorruptGobMessage(t *testing.T) {
	sqlDB, queue, runner := newJobRunnerErrorPolicyTestHarness(t)
	if err := queue.Send(context.Background(), goqite.Message{Body: []byte("not-a-gob-message")}); err != nil {
		t.Fatalf("enqueue corrupt message: %v", err)
	}

	runOneQueuedJobAttempt(t, runner)
	if queued := queuedMessageCount(t, sqlDB); queued != 0 {
		t.Fatalf("corrupt message remained queued: %d", queued)
	}
}

func TestJobRunnerConsumesUnregisteredJobMessage(t *testing.T) {
	sqlDB, queue, runner := newJobRunnerErrorPolicyTestHarness(t)
	if err := createJobMessage(context.Background(), queue, "removed-job-name", nil); err != nil {
		t.Fatalf("enqueue unregistered job message: %v", err)
	}

	runOneQueuedJobAttempt(t, runner)
	if queued := queuedMessageCount(t, sqlDB); queued != 0 {
		t.Fatalf("unregistered job message remained queued: %d", queued)
	}
}

func TestJobRunnerPoisonDeleteFailureLeavesMessageForRetry(t *testing.T) {
	sqlDB, queue, runner := newJobRunnerErrorPolicyTestHarness(t)
	if _, err := sqlDB.Exec(`
		create trigger reject_goqite_delete
		before delete on goqite
		begin
			select raise(abort, 'delete blocked');
		end;
	`); err != nil {
		t.Fatalf("create delete failure trigger: %v", err)
	}
	if err := queue.Send(context.Background(), goqite.Message{Body: []byte("not-a-gob-message")}); err != nil {
		t.Fatalf("enqueue corrupt message: %v", err)
	}

	runOneQueuedJobAttempt(t, runner)
	if queued := queuedMessageCount(t, sqlDB); queued != 1 {
		t.Fatalf("poison message lost after delete failure: queued=%d", queued)
	}
	if _, err := sqlDB.Exec("drop trigger reject_goqite_delete"); err != nil {
		t.Fatalf("drop delete failure trigger: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	runOneQueuedJobAttempt(t, runner)
	if queued := queuedMessageCount(t, sqlDB); queued != 0 {
		t.Fatalf("poison message was not consumed after delete recovered: queued=%d", queued)
	}
}

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
