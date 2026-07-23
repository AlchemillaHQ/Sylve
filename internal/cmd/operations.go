package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func executeConsoleOperation(command *cli.Command, operation string, payload any, jsonMode bool) error {
	socketPath, err := consoleSocketPath(command.String("config"))
	if err != nil {
		printConsoleOperationError(jsonMode, err)
		return err
	}

	output, err := console.ExecuteOperation(socketPath, operation, payload)
	if err != nil {
		printConsoleOperationError(jsonMode, err)
		return err
	}

	fmt.Print(output)
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
