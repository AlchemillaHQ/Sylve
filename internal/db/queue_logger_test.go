// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
)

func TestQueueLoggerSchedulerTickLevels(t *testing.T) {
	for _, test := range []struct {
		message, job string
		level        zerolog.Level
	}{
		{"Running job", "disk-smart-scheduler-tick", zerolog.TraceLevel},
		{"Ran job", "disk-smart-scheduler-tick", zerolog.TraceLevel},
		{"Running job", "zfs_scrub_scheduler_tick", zerolog.TraceLevel},
		{"Ran job", "zfs_scrub_scheduler_tick", zerolog.TraceLevel},
		{"Error running job", "disk-smart-scheduler-tick", zerolog.DebugLevel},
		{"Error running job", "zfs_scrub_scheduler_tick", zerolog.DebugLevel},
		{"Ran job", "zfs_scrub_event_deliver", zerolog.DebugLevel},
		{"Running job", "other-job", zerolog.DebugLevel},
		{"Ran job", "other-job", zerolog.DebugLevel},
	} {
		for _, minimum := range []zerolog.Level{zerolog.TraceLevel, zerolog.DebugLevel} {
			t.Run(test.job+"/"+test.message+"/"+minimum.String(), func(t *testing.T) {
				var output bytes.Buffer
				log := zerologJobLogger{Logger: zerolog.New(&output).Level(minimum)}
				log.Info(test.message, "id", "job-id", "name", test.job, "duration", "4ms")
				if test.level < minimum {
					if output.Len() != 0 {
						t.Fatalf("trace tick was visible at debug level: %s", output.String())
					}
					return
				}
				var entry struct {
					Level, Message, Name, ID, Duration string
				}
				if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
					t.Fatal(err)
				}
				if entry.Level != test.level.String() || entry.Message != test.message || entry.Name != test.job || entry.ID != "job-id" || entry.Duration != "4ms" {
					t.Fatalf("unexpected log level or fields: %+v", entry)
				}
			})
		}
	}
}
