/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';

export type ClusterJoinPhaseTone = 'progress' | 'warning' | 'error';

export interface ClusterJoinPhaseMeta {
	label: string;
	description: string;
	tone: ClusterJoinPhaseTone;
}

export interface ClusterLeavePhaseMeta {
	label: string;
	description: string;
}

export function getClusterJoinPhaseMeta(phase: string): ClusterJoinPhaseMeta {
	switch (phase.trim().toLowerCase()) {
		case 'intent_saved':
			return {
				label: 'Preparing Join',
				description: 'Saving the local cluster configuration.',
				tone: 'progress'
			};
		case 'starting':
			return {
				label: 'Starting Cluster Services',
				description: 'Starting local cluster services.',
				tone: 'progress'
			};
		case 'submitting':
			return {
				label: 'Contacting Leader',
				description: 'Requesting membership from the cluster leader.',
				tone: 'progress'
			};
		case 'staged_nonvoter':
			return {
				label: 'Membership Accepted',
				description: 'Membership was accepted. State synchronization will begin next.',
				tone: 'progress'
			};
		case 'catching_up':
			return {
				label: 'Catching Up',
				description: 'Synchronizing and verifying cluster state before promotion.',
				tone: 'progress'
			};
		case 'stalled':
			return {
				label: 'Join Delayed',
				description: 'The join is paused and will retry automatically.',
				tone: 'warning'
			};
		case 'failed':
			return {
				label: 'Join Failed',
				description: 'The join stopped and needs attention.',
				tone: 'error'
			};
		case 'voter':
			return {
				label: 'Join Complete',
				description: 'This node is a voting cluster member.',
				tone: 'progress'
			};
		default:
			return {
				label: 'Joining Cluster',
				description: 'Sylve is preparing this node for cluster membership.',
				tone: 'progress'
			};
	}
}

export function getClusterJoinErrorMessage(error: string, retrying: boolean): string {
	const value = error.trim().toLowerCase();
	const contains = (...markers: string[]) => markers.some((marker) => value.includes(marker));

	if (contains('cluster_version_mismatch', 'version mismatch')) {
		return 'This node and every cluster member must run the same Sylve version.';
	}
	if (contains('cluster_version_check_unavailable')) {
		return 'Sylve could not verify cluster versions. Make sure every current member is online.';
	}
	if (contains('invalid_cluster_key', 'unauthorized', 'authentication failed')) {
		return 'The cluster key was rejected. Reset this join and use the current key from the leader.';
	}
	if (
		contains(
			'guest_identity_inventory_conflict',
			'guest_identity_conflict',
			'joining_node_id_already_in_use',
			'joining_node_address_already_in_use'
		)
	) {
		return 'A node, VM, or jail identity conflicts with an existing cluster resource.';
	}
	if (contains('joining_inventory_changed_before_start')) {
		return 'The local VM or jail inventory changed during validation. Try again after those changes finish.';
	}
	if (contains('inventory_unavailable', 'inventory_remote_', 'inventory_collection_canceled')) {
		return 'Sylve could not verify VM and jail identities across the cluster. Make sure every member is reachable.';
	}
	if (contains('not_leader')) {
		return retrying
			? 'The cluster leader changed. Sylve will locate the current leader and retry.'
			: 'The contacted node is no longer the leader. Use the current leader and try again.';
	}
	if (contains('cluster_join_outcome_uncertain')) {
		return 'The leader did not confirm the outcome. Sylve will verify membership before retrying.';
	}
	if (
		value === 'eof' ||
		contains(
			'context deadline exceeded',
			'client.timeout',
			'connection refused',
			'connection reset',
			'no route to host',
			'network is unreachable',
			'i/o timeout',
			'tls handshake',
			'x509:'
		)
	) {
		return 'The nodes cannot communicate reliably. Check bidirectional routing, firewalls, and cluster ports 8180 and 8184.';
	}
	if (contains('replicated_state_', 'join_progress_', 'repair_fenced')) {
		return 'Cluster state could not be synchronized or verified. Check connectivity between the joining node and the leader.';
	}
	if (
		contains(
			'cluster_consensus_unavailable',
			'add_nonvoter_failed',
			'get_config_failed',
			'leadership lost',
			'quorum'
		)
	) {
		return 'The leader cannot update membership right now. Restore cluster quorum and connectivity.';
	}
	if (contains('failed_to_bind_to_port', 'failed_to_bind_raft_port', 'invalid_ip_address')) {
		return 'Sylve cannot use the selected node IP or Raft port 8180. Check the address and existing listeners.';
	}
	if (contains('cluster_listener_start_failed')) {
		return 'The cluster API listener could not start on port 8184. Check the address and existing listeners.';
	}
	if (contains('failed_to_create_', 'failed_to_clean_raft_dir', 'raft_path_', 'no_raft_path')) {
		return 'Sylve could not prepare local Raft storage. Check disk space, permissions, and the data path.';
	}
	if (contains('clustered_already', 'raft_already_initialized', 'raft_state_already_exists')) {
		return 'This node already has active cluster state. Reset it before joining another cluster.';
	}
	if (contains('node_leave_fenced')) {
		return 'This node is currently leaving or resetting its cluster. Finish that operation first.';
	}

	return retrying
		? 'The join is temporarily delayed. Sylve will retry automatically.'
		: 'The join could not continue. Review the cluster settings and try again.';
}

