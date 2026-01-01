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
	"path/filepath"
	"time"

	"github.com/alchemillahq/sylve/internal"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"maragu.dev/goqite"
	"maragu.dev/goqite/jobs"
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
	dbConn *sql.DB
	q      *goqite.Queue
	r      *jobs.Runner
)

type QueueHandler[T any] func(ctx context.Context, payload T) error

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

	q = goqite.New(goqite.NewOpts{
		DB:   dbConn,
		Name: "jobs",
	})

	r = jobs.NewRunner(jobs.NewRunnerOpts{
		Limit:        32,
		Log:          zerologJobLogger{Logger: log},
		PollInterval: 2 * time.Second,
		Queue:        q,
	})

	return nil
}

func StartQueue(ctx context.Context) {
	if r == nil {
		panic("StartQueue called before SetupQueue")
	}
	r.Start(ctx)
}

func QueueRegister(name string, fn func(ctx context.Context, body []byte) error) {
	if r == nil {
		panic("queue.Register called before SetupQueue")
	}
	r.Register(name, fn)
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
	if q == nil {
		panic("queue.Enqueue called before SetupQueue")
	}
	return jobs.Create(ctx, q, name, body)
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
