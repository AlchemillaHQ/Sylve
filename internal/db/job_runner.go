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
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"maragu.dev/goqite"
)

type jobRunnerOpts struct {
	Extend       time.Duration
	Limit        int
	Log          jobLogger
	Name         string
	PollInterval time.Duration
	Queue        *goqite.Queue
}

type jobRunner struct {
	extend        time.Duration
	jobCount      int
	jobCountLimit int
	jobCountLock  sync.RWMutex
	jobs          map[string]registeredJob
	log           jobLogger
	name          string
	pollInterval  time.Duration
	queue         *goqite.Queue
}

type jobFunc func(ctx context.Context, m []byte) error

type registeredJob struct {
	fn                 jobFunc
	handlerErrorPolicy QueueHandlerErrorPolicy
}

type jobLogger interface {
	Info(msg string, args ...any)
}

type queueJobMessage struct {
	Name    string
	Message []byte
}

type discardJobLogger struct{}

func (discardJobLogger) Info(string, ...any) {}

func newJobRunner(opts jobRunnerOpts) *jobRunner {
	if opts.Log == nil {
		opts.Log = discardJobLogger{}
	}
	if opts.Limit == 0 {
		opts.Limit = runtime.GOMAXPROCS(0)
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 100 * time.Millisecond
	}
	if opts.Extend == 0 {
		opts.Extend = 5 * time.Second
	}

	return &jobRunner{
		extend:        opts.Extend,
		jobCountLimit: opts.Limit,
		jobs:          make(map[string]registeredJob),
		log:           opts.Log,
		name:          opts.Name,
		pollInterval:  opts.PollInterval,
		queue:         opts.Queue,
	}
}

func (r *jobRunner) Register(name string, job jobFunc) {
	r.RegisterWithPolicy(name, QueueHandlerErrorRetry, job)
}

func (r *jobRunner) RegisterWithPolicy(name string, policy QueueHandlerErrorPolicy, job jobFunc) {
	if _, ok := r.jobs[name]; ok {
		panic(fmt.Sprintf(`job "%v" already registered`, name))
	}
	if policy != QueueHandlerErrorRetry && policy != QueueHandlerErrorConsume {
		panic(fmt.Sprintf(`job "%v" has invalid handler error policy %d`, name, policy))
	}
	r.jobs[name] = registeredJob{fn: job, handlerErrorPolicy: policy}
}

func (r *jobRunner) Start(ctx context.Context) {
	var names []string
	for k := range r.jobs {
		names = append(names, k)
	}
	sort.Strings(names)

	r.log.Info("Starting job runner", "name", r.name, "jobs", names)

	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			r.log.Info("Stopping job runner", "name", r.name)
			wg.Wait()
			r.log.Info("Stopped job runner", "name", r.name)
			return
		default:
			r.receiveAndRun(ctx, &wg)
		}
	}
}

func (r *jobRunner) receiveAndRun(ctx context.Context, wg *sync.WaitGroup) {
	r.jobCountLock.RLock()
	if r.jobCount == r.jobCountLimit {
		r.jobCountLock.RUnlock()
		time.Sleep(r.pollInterval)
		return
	}
	r.jobCountLock.RUnlock()

	m, err := r.queue.ReceiveAndWait(ctx, r.pollInterval)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return
		}
		r.log.Info("Error receiving job", "error", err)
		time.Sleep(time.Second)
		return
	}
	if m == nil {
		return
	}

	var qm queueJobMessage
	if err := gob.NewDecoder(bytes.NewReader(m.Body)).Decode(&qm); err != nil {
		r.log.Info("Error decoding job message body", "error", err)
		if deleteErr := r.deleteMessage(m.ID); deleteErr != nil {
			r.log.Info(
				"Error deleting corrupt job message from queue, it will be retried",
				"id", m.ID,
				"error", deleteErr,
			)
		}
		return
	}

	job, ok := r.jobs[qm.Name]
	if !ok {
		r.log.Info("Discarding job with unregistered name", "name", qm.Name, "id", m.ID)
		if deleteErr := r.deleteMessage(m.ID); deleteErr != nil {
			r.log.Info(
				"Error deleting unregistered job message from queue, it will be retried",
				"name", qm.Name,
				"id", m.ID,
				"error", deleteErr,
			)
		}
		return
	}

	r.jobCountLock.Lock()
	r.jobCount++
	r.jobCountLock.Unlock()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			r.jobCountLock.Lock()
			r.jobCount--
			r.jobCountLock.Unlock()
		}()
		defer func() {
			if rec := recover(); rec != nil {
				r.log.Info("Recovered from panic in job", "name", qm.Name, "id", m.ID, "error", rec)
				if job.handlerErrorPolicy == QueueHandlerErrorConsume {
					if deleteErr := r.deleteMessage(m.ID); deleteErr != nil {
						r.log.Info(
							"Error deleting consumed panicked job from queue, it will be retried",
							"name", qm.Name,
							"id", m.ID,
							"error", deleteErr,
						)
					}
				}
			}
		}()

		jobCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			time.Sleep(r.extend - r.extend/5)
			for {
				select {
				case <-jobCtx.Done():
					return
				default:
					r.log.Info("Extending message timeout", "name", qm.Name, "id", m.ID)
					if err := r.queue.Extend(jobCtx, m.ID, r.extend); err != nil {
						r.log.Info("Error extending message timeout", "name", qm.Name, "id", m.ID, "error", err)
					}
					time.Sleep(r.extend - r.extend/5)
				}
			}
		}()

		r.log.Info("Running job", "name", qm.Name, "id", m.ID)
		before := time.Now()
		if err := job.fn(jobCtx, qm.Message); err != nil {
			r.log.Info("Error running job", "name", qm.Name, "id", m.ID, "error", err)
			if job.handlerErrorPolicy == QueueHandlerErrorConsume {
				if deleteErr := r.deleteMessage(m.ID); deleteErr != nil {
					r.log.Info(
						"Error deleting consumed failed job from queue, it will be retried",
						"name", qm.Name,
						"id", m.ID,
						"error", deleteErr,
					)
				}
			}
			return
		}
		duration := time.Since(before)
		r.log.Info("Ran job", "name", qm.Name, "id", m.ID, "duration", duration.String())

		if err := r.deleteMessage(m.ID); err != nil {
			r.log.Info("Error deleting job from queue, it will be retried", "name", qm.Name, "id", m.ID, "error", err)
		}
	}()
}

func (r *jobRunner) deleteMessage(id goqite.ID) error {
	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), time.Second)
	defer deleteCancel()
	return r.queue.Delete(deleteCtx, id)
}

func createJobMessage(ctx context.Context, q *goqite.Queue, name string, m []byte) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(queueJobMessage{Name: name, Message: m}); err != nil {
		return err
	}
	return q.Send(ctx, goqite.Message{Body: buf.Bytes()})
}