export function getClusterLeavePhaseMeta(phase: string): ClusterLeavePhaseMeta {
	switch (phase.trim().toLowerCase()) {
		case 'fenced':
			return {
				label: 'Fencing Node',
				description: 'Blocking new work and waiting for active operations to finish.'
			};
		case 'removing':
			return {
				label: 'Removing Membership',
				description: 'Removing this node from Raft and confirming the committed membership.'
			};
		case 'cleaning':
			return {
				label: 'Cleaning Local State',
				description: 'Clearing local cluster state before restarting as a standalone node.'
			};
		default:
			return {
				label: 'Leaving Cluster',
				description: 'Safely removing this node from the cluster.'
			};
	}
}

export function getClusterLeaveErrorMessage(error: string): string {
	const value = error.trim().toLowerCase();
	const contains = (...markers: string[]) => markers.some((marker) => value.includes(marker));

	if (contains('cluster_leave_active_mutations')) {
		return 'Active work is still using this node. Close open consoles or wait for running operations, then retry.';
	}
	if (contains('cluster_leave_membership_unconfirmed', 'cluster_removal_start_uncertain')) {
		return 'Cluster membership could not be confirmed. Keep the node isolated while Sylve retries.';
	}
	if (contains('cluster_removal_cleanup_unconfirmed')) {
		return 'Membership was removed, but the target has not confirmed local cleanup.';
	}
	if (contains('cluster_leave_leadership_transfer')) {
		return 'Leadership could not move to another voter. Restore connectivity to another voter and retry.';
	}
	if (contains('cluster_version_mismatch')) {
		return 'Every participating node must run the same Sylve version.';
	}
	if (contains('cluster_version_check_unavailable')) {
		return 'Sylve could not verify member versions. Make sure the remaining members are online.';
	}
	if (contains('peer_removal_blocked')) {
		return 'This node still owns cluster resources that must be moved or removed first.';
	}
	if (contains('not_leader', 'leadership_changed')) {
		return 'The cluster leader changed. Sylve will use the current leader on the next attempt.';
	}
	if (contains('connection refused', 'no route to host', 'network is unreachable', 'i/o timeout')) {
		return 'The nodes cannot communicate reliably. Check bidirectional routing and cluster ports 8180 and 8184.';
	}

	return 'The node could not finish leaving the cluster. Review the technical details before retrying or forcing a reset.';
}

export function getQuorumStatus(
	details: ClusterDetails,
	nodes: ClusterNode[]
): 'ok' | 'warning' | 'error' {
	const voters = details.nodes.filter((n) => (n.suffrage ?? 'Voter').toLowerCase() !== 'nonvoter');
	const totalVoters = voters.length;

	if (totalVoters === 0) return 'error';

	const onlineVoters = voters.filter((rn) =>
		nodes.some((n) => n.nodeUUID === rn.id && n.status.toLowerCase() === 'online')
	).length;

	const quorum = Math.floor(totalVoters / 2) + 1;
	const hasLeader = Boolean(details.leaderId) || details.nodes.some((n) => n.isLeader === true);

	if (!hasLeader) return 'error';

	if (onlineVoters < quorum) {
		return 'error';
	}

	if (onlineVoters < totalVoters) {
		return 'warning';
	}

	return 'ok';
}
