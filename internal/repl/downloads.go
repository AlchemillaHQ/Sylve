// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
)

type downloadStartResult struct {
	ID      uint `json:"id"`
	Started bool `json:"started"`
}

type downloadDeleteResult struct {
	Deleted bool `json:"deleted"`
	ID      uint `json:"id"`
}

func handleDownloads(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	cleanArgs := dropJSONFlag(args)

	if len(cleanArgs) == 0 {
		printSubHelp(ctx, "downloads", []cmdHelp{
			{"list", "List downloads"},
			{"start <url> [--type <base-rootfs|cloud-init|other>] [--filename <name>] [--ignore-tls] [--extract] [--raw]", "Start a download"},
			{"delete <id>", "Delete a download"},
		})
		return
	}

	switch cleanArgs[0] {
	case "list":
		if len(cleanArgs) != 1 {
			println(ctx, styledErrorf("Usage: downloads list"))
			return
		}
		downloadsList(ctx, jsonMode)

	case "start":
		request, err := buildConsoleDownloadRequest(cleanArgs[1:])
		if err != nil {
			println(ctx, styledErrorf("%v", err))
			return
		}
		downloadsStart(ctx, request, jsonMode)

	case "delete":
		if len(cleanArgs) != 2 {
			println(ctx, styledErrorf("Usage: downloads delete <id>"))
			return
		}
		id, err := parsePositiveUint(cleanArgs[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid download ID '%s'", cleanArgs[1]))
			return
		}
		downloadsDelete(ctx, id, jsonMode)

	default:
		println(ctx, styledErrorf("Unknown downloads command: '%s'. Type 'downloads' for help.", cleanArgs[0]))
	}
}

func buildConsoleDownloadRequest(args []string) (utilitiesServiceInterfaces.DownloadFileRequest, error) {
	const usage = "Usage: downloads start <url> [--type <base-rootfs|cloud-init|other>] [--filename <name>] [--ignore-tls] [--extract] [--raw]"
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return utilitiesServiceInterfaces.DownloadFileRequest{}, fmt.Errorf("%s", usage)
	}

	request := utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          strings.TrimSpace(args[0]),
		DownloadType: utilitiesModels.DownloadUTypeOther,
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--type":
			if i+1 >= len(args) {
				return utilitiesServiceInterfaces.DownloadFileRequest{}, fmt.Errorf("%s", usage)
			}
			request.DownloadType = utilitiesModels.DownloadUType(args[i+1])
			i++
		case "--filename":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return utilitiesServiceInterfaces.DownloadFileRequest{}, fmt.Errorf("%s", usage)
			}
			filename := strings.TrimSpace(args[i+1])
			request.Filename = &filename
			i++
		case "--ignore-tls":
			request.IgnoreTLS = boolPointer(true)
		case "--extract":
			request.AutomaticExtraction = boolPointer(true)
		case "--raw":
			request.AutomaticRawConversion = boolPointer(true)
		default:
			return utilitiesServiceInterfaces.DownloadFileRequest{}, fmt.Errorf("unknown download option %q", args[i])
		}
	}

	return request, nil
}

func boolPointer(value bool) *bool {
	return &value
}

func normalizeDownloadRequest(request utilitiesServiceInterfaces.DownloadFileRequest) (utilitiesServiceInterfaces.DownloadFileRequest, error) {
	request.URL = strings.TrimSpace(request.URL)
	if request.URL == "" {
		return utilitiesServiceInterfaces.DownloadFileRequest{}, fmt.Errorf("download_url_required")
	}

	switch strings.ToLower(strings.TrimSpace(string(request.DownloadType))) {
	case "", "other", "uncategorized", "uncategoried":
		request.DownloadType = utilitiesModels.DownloadUTypeOther
	case "base", "base-rootfs":
		request.DownloadType = utilitiesModels.DownloadUTypeBase
		if request.AutomaticExtraction == nil {
			request.AutomaticExtraction = boolPointer(true)
		}
	case "cloud-init", "cloudinit":
		request.DownloadType = utilitiesModels.DownloadUTypeCloudInit
	default:
		return utilitiesServiceInterfaces.DownloadFileRequest{}, fmt.Errorf("invalid_download_type")
	}

	return request, nil
}

