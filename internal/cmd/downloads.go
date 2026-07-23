package cmd

import (
	"context"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/urfave/cli/v3"
)

func newDownloadsCommand() *cli.Command {
	jsonFlag := &cli.BoolFlag{
		Name:  "json",
		Usage: "output in JSON format",
	}

	return &cli.Command{
		Name:  "downloads",
		Usage: "Manage downloads",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List downloads",
				Flags: []cli.Flag{jsonFlag},
				Action: func(ctx context.Context, command *cli.Command) error {
					return executeConsoleOperation(command, consoleprotocol.OperationDownloadList, consoleprotocol.DownloadListPayload{
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "start",
				Usage: "Start an HTTP, magnet, or local-path download",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.StringFlag{Name: "url", Usage: "HTTP URL, magnet URI, or absolute local path", Required: true},
					&cli.StringFlag{Name: "filename", Usage: "Optional destination filename"},
					&cli.StringFlag{Name: "type", Usage: "Download category: base-rootfs, cloud-init, or other", Value: "other"},
					&cli.BoolFlag{Name: "ignore-tls", Usage: "skip TLS certificate verification"},
					&cli.BoolFlag{Name: "extract", Usage: "automatically extract the completed download"},
					&cli.BoolFlag{Name: "raw", Usage: "automatically convert the completed download to raw"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					var filename *string
					if command.IsSet("filename") {
						value := command.String("filename")
						filename = &value
					}
					return executeConsoleOperation(command, consoleprotocol.OperationDownloadStart, consoleprotocol.DownloadStartPayload{
						Request: utilitiesServiceInterfaces.DownloadFileRequest{
							URL:                    command.String("url"),
							Filename:               filename,
							IgnoreTLS:              commandEnabledBool(command, "ignore-tls"),
							AutomaticExtraction:    commandEnabledBool(command, "extract"),
							AutomaticRawConversion: commandEnabledBool(command, "raw"),
							DownloadType:           utilitiesModels.DownloadUType(command.String("type")),
						},
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
			{
				Name:  "delete",
				Usage: "Delete a download",
				Flags: []cli.Flag{
					jsonFlag,
					&cli.IntFlag{Name: "id", Usage: "Download ID", Required: true},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					id, err := commandPositiveUint(command, "id")
					if err != nil {
						return err
					}
					return executeConsoleOperation(command, consoleprotocol.OperationDownloadDelete, consoleprotocol.DownloadDeletePayload{
						ID:   id,
						JSON: command.Bool("json"),
					}, command.Bool("json"))
				},
			},
		},
	}
}
