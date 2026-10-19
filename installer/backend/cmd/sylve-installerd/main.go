// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/alchemillahq/sylve/installer/backend/internal/api"
	"github.com/alchemillahq/sylve/installer/backend/internal/sysinfo"
)

var version = "dev"

func main() {
	var (
		listen        = flag.String("listen", "127.0.0.1:8080", "listen address")
		logJSON       = flag.Bool("log-json", false, "emit JSON logs")
		accessFile    = flag.String("access-file", "/var/run/freebsd-sylve/access.json", "private live-session access details")
		kioskURL      = flag.Bool("kiosk-url", false, "print the local kiosk bootstrap URL")
		showAccess    = flag.Bool("show-access", false, "show connection details on the local console")
		consoleAccess = flag.Bool("console-access", true, "announce access details on the system console at startup")
	)
	flag.Parse()
	if *kioskURL || *showAccess {
		info, err := api.ReadAccessInfo(*accessFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if *kioskURL {
			fmt.Println(info.KioskURL)
			return
		}
		if err := showConnectionInfo(os.Stdout, info); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	level := slog.LevelInfo
	if os.Getenv("SYLVE_INSTALLERD_DEBUG") != "" {
		level = slog.LevelDebug
	}
	var handler slog.Handler
	if *logJSON {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	slog.SetDefault(slog.New(handler))
	if sysinfo.VirtualBoxUEFI() {
		if err := exec.Command("/sbin/sysctl", "hw.efi.poweroff=0").Run(); err != nil {
			slog.Warn("cannot apply VirtualBox UEFI poweroff workaround", "err", err)
		}
	}
	access, err := api.NewAccess()
	if err != nil {
		slog.Error("cannot generate installer access password", "err", err)
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		slog.Error("cannot listen", "err", err)
		os.Exit(1)
	}
	defer listener.Close()
	if err := access.WriteInfo(*accessFile, listener.Addr().String()); err != nil {
		slog.Error("cannot write private access details", "err", err)
		os.Exit(1)
	}
	defer os.Remove(*accessFile)
	if *consoleAccess {
		if console, err := os.OpenFile("/dev/console", os.O_WRONLY, 0); err == nil {
			if info, err := api.ReadAccessInfo(*accessFile); err == nil {
				_ = showConnectionInfo(console, info)
			}
			_ = console.Close()
		}
	}
	slog.Info("sylve-installerd starting", "version", version, "listen", listener.Addr().String())

	srv := &http.Server{
		Addr:              *listen,
		Handler:           api.Routes(api.Config{Version: version, Access: access}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		slog.Error("server failed", "err", err)
		os.Exit(1)
	case sig := <-stop:
		slog.Info("shutting down", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func showConnectionInfo(w io.Writer, info api.AccessInfo) error {
	host, port, err := net.SplitHostPort(info.Listen)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "\nFreeBSD + Sylve installer")
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		fmt.Fprintf(w, "Installer URL: http://%s\n", info.Listen)
	} else {
		ifaces := sysinfo.Collect().Interfaces
		if len(ifaces) == 0 {
			fmt.Fprintln(w, "No IPv4 address yet; configure networking from this console.")
		}
		for _, iface := range ifaces {
			for _, address := range iface.IPv4 {
				fmt.Fprintf(w, "Open http://%s on another device\n", net.JoinHostPort(address, port))
			}
		}
	}
	fmt.Fprintf(w, "Installer access password: %s\n", info.Password)
	fmt.Fprintln(w, "This password is only for the live installer, not root, Sylve or GELI.")
	return nil
}
