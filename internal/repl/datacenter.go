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
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

func handleDatacenter(ctx *Context, args []string) {
	jsonMode := hasJSONFlag(args)
	args = dropJSONFlag(args)
	if len(args) == 0 {
		printSubHelp(ctx, "datacenter", []cmdHelp{
			{"notes", "Manage replicated datacenter notes"},
			{"cluster", "Inspect and recover cluster membership"},
		})
		return
	}
	switch args[0] {
	case "notes":
		handleDatacenterNotes(ctx, args[1:], jsonMode)
	case "cluster":
		handleDatacenterCluster(ctx, args[1:], jsonMode)
	default:
		println(ctx, styledErrorf("Unknown datacenter command: '%s'. Type 'datacenter' for help.", args[0]))
	}
}

func handleDatacenterNotes(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "datacenter notes", []cmdHelp{
			{"list", "List datacenter notes"},
			{"get <id>", "Get a datacenter note"},
			{"add <title> <content>", "Add a datacenter note"},
			{"update <id> <title> <content>", "Update a datacenter note"},
			{"delete <id>", "Delete a datacenter note"},
		})
		return
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			println(ctx, styledErrorf("Usage: datacenter notes list"))
			return
		}
		datacenterNotesList(ctx, jsonMode)
	case "get":
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: datacenter notes get <id>"))
			return
		}
		id, err := parsePositiveUint(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid ID '%s'", args[1]))
			return
		}
		datacenterNotesGet(ctx, id, jsonMode)
	case "add":
		if len(args) != 3 {
			println(ctx, styledErrorf("Usage: datacenter notes add <title> <content>"))
			return
		}
		datacenterNotesMutate(ctx, clusterService.NoteMutation{Action: "create", Title: args[1], Content: args[2]}, jsonMode)
	case "update":
		if len(args) != 4 {
			println(ctx, styledErrorf("Usage: datacenter notes update <id> <title> <content>"))
			return
		}
		id, err := parsePositiveUint(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid ID '%s'", args[1]))
			return
		}
		datacenterNotesMutate(ctx, clusterService.NoteMutation{Action: "update", ID: int(id), Title: args[2], Content: args[3]}, jsonMode)
	case "delete":
		if len(args) != 2 {
			println(ctx, styledErrorf("Usage: datacenter notes delete <id>"))
			return
		}
		id, err := parsePositiveUint(args[1])
		if err != nil {
			println(ctx, styledErrorf("Invalid ID '%s'", args[1]))
			return
		}
		datacenterNotesMutate(ctx, clusterService.NoteMutation{Action: "delete", ID: int(id)}, jsonMode)
	default:
		println(ctx, styledErrorf("Unknown datacenter notes command: '%s'.", args[0]))
	}
}

func handleDatacenterCluster(ctx *Context, args []string, jsonMode bool) {
	if len(args) == 0 {
		printSubHelp(ctx, "datacenter cluster", []cmdHelp{
			{"status", "Show local cluster and consensus status"},
			{"members", "List authoritative Raft members"},
			{"readdress --new-ip <ip> --allow-disruption", "Change this member's cluster IP"},
			{"repair-address --node-id <uuid> --new-ip <ip> --allow-disruption", "Repair a recovered member's address"},
		})
		return
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			println(ctx, styledErrorf("Usage: datacenter cluster status"))
			return
		}
		datacenterClusterStatus(ctx, jsonMode)
	case "members":
		if len(args) != 1 {
			println(ctx, styledErrorf("Usage: datacenter cluster members"))
			return
		}
		datacenterClusterMembers(ctx, jsonMode)
	case "readdress":
		newIP, _, allow, err := parseClusterAddressFlags(args[1:], false)
		if err != nil {
			println(ctx, styledErrorf("Usage: datacenter cluster readdress --new-ip <ip> --allow-disruption"))
			return
		}
		datacenterClusterReaddress(ctx, newIP, allow, jsonMode)
	case "repair-address":
		newIP, nodeID, allow, err := parseClusterAddressFlags(args[1:], true)
		if err != nil {
			println(ctx, styledErrorf("Usage: datacenter cluster repair-address --node-id <uuid> --new-ip <ip> --allow-disruption"))
			return
		}
		datacenterClusterRepairAddress(ctx, nodeID, newIP, allow, jsonMode)
	default:
		println(ctx, styledErrorf("Unknown datacenter cluster command: '%s'.", args[0]))
	}
}

