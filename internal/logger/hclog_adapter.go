// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package logger

import (
	"fmt"
	"io"
	stdlog "log"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/rs/zerolog"
)

type zerologWriter struct {
	l     zerolog.Logger
	level zerolog.Level
}

func (w zerologWriter) Write(p []byte) (int, error) {
	msg := string(p)
	switch w.level {
	case zerolog.DebugLevel:
		w.l.Debug().Msg(msg)
	case zerolog.WarnLevel:
		w.l.Warn().Msg(msg)
	case zerolog.ErrorLevel:
		w.l.Error().Msg(msg)
	default:
		w.l.Info().Msg(msg)
	}
	return len(p), nil
}

func StandardWriterAdapter(zl zerolog.Logger) io.Writer {
	return zerologWriter{l: zl, level: zerolog.InfoLevel}
}

type ZerologHCLog struct {
	zl          zerolog.Logger
	name        string
	level       hclog.Level
	impliedArgs []any
}

type raftLogLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	entries  map[string]raftLogEntry
}

type raftLogEntry struct {
	last       time.Time
	suppressed int
}

func newRaftLogLimiter(interval time.Duration) *raftLogLimiter {
	return &raftLogLimiter{
		interval: interval,
		entries:  make(map[string]raftLogEntry),
	}
}

func (r *raftLogLimiter) allow(key string, now time.Time) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.entries[key]
	if entry.last.IsZero() || now.Sub(entry.last) >= r.interval {
		suppressed := entry.suppressed
		entry.last = now
		entry.suppressed = 0
		r.entries[key] = entry
		return true, suppressed
	}

	entry.suppressed++
	r.entries[key] = entry
	return false, 0
}

var raftRequestVoteLimiter = newRaftLogLimiter(30 * time.Second)

func NewZerologHCLog(zl zerolog.Logger, name string) hclog.Logger {
	return &ZerologHCLog{
		zl:    zl,
		name:  name,
		level: hclog.Info,
	}
}

func (l *ZerologHCLog) Log(level hclog.Level, msg string, args ...any) {
	if !l.accept(level) {
		return
	}
	allArgs := append(l.impliedArgs, args...)
	fields := kvsToMap(allArgs...)
	if l.shouldRateLimitRaftError(level, msg, fields) {
		target := fmt.Sprint(fields["target"])
		errText := fmt.Sprint(fields["error"])
		key := fmt.Sprintf("%s|%s", target, errText)
		allow, suppressed := raftRequestVoteLimiter.allow(key, time.Now())
		if !allow {
			return
		}
		if suppressed > 0 {
			fields["suppressed_repeats"] = suppressed
			fields["suppression_window"] = "30s"
		}
	}
	ev := l.baseEvent(level)
	ev.Fields(fields).Msg(msg)
}

func (l *ZerologHCLog) Trace(msg string, args ...any) { l.Log(hclog.Trace, msg, args...) }
func (l *ZerologHCLog) Debug(msg string, args ...any) { l.Log(hclog.Debug, msg, args...) }
func (l *ZerologHCLog) Info(msg string, args ...any)  { l.Log(hclog.Info, msg, args...) }
func (l *ZerologHCLog) Warn(msg string, args ...any)  { l.Log(hclog.Warn, msg, args...) }
func (l *ZerologHCLog) Error(msg string, args ...any) { l.Log(hclog.Error, msg, args...) }

func (l *ZerologHCLog) IsTrace() bool { return l.level <= hclog.Trace }
func (l *ZerologHCLog) IsDebug() bool { return l.level <= hclog.Debug }
func (l *ZerologHCLog) IsInfo() bool  { return l.level <= hclog.Info }
func (l *ZerologHCLog) IsWarn() bool  { return l.level <= hclog.Warn }
func (l *ZerologHCLog) IsError() bool { return l.level <= hclog.Error }

func (l *ZerologHCLog) With(args ...any) hclog.Logger {
	n := *l
	n.zl = l.zl.With().Fields(kvsToMap(args...)).Logger()
	n.impliedArgs = append(append([]any{}, l.impliedArgs...), args...)
	return &n
}

func (l *ZerologHCLog) Named(name string) hclog.Logger {
	n := *l
	if l.name != "" {
		n.name = l.name + "." + name
	} else {
		n.name = name
	}
	return &n
}

func (l *ZerologHCLog) ResetNamed(name string) hclog.Logger {
	n := *l
	n.name = name
	return &n
}

func (l *ZerologHCLog) Name() string               { return l.name }
func (l *ZerologHCLog) SetLevel(level hclog.Level) { l.level = level }
func (l *ZerologHCLog) GetLevel() hclog.Level      { return l.level }

func (l *ZerologHCLog) StandardLogger(opts *hclog.StandardLoggerOptions) *stdlog.Logger {
	return stdlog.New(l.StandardWriter(opts), "", 0)
}

func (l *ZerologHCLog) StandardWriter(opts *hclog.StandardLoggerOptions) io.Writer {
	lev := zerolog.InfoLevel
	if opts != nil {
		switch opts.ForceLevel {
		case hclog.Trace, hclog.Debug:
			lev = zerolog.DebugLevel
		case hclog.Warn:
			lev = zerolog.WarnLevel
		case hclog.Error:
			lev = zerolog.ErrorLevel
		}
	}
	return zerologWriter{l: l.zl.With().Str("subsystem", l.name).Logger(), level: lev}
}

func (l *ZerologHCLog) ImpliedArgs() []any {
	return append([]any{}, l.impliedArgs...)
}

func (l *ZerologHCLog) accept(level hclog.Level) bool { return level >= l.level }
func (l *ZerologHCLog) baseEvent(level hclog.Level) *zerolog.Event {
	logger := l.zl.With().Str("subsystem", l.name).Logger()
	switch level {
	case hclog.Trace, hclog.Debug:
		return logger.Debug()
	case hclog.Info:
		return logger.Info()
	case hclog.Warn:
		return logger.Warn()
	case hclog.Error:
		return logger.Error()
	default:
		return logger.Info()
	}
}

func (l *ZerologHCLog) shouldRateLimitRaftError(level hclog.Level, msg string, fields map[string]any) bool {
	if level != hclog.Error || l.name != "raft" {
		return false
	}
	if !strings.Contains(msg, "failed to make requestVote RPC") {
		return false
	}
	errText, ok := fields["error"]
	if !ok {
		return false
	}
	return strings.Contains(fmt.Sprint(errText), "connect: connection refused")
}

func kvsToMap(kvs ...any) map[string]any {
	m := make(map[string]any, len(kvs)/2)
	for i := 0; i < len(kvs); i += 2 {
		if i+1 >= len(kvs) {
			m["arg"] = kvs[i]
			break
		}

		key, ok := kvs[i].(string)
		if !ok || key == "" {
			key = "arg"
			for {
				if _, exists := m[key]; !exists {
					break
				}
				key += "_"
			}
		}
		m[key] = kvs[i+1]
	}
	return m
}
