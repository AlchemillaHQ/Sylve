// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/signal"
	"syscall"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func executeConsoleOperation(ctx context.Context, command *cli.Command, operation string, payload any, jsonMode bool) error {
	response, err := executeConsoleOperationResponse(ctx, command, operation, payload)
	if response.Output != "" {
		fmt.Print(response.Output)
	} else if err != nil {
		printConsoleOperationError(jsonMode, err)
	}
	return err
}

func executeConsoleOperationResponse(ctx context.Context, command *cli.Command, operation string, payload any) (console.Response, error) {
	socketPath, err := consoleSocketPath(command.String("config"))
	if err != nil {
		return console.Response{}, err
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	wait := time.Duration(command.Int("wait-service")) * time.Second
	return console.ExecuteOperationResponseWithWait(ctx, socketPath, operation, payload, wait)
}

func validateServiceWait(seconds int) error {
	if seconds < 0 {
		return fmt.Errorf("--wait-service must be zero or a positive number of seconds")
	}
	if int64(seconds) > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("--wait-service is too large")
	}
	return nil
}

func printConsoleOperationError(jsonMode bool, err error) {
	if !jsonMode {
		return
	}
	encoded, marshalErr := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: err.Error()})
	if marshalErr == nil {
		fmt.Println(string(encoded))
	}
}

func consoleSocketPath(configPath string) (string, error) {
	resolvedConfigPath, err := ResolveConfigPath(configPath)
	if err != nil {
		return "", err
	}

	dataPath, err := config.DataPathFromConfig(resolvedConfigPath)
	if err != nil {
		return "", fmt.Errorf("resolve console data path: %w", err)
	}
	return console.SocketPath(dataPath), nil
}

func commandPositiveUint(command *cli.Command, name string) (uint, error) {
	value := command.Int(name)
	if value < 1 {
		return 0, fmt.Errorf("--%s must be greater than zero", name)
	}
	return uint(value), nil
}

func commandOptionalPositiveUint(command *cli.Command, name string) (*uint, error) {
	if !command.IsSet(name) {
		return nil, nil
	}
	value, err := commandPositiveUint(command, name)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func commandEnabledBool(command *cli.Command, name string) *bool {
	if !command.Bool(name) {
		return nil
	}
	value := true
	return &value
}
