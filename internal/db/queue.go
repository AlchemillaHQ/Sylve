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
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"maragu.dev/goqite"
)

const queueSchema = `
create table if not exists goqite (
  id text primary key default ('m_' || lower(hex(randomblob(16)))),
  created text not null default (strftime('%Y-%m-%dT%H:%M:%fZ')),
  updated text not null default (strftime('%Y-%m-%dT%H:%M:%fZ')),
  queue text not null,
  body blob not null,
  timeout text not null default (strftime('%Y-%m-%dT%H:%M:%fZ')),
  received integer not null default 0,
  priority integer not null default 0
) strict;

create trigger if not exists goqite_updated_timestamp after update on goqite begin
  update goqite set updated = strftime('%Y-%m-%dT%H:%M:%fZ') where id = old.id;
end;

create index if not exists goqite_queue_priority_created_idx
  on goqite (queue, priority desc, created);
`

var (
	dbConn       *sql.DB
	laneQueues   map[string]*goqite.Queue
	laneRunners  map[string]*jobRunner
	registered   map[string]struct{}
	setupQueueMu sync.RWMutex
)

type QueueHandler[T any] func(ctx context.Context, payload T) error

const (
	queueLaneLifecycleID   = "lifecycle"
	queueLaneDownloadsID   = "downloads"
	queueLaneZeltaID       = "zelta"
	queueLaneMaintenanceID = "maintenance"
	queueLaneDefaultID     = "default"
	queueLaneLegacyID      = "legacy"
)

type queueLaneConfig struct {
	LaneID    string
	QueueName string
	Limit     int
}

func queueLaneConfigs() []queueLaneConfig {
	return []queueLaneConfig{
		{LaneID: queueLaneLifecycleID, QueueName: "jobs-lifecycle", Limit: 8},
		{LaneID: queueLaneDownloadsID, QueueName: "jobs-downloads", Limit: 8},
		{LaneID: queueLaneZeltaID, QueueName: "jobs-zelta", Limit: 8},
		{LaneID: queueLaneMaintenanceID, QueueName: "jobs-maintenance", Limit: 4},
		{LaneID: queueLaneDefaultID, QueueName: "jobs-default", Limit: 2},
		// Keep consuming the previous single-lane queue so upgrades do not strand pending jobs.
		{LaneID: queueLaneLegacyID, QueueName: "jobs", Limit: 2},
	}
}

func resolveQueueLane(jobName string) string {
	name := strings.TrimSpace(strings.ToLower(jobName))

	switch {
	case strings.HasPrefix(name, "guest-lifecycle-"), strings.HasPrefix(name, "guest-autostart-"),
		strings.HasPrefix(name, "utils-wol-"):
		return queueLaneLifecycleID
	case strings.HasPrefix(name, "utils-download-"):
		return queueLaneDownloadsID
	case strings.HasPrefix(name, "zelta-"):
		return queueLaneZeltaID
	case strings.HasPrefix(name, "zfs_"):
		return queueLaneMaintenanceID
	default:
		return queueLaneDefaultID
	}
}

func SetupQueue(cfg *internal.SylveConfig, isTest bool, log zerolog.Logger) error {
	dbPath := filepath.Join(cfg.DataPath, "sylve_queue.db")
	d, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_timeout=5000&_fk=true")
	if err != nil {
		return err
	}

	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)

	if _, err := d.Exec(queueSchema); err != nil {
		return err
	}

	dbConn = d

	setupQueueMu.Lock()
	defer setupQueueMu.Unlock()

	laneQueues = make(map[string]*goqite.Queue, len(queueLaneConfigs()))
	laneRunners = make(map[string]*jobRunner, len(queueLaneConfigs()))
	registered = make(map[string]struct{})

	for _, lane := range queueLaneConfigs() {
		q := goqite.New(goqite.NewOpts{
			DB:   dbConn,
			Name: lane.QueueName,
		})

		laneQueues[lane.LaneID] = q
		laneRunners[lane.LaneID] = newJobRunner(jobRunnerOpts{
			Limit:        lane.Limit,
			Log:          zerologJobLogger{Logger: log},
			PollInterval: 2 * time.Second,
			Queue:        q,
		})
	}

	return nil
}

func StartQueue(ctx context.Context) {
	setupQueueMu.RLock()
	defer setupQueueMu.RUnlock()

	if len(laneRunners) == 0 {
		panic("StartQueue called before SetupQueue")
	}

	startOrder := []string{
		queueLaneLifecycleID,
		queueLaneDownloadsID,
		queueLaneZeltaID,
		queueLaneMaintenanceID,
		queueLaneDefaultID,
		queueLaneLegacyID,
	}

	var wg sync.WaitGroup
	for _, lane := range startOrder {
		runner, ok := laneRunners[lane]
		if !ok || runner == nil {
			continue
		}

		wg.Add(1)
		go func(r *jobRunner) {
			defer wg.Done()
			r.Start(ctx)
		}(runner)
	}

	wg.Wait()
}

func QueueRegister(name string, fn func(ctx context.Context, body []byte) error) {
	setupQueueMu.Lock()
	defer setupQueueMu.Unlock()

	if len(laneRunners) == 0 {
		panic("queue.Register called before SetupQueue")
	}
	if _, exists := registered[name]; exists {
		panic(fmt.Sprintf(`job "%v" already registered`, name))
	}

	laneID := resolveQueueLane(name)
	runner, ok := laneRunners[laneID]
	if !ok || runner == nil {
		runner = laneRunners[queueLaneDefaultID]
	}
	if runner == nil {
		panic("default queue lane is not available")
	}

	runner.Register(name, fn)

	// Register every current job in the legacy lane too, so pending pre-lane messages still drain.
	if laneID != queueLaneLegacyID {
		if legacyRunner, ok := laneRunners[queueLaneLegacyID]; ok && legacyRunner != nil {
			legacyRunner.Register(name, fn)
		}
	}

	registered[name] = struct{}{}
}

func QueueRegisterJSON[T any](name string, h QueueHandler[T]) {
	QueueRegister(name, func(ctx context.Context, body []byte) error {
		var v T
		if err := json.Unmarshal(body, &v); err != nil {
			return err
		}
		return h(ctx, v)
	})
}

func QueueRegisterNoPayload(name string, fn func(ctx context.Context) error) {
	QueueRegister(name, func(ctx context.Context, _ []byte) error {
		return fn(ctx)
	})
}

func Enqueue(ctx context.Context, name string, body []byte) error {
	setupQueueMu.RLock()
	defer setupQueueMu.RUnlock()

	if len(laneQueues) == 0 {
		panic("queue.Enqueue called before SetupQueue")
	}

	laneID := resolveQueueLane(name)
	q, ok := laneQueues[laneID]
	if !ok || q == nil {
		q = laneQueues[queueLaneDefaultID]
	}
	if q == nil {
		return fmt.Errorf("queue_lane_not_available")
	}

	return createJobMessage(ctx, q, name, body)
}

func EnqueueJSON[T any](ctx context.Context, name string, payload T) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return Enqueue(ctx, name, b)
}

func EnqueueNoPayload(ctx context.Context, name string) error {
	return Enqueue(ctx, name, nil)
}
