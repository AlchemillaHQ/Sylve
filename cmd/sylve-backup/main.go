// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/replication"
	sysU "github.com/alchemillahq/sylve/pkg/system"
	"gorm.io/gorm"
)

const defaultBackupConfigPath = "./backup.config.json"

func main() {
	cmd.AsciiArt()

	command, args := parseCommand(os.Args[1:])

	switch command {
	case "serve":
		if !sysU.IsRoot() {
			logger.BootstrapFatal("Root privileges required for backup target mode")
		}
		runServe(args)
	case "datasets":
		runDatasets(args)
	case "status":
		runStatus(args)
	case "pull":
		if !sysU.IsRoot() {
			logger.BootstrapFatal("Root privileges required for pull mode")
		}
		runPull(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func parseCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "serve", args
	}

	first := args[0]
	if strings.HasPrefix(first, "-") {
		return "serve", args
	}

	return first, args[1:]
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", defaultBackupConfigPath, "path to backup config file")
	listenPort := fs.Int("listen-port", 0, "override listen port from backup config")
	fs.Parse(args)

	runtime, err := newRuntime(*configPath, *listenPort)
	if err != nil {
		logger.BootstrapFatal(err.Error())
	}
	defer runtime.close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.L.Info().Int("listen_port", runtime.cfg.ListenPort).Msg("Starting backup replication target")

	if err := runtime.replication.RunStandalone(ctx, runtime.cfg.ListenPort); err != nil {
		logger.L.Fatal().Err(err).Msg("backup_replication_target_failed")
	}
}

func runDatasets(args []string) {
	fs := flag.NewFlagSet("datasets", flag.ExitOnError)
	configPath := fs.String("config", defaultBackupConfigPath, "path to backup config file")
	target := fs.String("target", "", "target endpoint host:port")
	prefix := fs.String("prefix", "", "optional dataset prefix filter")
	fs.Parse(args)

	if *target == "" {
		logger.BootstrapFatal("target is required")
	}

	runtime, err := newRuntime(*configPath, 0)
	if err != nil {
		logger.BootstrapFatal(err.Error())
	}
	defer runtime.close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	datasets, err := runtime.replication.ListTargetDatasets(ctx, *target, *prefix)
	if err != nil {
		logger.BootstrapFatal(err.Error())
	}

	printJSON(datasets)
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	configPath := fs.String("config", defaultBackupConfigPath, "path to backup config file")
	target := fs.String("target", "", "target endpoint host:port")
	limit := fs.Int("limit", 50, "max number of events")
	fs.Parse(args)

	if *target == "" {
		logger.BootstrapFatal("target is required")
	}

	runtime, err := newRuntime(*configPath, 0)
	if err != nil {
		logger.BootstrapFatal(err.Error())
	}
	defer runtime.close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	events, err := runtime.replication.ListTargetStatus(ctx, *target, *limit)
	if err != nil {
		logger.BootstrapFatal(err.Error())
	}

	printJSON(events)
}

func runPull(args []string) {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	configPath := fs.String("config", defaultBackupConfigPath, "path to backup config file")
	target := fs.String("target", "", "backup target endpoint host:port")
	sourceDataset := fs.String("source-dataset", "", "source dataset on target")
	destinationDataset := fs.String("destination-dataset", "", "destination dataset on local host")
	snapshot := fs.String("snapshot", "", "specific snapshot to pull (optional)")
	force := fs.Bool("force", false, "force zfs recv rollback")
	withIntermediates := fs.Bool("with-intermediates", false, "use -I incremental stream when possible")
	fs.Parse(args)

	if *target == "" || *sourceDataset == "" || *destinationDataset == "" {
		logger.BootstrapFatal("target, source-dataset, and destination-dataset are required")
	}

	runtime, err := newRuntime(*configPath, 0)
	if err != nil {
		logger.BootstrapFatal(err.Error())
	}
	defer runtime.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	plan, err := runtime.replication.PullDatasetFromNode(
		ctx,
		*sourceDataset,
		*destinationDataset,
		*target,
		*snapshot,
		*force,
		*withIntermediates,
	)
	if err != nil {
		logger.BootstrapFatal(err.Error())
	}

	printJSON(plan)
}

type backupRuntime struct {
	cfg         *config.BackupConfig
	db          *gorm.DB
	replication *replication.Service
}

func newRuntime(configPath string, listenPortOverride int) (*backupRuntime, error) {
	cfg, err := config.ParseBackupConfig(configPath)
	if err != nil {
		return nil, err
	}

	if listenPortOverride > 0 {
		cfg.ListenPort = listenPortOverride
	}

	config.ParsedConfig = &internal.SylveConfig{
		DataPath: cfg.DataPath,
		LogLevel: cfg.LogLevel,
		TLS: internal.TLSConfig{
			CertFile: cfg.TLS.CertFile,
			KeyFile:  cfg.TLS.KeyFile,
		},
	}

	logger.InitLogger(cfg.DataPath, cfg.LogLevel)

	database, err := db.SetupBackupDatabase(cfg.DataPath, cfg.ClusterKey)
	if err != nil {
		return nil, err
	}

	authService := auth.NewAuthService(database)
	clusterService, ok := cluster.NewClusterService(database, authService).(*cluster.Service)
	if !ok {
		return nil, fmt.Errorf("failed_to_create_cluster_service")
	}

	gz := gzfs.NewClient(gzfs.Options{
		Sudo:               false,
		ZDBCacheTTLSeconds: 0,
	})

	repl := replication.NewService(database, authService, gz, clusterService)

	return &backupRuntime{
		cfg:         cfg,
		db:          database,
		replication: repl,
	}, nil
}

func (r *backupRuntime) close() {
	if r == nil || r.db == nil {
		return
	}

	sqlDB, err := r.db.DB()
	if err == nil {
		_ = sqlDB.Close()
	}
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		logger.BootstrapFatal(err.Error())
	}
	fmt.Println(string(b))
}

func printUsage() {
	fmt.Println("sylve-backup usage:")
	fmt.Println("  sylve-backup serve [--config backup.config.json] [--listen-port 7444]")
	fmt.Println("  sylve-backup datasets --target host:port [--prefix pool/path] [--config backup.config.json]")
	fmt.Println("  sylve-backup status --target host:port [--limit 50] [--config backup.config.json]")
	fmt.Println("  sylve-backup pull --target host:port --source-dataset src --destination-dataset dst [--snapshot snap] [--force] [--with-intermediates] [--config backup.config.json]")
}
