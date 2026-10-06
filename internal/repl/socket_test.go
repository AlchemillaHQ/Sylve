// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/bootstrap"
	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

type fakeBootstrapApplier struct {
	applyReport bootstrap.Report
	appliedPath string
	onApply     func()
}

func (f *fakeBootstrapApplier) Apply(_ context.Context, path string) bootstrap.Report {
	f.appliedPath = path
	if f.onApply != nil {
		f.onApply()
	}
	return f.applyReport
}

func bootstrapApplyPayload(t *testing.T, file string, jsonMode bool) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(consoleprotocol.BootstrapApplyPayload{File: file, JSON: jsonMode})
	if err != nil {
		t.Fatalf("marshal bootstrap apply payload: %v", err)
	}
	return payload
}

func TestBootstrapApplyUsesDefaultPathAndFullJSONReport(t *testing.T) {
	fake := &fakeBootstrapApplier{applyReport: bootstrap.Report{RestartRequired: true}}
	reply := processBootstrapApplySocketRequest(&Context{Bootstrap: fake, RequestRestart: func() {}}, bootstrapApplyPayload(t, "default", true))
	if fake.appliedPath != bootstrap.DefaultPath {
		t.Fatalf("applied path = %q, want %q", fake.appliedPath, bootstrap.DefaultPath)
	}
	if reply.response.Error != "" {
		t.Fatalf("unexpected protocol error: %q", reply.response.Error)
	}
	if want := mustJSON(fake.applyReport) + "\n"; reply.response.Output != want {
		t.Fatalf("output = %q, want %q", reply.response.Output, want)
	}
	if reply.afterResponse == nil {
		t.Fatal("expected a post-response restart hook")
	}
}

func TestBootstrapApplyEmptyPathUsesDefault(t *testing.T) {
	fake := &fakeBootstrapApplier{}
	_ = processBootstrapApplySocketRequest(&Context{Bootstrap: fake}, bootstrapApplyPayload(t, "", true))
	if fake.appliedPath != bootstrap.DefaultPath {
		t.Fatalf("applied path = %q, want default %q", fake.appliedPath, bootstrap.DefaultPath)
	}
}

func TestBootstrapApplyExplicitPathIsForwarded(t *testing.T) {
	fake := &fakeBootstrapApplier{}
	_ = processBootstrapApplySocketRequest(&Context{Bootstrap: fake}, bootstrapApplyPayload(t, "/tmp/custom.json", true))
	if fake.appliedPath != "/tmp/custom.json" {
		t.Fatalf("applied path = %q, want explicit path", fake.appliedPath)
	}
}

func TestBootstrapApplyFailedReportSetsErrorButKeepsOutput(t *testing.T) {
	fake := &fakeBootstrapApplier{applyReport: bootstrap.Report{
		Items: []systemServiceInterfaces.BootstrapItemResult{
			{Kind: "service", Index: 0, Name: "jails", Status: systemServiceInterfaces.BootstrapFailed, Message: "missing dependency"},
		},
	}}
	reply := processBootstrapApplySocketRequest(&Context{Bootstrap: fake}, bootstrapApplyPayload(t, "default", true))
	if reply.response.Error != "bootstrap_apply_failed" {
		t.Fatalf("protocol error = %q, want bootstrap_apply_failed", reply.response.Error)
	}
	if reply.response.Output == "" {
		t.Fatal("failed report must still carry the full output")
	}
	if reply.afterResponse != nil {
		t.Fatal("no restart was required, so no restart hook should be attached")
	}
}

func TestBootstrapApplyWarningReportSucceeds(t *testing.T) {
	fake := &fakeBootstrapApplier{applyReport: bootstrap.Report{
		Items: []systemServiceInterfaces.BootstrapItemResult{
			{Kind: "service", Index: 0, Name: "samba", Status: systemServiceInterfaces.BootstrapWarning, Message: "package missing"},
		},
	}}
	reply := processBootstrapApplySocketRequest(&Context{Bootstrap: fake}, bootstrapApplyPayload(t, "default", true))
	if reply.response.Error != "" {
		t.Fatalf("warnings must not be a protocol error, got %q", reply.response.Error)
	}
}

