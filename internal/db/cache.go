// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"fmt"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/dgraph-io/badger/v4"
	_ "github.com/mattn/go-sqlite3"
)

var CacheDB *badger.DB

func SetupCache(cfg *internal.SylveConfig) *badger.DB {
	opts := badger.DefaultOptions(fmt.Sprintf("%s/cache.db", cfg.DataPath)).
		WithLoggingLevel(badger.ERROR).
		WithDetectConflicts(false)

	db, err := badger.Open(opts)
	if err != nil {
		logger.L.Fatal().Msgf("badger open failed: %v", err)
	}

	CacheDB = db
	return db
}

func SetValue(key string, value []byte, ttlSeconds int64) error {
	e := badger.NewEntry([]byte(key), value).
		WithTTL(time.Duration(ttlSeconds) * time.Second)

	return CacheDB.Update(func(txn *badger.Txn) error {
		return txn.SetEntry(e)
	})
}

func GetValue(key string) ([]byte, bool) {
	var valCopy []byte

	err := CacheDB.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		valCopy, err = item.ValueCopy(nil)
		return err
	})

	if err != nil {
		return nil, false
	}

	return valCopy, true
}

func RunCacheGC() {
	for CacheDB.RunValueLogGC(0.5) == nil {
		logger.L.Info().Msg("Successfully ran value log GC on cache DB")
	}
}
