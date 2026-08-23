// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestRebootSystemJailedRequestsSelfRestart(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := database.Create(&models.BasicSettings{
		ID:          1,
		Initialized: true,
		Restarted:   false,
	}).Error; err != nil {
		t.Fatalf("seed basic settings: %v", err)
	}

	restartRequests := 0
	hostCommands := 0
	service := &Service{
		DB:     database,
		jailed: true,
		restartRequester: func() {
			restartRequests++
		},
		runCommand: func(string, ...string) (string, error) {
			hostCommands++
			return "", nil
		},
	}

	if err := service.RebootSystem(); err != nil {
		t.Fatalf("RebootSystem returned error: %v", err)
	}
	if restartRequests != 1 {
		t.Fatalf("restart requests = %d, want 1", restartRequests)
	}
	if hostCommands != 0 {
		t.Fatalf("host shutdown commands = %d, want 0", hostCommands)
	}

	var settings models.BasicSettings
	if err := database.First(&settings, 1).Error; err != nil {
		t.Fatalf("reload basic settings: %v", err)
	}
	if settings.Restarted {
		t.Fatal("Restarted was set before the new process completed startup")
	}
}

func TestRebootSystemJailedRequiresRestartRequester(t *testing.T) {
	service := &Service{jailed: true}

	err := service.RebootSystem()
	if !errors.Is(err, errRestartRequesterUnavailable) {
		t.Fatalf("RebootSystem error = %v, want %v", err, errRestartRequesterUnavailable)
	}
}

func TestRebootSystemHostUsesShutdownCommand(t *testing.T) {
	var gotCommand string
	var gotArgs []string
	service := &Service{
		runCommand: func(command string, args ...string) (string, error) {
			gotCommand = command
			gotArgs = append([]string(nil), args...)
			return "", nil
		},
	}

	if err := service.RebootSystem(); err != nil {
		t.Fatalf("RebootSystem returned error: %v", err)
	}
	if gotCommand != "/sbin/shutdown" {
		t.Fatalf("command = %q, want /sbin/shutdown", gotCommand)
	}
	wantArgs := []string{"-r", "now", "Reboot initiated by Sylve"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestHTTPShutdownDrainsAcceptedRestartRequest(t *testing.T) {
	restartRequested := make(chan struct{}, 1)
	service := &Service{
		jailed: true,
		restartRequester: func() {
			select {
			case restartRequested <- struct{}{}:
			default:
			}
		},
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if err := service.RebootSystem(); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writer.WriteHeader(http.StatusAccepted)
	}))
	server.Start()
	t.Cleanup(server.Close)

	shutdownDone := make(chan error, 1)
	go func() {
		<-restartRequested
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownDone <- server.Config.Shutdown(ctx)
	}()

	response, err := server.Client().Post(server.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("restart request failed: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}

	if err := <-shutdownDone; err != nil {
		t.Fatalf("HTTP shutdown failed: %v", err)
	}
}