func TestBootstrapApplyUnavailableService(t *testing.T) {
	reply := processBootstrapApplySocketRequest(&Context{}, bootstrapApplyPayload(t, "default", true))
	if reply.response.Error != "bootstrap_service_unavailable" {
		t.Fatalf("error = %q, want bootstrap_service_unavailable", reply.response.Error)
	}
}

func TestBootstrapApplyRejectsUnknownPayloadFields(t *testing.T) {
	reply := processBootstrapApplySocketRequest(&Context{Bootstrap: &fakeBootstrapApplier{}}, json.RawMessage(`{"unexpected":true}`))
	if reply.response.Error == "" {
		t.Fatal("expected invalid payload error")
	}
}

func TestFormatBootstrapReportTextSurfacesGlobalErrors(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		report bootstrap.Report
		want   string
	}{
		{"malformed document with no items", bootstrap.Report{DocumentError: "bootstrap_document_invalid_version"}, "bootstrap_document_invalid_version"},
		{"archive error", bootstrap.Report{ArchiveError: "rename /x -> /x.applied: permission denied"}, "permission denied"},
		{"general error", bootstrap.Report{GeneralError: "settings_service_unavailable"}, "settings_service_unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			out := formatBootstrapReport(testCase.report, false)
			if !strings.Contains(out, testCase.want) || !strings.Contains(out, "failed=true") {
				t.Fatalf("text report %q does not surface failure %q", out, testCase.want)
			}
		})
	}
}

func TestFormatBootstrapReportTextIncludesItemsAndGlobalErrors(t *testing.T) {
	report := bootstrap.Report{
		RestartRequired: true, DocumentError: "doc_bad", ArchiveError: "archive_bad", GeneralError: "general_bad",
		Items: []systemServiceInterfaces.BootstrapItemResult{
			{Kind: "service", Index: 0, Name: "jails", Status: systemServiceInterfaces.BootstrapFailed, Message: "missing"},
		},
	}
	out := formatBootstrapReport(report, false)
	for _, want := range []string{"document error: doc_bad", "archive error: archive_bad", "general error: general_bad", "jails: failed (missing)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text report %q missing %q", out, want)
		}
	}
}

func TestFormatBootstrapReportJSONIsUnchangedByGlobalErrors(t *testing.T) {
	report := bootstrap.Report{
		Archived: true, RestartRequired: true, DocumentError: "doc_bad", ArchiveError: "archive_bad", GeneralError: "general_bad",
		Items: []systemServiceInterfaces.BootstrapItemResult{
			{Kind: "pool", Index: 2, Name: "tank", Status: systemServiceInterfaces.BootstrapApplied},
		},
	}
	if got, want := formatBootstrapReport(report, true), mustJSON(report)+"\n"; got != want {
		t.Fatalf("json report = %q, want %q", got, want)
	}
}

func TestHandleSocketConnEncodesResponseBeforeRestartHook(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		report    bootstrap.Report
		wantError string
	}{
		{"successful restart", bootstrap.Report{RestartRequired: true}, ""},
		{
			"partial failure with restart",
			bootstrap.Report{
				RestartRequired: true,
				Items: []systemServiceInterfaces.BootstrapItemResult{
					{Kind: "switch", Index: 0, Name: "lan", Status: systemServiceInterfaces.BootstrapFailed, Message: "conflict"},
				},
			},
			"bootstrap_apply_failed",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			defer clientConn.Close()
			restartCalled := make(chan struct{})
			ctx := &Context{
				Bootstrap:      &fakeBootstrapApplier{applyReport: testCase.report},
				RequestRestart: func() { close(restartCalled) },
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				handleSocketConn(ctx, serverConn)
			}()
			if err := json.NewEncoder(clientConn).Encode(consoleprotocol.Request{
				Operation: consoleprotocol.OperationBootstrapApply,
				Payload:   bootstrapApplyPayload(t, "default", true),
			}); err != nil {
				t.Fatalf("write request: %v", err)
			}
			select {
			case <-restartCalled:
				t.Fatal("restart requested before the response was encoded")
			default:
			}
			var resp socketResponse
			if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Output == "" || resp.Error != testCase.wantError {
				t.Fatalf("response = %+v, want report and error %q", resp, testCase.wantError)
			}
			var decoded bootstrap.Report
			if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Output)), &decoded); err != nil {
				t.Fatalf("decode full JSON report: %v\noutput: %s", err, resp.Output)
			}
			if !decoded.RestartRequired || decoded.Failed() != (testCase.wantError != "") {
				t.Fatalf("decoded report lost restart or failure: %+v", decoded)
			}
			select {
			case <-restartCalled:
			case <-time.After(time.Second):
				t.Fatal("restart hook was not invoked after the response")
			}
			_ = clientConn.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("handleSocketConn did not return after client disconnect")
			}
		})
	}
}