func parseClusterAddressFlags(args []string, requireNodeID bool) (string, string, bool, error) {
	var newIP string
	var nodeID string
	allowDisruption := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--allow-disruption":
			if allowDisruption {
				return "", "", false, fmt.Errorf("duplicate_flag")
			}
			allowDisruption = true
		case "--new-ip", "--node-id":
			if index+1 >= len(args) {
				return "", "", false, fmt.Errorf("flag_value_required")
			}
			name, value := args[index], strings.TrimSpace(args[index+1])
			index++
			if value == "" {
				return "", "", false, fmt.Errorf("flag_value_required")
			}
			if name == "--new-ip" {
				if newIP != "" {
					return "", "", false, fmt.Errorf("duplicate_flag")
				}
				newIP = value
			} else {
				if nodeID != "" {
					return "", "", false, fmt.Errorf("duplicate_flag")
				}
				nodeID = value
			}
		default:
			return "", "", false, fmt.Errorf("unknown_flag")
		}
	}
	if newIP == "" || !allowDisruption || (requireNodeID && nodeID == "") || (!requireNodeID && nodeID != "") {
		return "", "", false, fmt.Errorf("required_flag_missing")
	}
	return newIP, nodeID, allowDisruption, nil
}

func datacenterNotesList(ctx *Context, jsonMode bool) {
	notes, err := listDatacenterNotes(ctx)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching datacenter notes", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(notes))
		return
	}
	println(ctx, formatDatacenterNotes(notes))
}

func datacenterNotesGet(ctx *Context, id uint, jsonMode bool) {
	note, err := getDatacenterNote(ctx, id)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching datacenter note", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(note))
		return
	}
	println(ctx, formatDatacenterNote(note))
}

func datacenterNotesMutate(ctx *Context, request clusterService.NoteMutation, jsonMode bool) {
	result, err := mutateDatacenterNote(ctx, request)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error changing datacenter note", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, formatDatacenterNoteMutation(result))
}

func listDatacenterNotes(ctx *Context) ([]clusterModels.ClusterNote, error) {
	if ctx == nil || ctx.Cluster == nil {
		return nil, fmt.Errorf("cluster_service_unavailable")
	}
	notes, err := ctx.Cluster.ListNotes()
	if notes == nil {
		notes = []clusterModels.ClusterNote{}
	}
	return notes, err
}

func getDatacenterNote(ctx *Context, id uint) (clusterModels.ClusterNote, error) {
	if ctx == nil || ctx.Cluster == nil {
		return clusterModels.ClusterNote{}, fmt.Errorf("cluster_service_unavailable")
	}
	return ctx.Cluster.GetNote(int(id))
}

func mutateDatacenterNote(ctx *Context, request clusterService.NoteMutation) (consoleprotocol.DatacenterNoteMutationResult, error) {
	result := consoleprotocol.DatacenterNoteMutationResult{Action: request.Action, ID: uint(request.ID)}
	if ctx == nil || ctx.Cluster == nil {
		return result, fmt.Errorf("cluster_service_unavailable")
	}
	return result, ctx.Cluster.ApplyNoteMutation(operationContext(ctx), request, true)
}