func listDownloads(ctx *Context) ([]utilitiesModels.Downloads, error) {
	if ctx == nil || ctx.Utilities == nil {
		return nil, fmt.Errorf("utilities_service_unavailable")
	}

	downloads, err := ctx.Utilities.ListDownloads()
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_downloads: %w", err)
	}
	return downloads, nil
}

func startDownload(ctx *Context, request utilitiesServiceInterfaces.DownloadFileRequest) (downloadStartResult, error) {
	request, err := normalizeDownloadRequest(request)
	if err != nil {
		return downloadStartResult{}, err
	}
	if ctx == nil || ctx.Utilities == nil {
		return downloadStartResult{}, fmt.Errorf("utilities_service_unavailable")
	}

	id, err := ctx.Utilities.DownloadFile(request)
	if err != nil {
		return downloadStartResult{}, fmt.Errorf("failed_to_start_download: %w", err)
	}
	return downloadStartResult{ID: id, Started: true}, nil
}

func deleteDownload(ctx *Context, id uint) (downloadDeleteResult, error) {
	if id == 0 || id > uint(^uint(0)>>1) {
		return downloadDeleteResult{}, fmt.Errorf("invalid_download_id")
	}
	if ctx == nil || ctx.Utilities == nil {
		return downloadDeleteResult{}, fmt.Errorf("utilities_service_unavailable")
	}
	if err := ctx.Utilities.DeleteDownload(int(id)); err != nil {
		return downloadDeleteResult{}, fmt.Errorf("failed_to_delete_download: %w", err)
	}
	return downloadDeleteResult{Deleted: true, ID: id}, nil
}

func formatDownloads(downloads []utilitiesModels.Downloads) string {
	if len(downloads) == 0 {
		return "No downloads found."
	}

	headers := []string{"ID", "Name", "Type", "Category", "Status", "Progress", "Size", "Error"}
	rows := make([][]string, 0, len(downloads))
	for _, download := range downloads {
		errText := download.Error
		if errText == "" {
			errText = "-"
		}
		rows = append(rows, []string{
			strconv.FormatUint(uint64(download.ID), 10),
			download.Name,
			string(download.Type),
			string(download.UType),
			string(download.Status),
			fmt.Sprintf("%d%%", download.Progress),
			strconv.FormatInt(download.Size, 10),
			errText,
		})
	}
	return styledTable(headers, rows)
}

func downloadsList(ctx *Context, jsonMode bool) {
	downloads, err := listDownloads(ctx)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching downloads", err)
		return
	}
	if downloads == nil {
		downloads = []utilitiesModels.Downloads{}
	}
	if jsonMode {
		println(ctx, mustJSON(downloads))
		return
	}
	println(ctx, formatDownloads(downloads))
}

func downloadsStart(ctx *Context, request utilitiesServiceInterfaces.DownloadFileRequest, jsonMode bool) {
	result, err := startDownload(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error starting download", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Download %d started.", result.ID))
}

func downloadsDelete(ctx *Context, id uint, jsonMode bool) {
	result, err := deleteDownload(ctx, id)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error deleting download", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, styledSuccessf("Download %d deleted successfully.", result.ID))
}

func processDownloadListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DownloadListPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_download_list_request: " + err.Error()}
	}
	downloads, err := listDownloads(ctx)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	if downloads == nil {
		downloads = []utilitiesModels.Downloads{}
	}
	return operationSuccess(request.JSON, downloads, formatDownloads(downloads))
}

func processDownloadStartSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DownloadStartPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_download_start_request: " + err.Error()}
	}
	result, err := startDownload(ctx, request.Request)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Download %d started.", result.ID))
}

func processDownloadDeleteSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DownloadDeletePayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_download_delete_request: " + err.Error()}
	}
	result, err := deleteDownload(ctx, request.ID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, styledSuccessf("Download %d deleted successfully.", result.ID))
}