func TestHandleSocketConnRequestsRestartAfterFailedResponseEncode(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	applyStarted := make(chan struct{})
	releaseApply := make(chan struct{})
	fake := &fakeBootstrapApplier{
		applyReport: bootstrap.Report{RestartRequired: true},
		onApply:     func() { close(applyStarted); <-releaseApply },
	}
	restartCalled := make(chan struct{})
	ctx := &Context{Bootstrap: fake, RequestRestart: func() { close(restartCalled) }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleSocketConn(ctx, serverConn)
	}()
	if err := json.NewEncoder(clientConn).Encode(consoleprotocol.Request{
		Operation: consoleprotocol.OperationBootstrapApply,
		Payload:   bootstrapApplyPayload(t, "default", true),
	}); err != nil {
		t.Fatalf("write request: %v", err)
	}
	select {
	case <-applyStarted:
	case <-time.After(time.Second):
		t.Fatal("bootstrap apply did not start")
	}
	if err := clientConn.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	close(releaseApply)
	select {
	case <-restartCalled:
	case <-time.After(time.Second):
		t.Fatal("restart was not requested after the response encode failed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleSocketConn did not return after the encoding failure")
	}
}

func TestProcessSocketRequestCommandRequired(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Command: "   "})
	if resp.Error != "command_required" {
		t.Fatalf("expected command_required, got %q", resp.Error)
	}
	if resp.Output != "" {
		t.Fatalf("expected empty output, got %q", resp.Output)
	}
}

func TestProcessSocketRequestExecutesCommand(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Command: "ping"})
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if strings.TrimSpace(resp.Output) != "pong" {
		t.Fatalf("expected pong output, got %q", resp.Output)
	}
	if resp.Close {
		t.Fatalf("expected ping to keep session open")
	}
}

func TestProcessSocketRequestRejectsUnknownOperation(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Operation: "unknown"})
	if resp.Error != "unknown_operation" {
		t.Fatalf("expected unknown_operation, got %q", resp.Error)
	}
}

func TestProcessSocketRequestFenceIsExactAndFailsClosed(t *testing.T) {
	service := clusterService.NewClusterService(nil, nil, nil).(*clusterService.Service)
	ctx := &Context{Cluster: service}

	readResponse := processSocketRequest(ctx, socketRequest{Operation: consoleprotocol.OperationStatus})
	if readResponse.Error == clusterService.ErrNodeLeaveFenced.Error() {
		t.Fatal("read-only operation was fenced")
	}

	mutationResponse := processSocketRequest(ctx, socketRequest{Operation: consoleprotocol.OperationNoteAdd})
	if mutationResponse.Error != clusterService.ErrNodeLeaveFenced.Error() {
		t.Fatalf("mutation error = %q", mutationResponse.Error)
	}

	unknownResponse := processSocketRequest(ctx, socketRequest{Operation: "unknown"})
	if unknownResponse.Error != clusterService.ErrNodeLeaveFenced.Error() {
		t.Fatalf("unknown operation did not fail closed: %q", unknownResponse.Error)
	}
}

func TestProcessSocketRequestReturnsStatusSnapshot(t *testing.T) {
	provider := newStatusProvider(statusSources{
		hostname: func() (string, error) { return "node-a", nil },
		cpuUsage: func() (float64, error) { return 12, nil },
	}, time.Minute)
	payload, err := json.Marshal(consoleprotocol.StatusPayload{})
	if err != nil {
		t.Fatalf("marshal status payload: %v", err)
	}

	resp := processSocketRequest(&Context{Status: provider}, socketRequest{
		Operation: consoleprotocol.OperationStatus,
		Payload:   payload,
	})
	if resp.Error != "" {
		t.Fatalf("status response error = %q", resp.Error)
	}

	var snapshot consoleprotocol.StatusSnapshot
	if err := json.Unmarshal([]byte(resp.Output), &snapshot); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if snapshot.Hostname != "node-a" || snapshot.CPUUsage == nil || *snapshot.CPUUsage != 12 {
		t.Fatalf("unexpected status snapshot: %#v", snapshot)
	}
}

