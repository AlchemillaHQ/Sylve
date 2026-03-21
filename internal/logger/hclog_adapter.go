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
	maxAge   time.Duration

	cleanupEvery time.Duration
	nextCleanup  time.Time
}

type raftLogEntry struct {
	last       time.Time
	suppressed int
}

func newRaftLogLimiter(interval time.Duration) *raftLogLimiter {
	return &raftLogLimiter{
		interval:     interval,
		entries:      make(map[string]raftLogEntry),
		maxAge:       interval * 10,
		cleanupEvery: interval,
	}
}

func (r *raftLogLimiter) allow(key string, now time.Time) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.nextCleanup.IsZero() || !now.Before(r.nextCleanup) {
		for k, v := range r.entries {
			if !v.last.IsZero() && now.Sub(v.last) >= r.maxAge {
				delete(r.entries, k)
			}
		}
		r.nextCleanup = now.Add(r.cleanupEvery)
	}

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

func raftConnErrorCategory(errText string) string {
	switch {
	case strings.Contains(errText, "connection refused"):
		return "connection_refused"
	case strings.Contains(errText, "host is down"):
		return "host_is_down"
	case strings.Contains(errText, "no route to host"):
		return "no_route_to_host"
	case strings.Contains(errText, "network is unreachable"):
		return "network_unreachable"
	case strings.Contains(errText, "i/o timeout"):
		return "io_timeout"
	default:
		return "other"
	}
}

func shouldRateLimitRaftConnError(msg, errText string) bool {
	if msg == "" || errText == "" {
		return false
	}

	isNoisyRPC := strings.Contains(msg, "failed to make requestVote RPC") ||
		strings.Contains(msg, "failed to heartbeat to") ||
		strings.Contains(msg, "failed to appendEntries to")

	if !isNoisyRPC {
		return false
	}

	isConnError := strings.Contains(errText, "connection refused") ||
		strings.Contains(errText, "host is down") ||
		strings.Contains(errText, "no route to host") ||
		strings.Contains(errText, "network is unreachable") ||
		strings.Contains(errText, "i/o timeout")

	return isConnError
}

func NewZerologHCLog(zl zerolog.Logger, name string) hclog.Logger {
	return &ZerologHCLog{
		zl:    zl.With().Str("subsystem", name).Logger(),
		name:  name,
		level: hclog.Info,
	}
}

func (l *ZerologHCLog) Log(level hclog.Level, msg string, args ...any) {
	if !l.accept(level) {
		return
	}

	var fields map[string]any

	if l.name == "raft" && level == hclog.Error {
		fields = kvsToMap(append(l.impliedArgs, args...)...)
		target := fmt.Sprint(fields["target"])
		if target == "<nil>" || target == "" {
			target = fmt.Sprint(fields["peer"])
		}
		errText := fmt.Sprint(fields["error"])

		if shouldRateLimitRaftConnError(msg, errText) {
			key := target + "|" + raftConnErrorCategory(errText)
			allow, suppressed := raftRequestVoteLimiter.allow(key, time.Now())
			if !allow {
				return
			}
			if suppressed > 0 {
				fields["suppressed_repeats"] = suppressed
				fields["suppression_window"] = "30s"
			}
		}
	}

	ev := l.baseEvent(level)

	if fields == nil && (len(l.impliedArgs) > 0 || len(args) > 0) {
		fields = kvsToMap(append(l.impliedArgs, args...)...)
	}

	if fields != nil {
		ev.Fields(fields)
	}
	ev.Msg(msg)
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

	n.zl = l.zl.With().Str("subsystem", n.name).Logger()
	return &n
}

func (l *ZerologHCLog) ResetNamed(name string) hclog.Logger {
	n := *l
	n.name = name
	n.zl = l.zl.With().Str("subsystem", n.name).Logger()
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

	return zerologWriter{l: l.zl, level: lev}
}

func (l *ZerologHCLog) ImpliedArgs() []any {
	return append([]any{}, l.impliedArgs...)
}

func (l *ZerologHCLog) accept(level hclog.Level) bool { return level >= l.level }

func (l *ZerologHCLog) baseEvent(level hclog.Level) *zerolog.Event {
	switch level {
	case hclog.Trace, hclog.Debug:
		return l.zl.Debug()
	case hclog.Info:
		return l.zl.Info()
	case hclog.Warn:
		return l.zl.Warn()
	case hclog.Error:
		return l.zl.Error()
	default:
		return l.zl.Info()
	}
}

func kvsToMap(kvs ...any) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
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