func formatDatacenterNotes(notes []clusterModels.ClusterNote) string {
	if len(notes) == 0 {
		return "No datacenter notes found."
	}
	rows := make([][]string, 0, len(notes))
	for _, note := range notes {
		rows = append(rows, []string{
			strconv.FormatUint(uint64(note.ID), 10), note.Title, note.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}
	return styledTable([]string{"ID", "TITLE", "UPDATED"}, rows)
}

func formatDatacenterNote(note clusterModels.ClusterNote) string {
	return strings.Join([]string{
		styledKeyValue("ID:", strconv.FormatUint(uint64(note.ID), 10)),
		styledKeyValue("Title:", note.Title),
		styledKeyValue("Content:", note.Content),
		styledKeyValue("Created:", note.CreatedAt.Format("2006-01-02 15:04")),
		styledKeyValue("Updated:", note.UpdatedAt.Format("2006-01-02 15:04")),
	}, "\n")
}

func formatDatacenterNoteMutation(result consoleprotocol.DatacenterNoteMutationResult) string {
	switch result.Action {
	case "create":
		return styledSuccessf("Datacenter note added successfully.")
	case "update":
		return styledSuccessf("Datacenter note %d updated successfully.", result.ID)
	case "delete":
		return styledSuccessf("Datacenter note %d deleted successfully.", result.ID)
	default:
		return styledSuccessf("Datacenter note changed successfully.")
	}
}

func datacenterClusterStatus(ctx *Context, jsonMode bool) {
	status, err := getDatacenterClusterStatus(ctx)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching cluster status", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(status))
		return
	}
	println(ctx, formatDatacenterClusterStatus(status))
}

func datacenterClusterMembers(ctx *Context, jsonMode bool) {
	members, err := getDatacenterClusterMembers(ctx)
	if err != nil {
		printOperationError(ctx, jsonMode, "Error fetching cluster members", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(members))
		return
	}
	println(ctx, formatDatacenterClusterMembers(members))
}

func datacenterClusterReaddress(ctx *Context, newIP string, allowDisruption bool, jsonMode bool) {
	result, err := readdressDatacenterCluster(ctx, clusterService.ReaddressRequest{
		NewIP: newIP, AllowDisruption: allowDisruption,
	})
	if err != nil {
		printOperationError(ctx, jsonMode, "Error changing cluster address", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, formatDatacenterClusterReaddress(result, false))
}

func datacenterClusterRepairAddress(
	ctx *Context,
	nodeID string,
	newIP string,
	allowDisruption bool,
	jsonMode bool,
) {
	result, err := repairDatacenterClusterAddress(ctx, clusterService.RepairAddressRequest{
		NodeID: nodeID, NewIP: newIP, AllowDisruption: allowDisruption,
	})
	if err != nil {
		printOperationError(ctx, jsonMode, "Error repairing cluster address", err)
		return
	}
	if jsonMode {
		println(ctx, mustJSON(result))
		return
	}
	println(ctx, formatDatacenterClusterReaddress(result, true))
}

func readdressDatacenterCluster(
	ctx *Context,
	request clusterService.ReaddressRequest,
) (clusterService.ReaddressResult, error) {
	if ctx == nil || ctx.Cluster == nil {
		return clusterService.ReaddressResult{}, fmt.Errorf("cluster_service_unavailable")
	}
	return ctx.Cluster.ReaddressLocal(operationContext(ctx), request)
}

func repairDatacenterClusterAddress(
	ctx *Context,
	request clusterService.RepairAddressRequest,
) (clusterService.ReaddressResult, error) {
	if ctx == nil || ctx.Cluster == nil {
		return clusterService.ReaddressResult{}, fmt.Errorf("cluster_service_unavailable")
	}
	return ctx.Cluster.RepairMemberAddress(operationContext(ctx), request)
}

func getDatacenterClusterStatus(ctx *Context) (clusterService.CommandStatus, error) {
	if ctx == nil || ctx.Cluster == nil {
		return clusterService.CommandStatus{}, fmt.Errorf("cluster_service_unavailable")
	}
	return ctx.Cluster.CommandStatus()
}

func getDatacenterClusterMembers(ctx *Context) ([]clusterService.CommandMember, error) {
	if ctx == nil || ctx.Cluster == nil {
		return nil, fmt.Errorf("cluster_service_unavailable")
	}
	return ctx.Cluster.CommandMembers()
}

func formatDatacenterClusterStatus(status clusterService.CommandStatus) string {
	return strings.Join([]string{
		styledKeyValue("Clustered:", strconv.FormatBool(status.Enabled)),
		styledKeyValue("Node ID:", status.NodeID),
		styledKeyValue("Cluster IP:", valueOrDash(status.RaftIP)),
		styledKeyValue("Raft state:", status.RaftState),
		styledKeyValue("Leader:", valueOrDash(status.LeaderID)),
		styledKeyValue("Leader address:", valueOrDash(status.LeaderAddress)),
		styledKeyValue("Voters:", strconv.Itoa(status.Voters)),
		styledKeyValue("Non-voters:", strconv.Itoa(status.Nonvoters)),
		styledKeyValue("Join phase:", valueOrDash(status.JoinPhase)),
		styledKeyValue("Leave phase:", valueOrDash(status.LeavePhase)),
		styledKeyValue("Readdress phase:", valueOrDash(status.ReaddressPhase)),
		styledKeyValue("Readdress error:", valueOrDash(status.ReaddressError)),
		styledKeyValue("Partial:", strconv.FormatBool(status.Partial)),
	}, "\n")
}

func formatDatacenterClusterMembers(members []clusterService.CommandMember) string {
	if len(members) == 0 {
		return "No cluster members found."
	}
	rows := make([][]string, 0, len(members))
	for _, member := range members {
		leader := ""
		if member.IsLeader {
			leader = "yes"
		}
		rows = append(rows, []string{
			valueOrDash(member.Hostname), member.NodeID, member.Address, member.Status,
			member.Suffrage, valueOrDash(member.SylveVersion), leader, strconv.Itoa(member.GuestCount),
		})
	}
	return styledTable([]string{"HOSTNAME", "NODE ID", "ADDRESS", "STATUS", "SUFFRAGE", "VERSION", "LEADER", "GUESTS"}, rows)
}

func formatDatacenterClusterReaddress(result clusterService.ReaddressResult, repaired bool) string {
	title := styledSuccessf("Cluster address change prepared.")
	if repaired {
		title = styledSuccessf("Cluster member address repaired.")
	}
	return strings.Join([]string{
		title,
		styledKeyValue("Node ID:", result.NodeID),
		styledKeyValue("Old IP:", valueOrDash(result.OldIP)),
		styledKeyValue("New IP:", result.NewIP),
		styledKeyValue("Phase:", valueOrDash(result.Phase)),
		styledKeyValue("Membership committed:", strconv.FormatBool(result.MembershipCommitted)),
		styledKeyValue("Restart requested:", strconv.FormatBool(result.RestartRequested)),
	}, "\n")
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func processDatacenterNoteListSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DatacenterNoteListPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_datacenter_note_list_request: " + err.Error()}
	}
	notes, err := listDatacenterNotes(ctx)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, notes, formatDatacenterNotes(notes))
}

func processDatacenterNoteGetSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DatacenterNoteGetPayload
	if err := decodeOperationPayload(payload, &request); err != nil || request.ID == 0 {
		return socketResponse{Error: "invalid_datacenter_note_get_request"}
	}
	note, err := getDatacenterNote(ctx, request.ID)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, note, formatDatacenterNote(note))
}

func processDatacenterNoteMutationSocketRequest(ctx *Context, payload json.RawMessage, action string) socketResponse {
	var request consoleprotocol.DatacenterNoteMutationPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_datacenter_note_mutation_request: " + err.Error()}
	}
	result, err := mutateDatacenterNote(ctx, clusterService.NoteMutation{
		Action: action, ID: int(request.ID), Title: request.Title, Content: request.Content,
	})
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, formatDatacenterNoteMutation(result))
}

func processDatacenterClusterStatusSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DatacenterClusterReadPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_datacenter_cluster_status_request: " + err.Error()}
	}
	status, err := getDatacenterClusterStatus(ctx)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, status, formatDatacenterClusterStatus(status))
}

func processDatacenterClusterMembersSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DatacenterClusterReadPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_datacenter_cluster_members_request: " + err.Error()}
	}
	members, err := getDatacenterClusterMembers(ctx)
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, members, formatDatacenterClusterMembers(members))
}

func processDatacenterClusterReaddressSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DatacenterClusterReaddressPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_datacenter_cluster_readdress_request: " + err.Error()}
	}
	result, err := readdressDatacenterCluster(ctx, clusterService.ReaddressRequest{
		NewIP: request.NewIP, AllowDisruption: request.AllowDisruption,
	})
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, formatDatacenterClusterReaddress(result, false))
}

func processDatacenterClusterRepairAddressSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.DatacenterClusterRepairAddressPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: "invalid_datacenter_cluster_repair_address_request: " + err.Error()}
	}
	result, err := repairDatacenterClusterAddress(ctx, clusterService.RepairAddressRequest{
		NodeID: request.NodeID, NewIP: request.NewIP, AllowDisruption: request.AllowDisruption,
	})
	if err != nil {
		return socketResponse{Error: err.Error()}
	}
	return operationSuccess(request.JSON, result, formatDatacenterClusterReaddress(result, true))
}