func TestProcessSocketRequestCreateRequiresPayloadAndService(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Operation: consoleprotocol.OperationJailCreate})
	if resp.Error != "invalid_jail_create_request: payload_required" {
		t.Fatalf("expected missing payload error, got %q", resp.Error)
	}

	payload, err := json.Marshal(consoleprotocol.JailCreatePayload{})
	if err != nil {
		t.Fatalf("marshal jail create payload: %v", err)
	}
	resp = processSocketRequest(&Context{}, socketRequest{
		Operation: consoleprotocol.OperationJailCreate,
		Payload:   payload,
	})
	if resp.Error != "jail_service_unavailable" {
		t.Fatalf("expected jail_service_unavailable, got %q", resp.Error)
	}
}

func TestProcessSocketRequestRejectsUnknownPayloadFields(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{
		Operation: consoleprotocol.OperationJailList,
		Payload:   json.RawMessage(`{"unexpected": true}`),
	})
	if !strings.Contains(resp.Error, "unknown field \"unexpected\"") {
		t.Fatalf("expected unknown payload field error, got %q", resp.Error)
	}
}

func TestProcessSocketRequestOperationsRequirePayload(t *testing.T) {
	testCases := []struct {
		operation string
		wantError string
	}{
		{consoleprotocol.OperationVMList, "invalid_vm_list_request: payload_required"},
		{consoleprotocol.OperationVMGet, "invalid_vm_get_request: payload_required"},
		{consoleprotocol.OperationVMCreate, "invalid_vm_create_request: payload_required"},
		{consoleprotocol.OperationVMAction, "invalid_vm_action_request: payload_required"},
		{consoleprotocol.OperationVMDelete, "invalid_vm_delete_request: payload_required"},
		{consoleprotocol.OperationVMPurge, "invalid_vm_purge_request: payload_required"},
		{consoleprotocol.OperationVMNetworks, "invalid_vm_networks_request: payload_required"},
		{consoleprotocol.OperationVMNetworkAttach, "invalid_vm_network_attach_request: payload_required"},
		{consoleprotocol.OperationVMNetworkUpdate, "invalid_vm_network_update_request: payload_required"},
		{consoleprotocol.OperationVMNetworkDetach, "invalid_vm_network_detach_request: payload_required"},
		{consoleprotocol.OperationVMConfigName, "invalid_vm_config_name_request: payload_required"},
		{consoleprotocol.OperationVMConfigDescription, "invalid_vm_config_description_request: payload_required"},
		{consoleprotocol.OperationVMConfigWOL, "invalid_vm_config_wol_request: payload_required"},
		{consoleprotocol.OperationVMConfigTPM, "invalid_vm_config_tpm_request: payload_required"},
		{consoleprotocol.OperationVMTemplateGet, "invalid_vm_template_get_request: payload_required"},
		{consoleprotocol.OperationVMQGAInfo, "invalid_vm_qga_info_request: payload_required"},
		{consoleprotocol.OperationVMQGASend, "invalid_vm_qga_request: payload_required"},
		{consoleprotocol.OperationSwitchList, "invalid_switch_list_request: payload_required"},
		{consoleprotocol.OperationSwitchCreate, "invalid_switch_create_request: payload_required"},
		{consoleprotocol.OperationSwitchDelete, "invalid_switch_delete_request: payload_required"},
		{consoleprotocol.OperationSwitchEdit, "invalid_switch_edit_request: payload_required"},
		{consoleprotocol.OperationObjectList, "invalid_object_list_request: payload_required"},
		{consoleprotocol.OperationObjectCreate, "invalid_object_create_request: payload_required"},
		{consoleprotocol.OperationObjectEdit, "invalid_object_edit_request: payload_required"},
		{consoleprotocol.OperationObjectDelete, "invalid_object_delete_request: payload_required"},
		{consoleprotocol.OperationDownloadList, "invalid_download_list_request: payload_required"},
		{consoleprotocol.OperationDownloadStart, "invalid_download_start_request: payload_required"},
		{consoleprotocol.OperationDownloadDelete, "invalid_download_delete_request: payload_required"},
		{consoleprotocol.OperationTaskListActive, "invalid_task_active_request: payload_required"},
		{consoleprotocol.OperationTaskListRecent, "invalid_task_recent_request: payload_required"},
		{consoleprotocol.OperationTaskGet, "invalid_task_get_request: payload_required"},
		{consoleprotocol.OperationStatus, "invalid_status_request: payload_required"},
		{consoleprotocol.OperationDatacenterClusterGuestIDsList, "invalid_datacenter_cluster_guest_ids_list_request: payload_required"},
		{consoleprotocol.OperationDatacenterClusterGuestIDReclaim, "invalid_datacenter_cluster_guest_id_reclaim_request: payload_required"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.operation, func(t *testing.T) {
			resp := processSocketRequest(&Context{}, socketRequest{Operation: testCase.operation})
			if resp.Error != testCase.wantError {
				t.Fatalf("error = %q, want %q", resp.Error, testCase.wantError)
			}
		})
	}
}

