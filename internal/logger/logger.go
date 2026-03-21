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
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/natefinch/lumberjack"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	L        zerolog.Logger
	logCache sync.Map
)

func InitLogger(dataDir string, level int8) {
	zerolog.TimeFieldFormat = "2006/01/02 15:04:05"

	switch level {
	case 0:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case 1:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case 2:
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case 3:
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case 4:
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case 5:
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "2006/01/02 15:04:05",
		NoColor:    !isatty.IsTerminal(os.Stderr.Fd()),
	}

	fileWriter := &lumberjack.Logger{
		Filename:   filepath.Join(dataDir, "logs.json"),
		MaxSize:    10,
		MaxBackups: 5,
		MaxAge:     28,
		Compress:   true,
	}

	multiWriter := zerolog.MultiLevelWriter(consoleWriter, fileWriter)

	L = zerolog.New(multiWriter).
		With().
		Timestamp().
		Caller().
		Logger()

	log.Logger = L

	fmt.Println("")
	L.Info().Msg("Logger initialized")
}

func LogWithDeduplication(level zerolog.Level, message string) {
	const dedupTime = 60 * time.Second

	now := time.Now()
	lastTime, exists := logCache.Load(message)

	if exists {
		if t, ok := lastTime.(time.Time); ok && now.Sub(t) < dedupTime {
			return
		}
	}

	logCache.Store(message, now)

	switch level {
	case zerolog.DebugLevel:
		L.Debug().Msg(message)
	case zerolog.InfoLevel:
		L.Info().Msg(message)
	case zerolog.WarnLevel:
		L.Warn().Msg(message)
	case zerolog.ErrorLevel:
		L.Error().Msg(message)
	case zerolog.FatalLevel:
		L.Fatal().Msg(message)
	case zerolog.PanicLevel:
		L.Panic().Msg(message)
	default:
		L.Info().Msg(message)
	}
}

func BootstrapFatal(msg string) {
	w := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "2006/01/02 15:04:05"}
	l := zerolog.New(w).With().Timestamp().Logger()
	l.Fatal().Msg(msg)
}