func TestProcessSocketRequestSwitchCreateUsesNetworkService(t *testing.T) {
	payload, err := json.Marshal(consoleprotocol.SwitchCreatePayload{
		Type: "standard",
		Standard: &consoleprotocol.StandardSwitchCreateRequest{
			Name: "isolated",
		},
	})
	if err != nil {
		t.Fatalf("marshal switch create payload: %v", err)
	}

	resp := processSocketRequest(&Context{}, socketRequest{
		Operation: consoleprotocol.OperationSwitchCreate,
		Payload:   payload,
	})
	if resp.Error != "network_service_unavailable" {
		t.Fatalf("expected network_service_unavailable, got %q", resp.Error)
	}
}

func TestProcessSocketRequestObjectCreateUsesNetworkService(t *testing.T) {
	payload, err := json.Marshal(consoleprotocol.ObjectCreatePayload{
		Request: consoleprotocol.NetworkObjectRequest{
			Name:   "lan4",
			Type:   "network",
			Values: []string{"192.0.2.0/24"},
		},
	})
	if err != nil {
		t.Fatalf("marshal object create payload: %v", err)
	}

	resp := processSocketRequest(&Context{}, socketRequest{
		Operation: consoleprotocol.OperationObjectCreate,
		Payload:   payload,
	})
	if resp.Error != "network_service_unavailable" {
		t.Fatalf("expected network_service_unavailable, got %q", resp.Error)
	}
}

func TestProcessSocketRequestShutdownTriggersSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	ctx := &Context{QuitChan: signals}

	resp := processSocketRequest(ctx, socketRequest{Command: "shutdown"})
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if !resp.Close {
		t.Fatalf("expected shutdown to close session")
	}

	select {
	case got := <-signals:
		if got != syscall.SIGTERM {
			t.Fatalf("expected SIGTERM, got %v", got)
		}
	default:
		t.Fatalf("expected shutdown signal")
	}
}

func TestHandleSocketConnMalformedRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleSocketConn(&Context{}, serverConn)
	}()

	if _, err := clientConn.Write([]byte("[]\n")); err != nil {
		t.Fatalf("failed writing malformed request: %v", err)
	}

	var resp socketResponse
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("failed decoding response: %v", err)
	}
	if resp.Error != "invalid_request" {
		t.Fatalf("expected invalid_request, got %q", resp.Error)
	}

	<-done
}

func TestStartSocketServerPermissionsAndCleanup(t *testing.T) {
	dataPath := t.TempDir()
	socketPath := consoleprotocol.SocketPath(dataPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create socket directory: %v", err)
	}

	stale, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed creating stale socket: %v", err)
	}
	_ = stale.Close()

	server, err := startSocketServer(&Context{}, socketPath)
	if err != nil {
		t.Fatalf("startSocketServer failed: %v", err)
	}

	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatalf("expected socket path to exist: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("expected socket file, got mode %v", info.Mode())
	}
	if perms := info.Mode().Perm(); perms != 0600 {
		t.Fatalf("expected permissions 0600, got %o", perms)
	}

	directory, err := os.Stat(filepath.Dir(socketPath))
	if err != nil {
		t.Fatalf("expected socket directory: %v", err)
	}
	if perms := directory.Mode().Perm(); perms != 0o700 {
		t.Fatalf("expected directory permissions 0700, got %o", perms)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("server close failed: %v", err)
	}
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket to be removed, got err=%v", err)
	}
}
