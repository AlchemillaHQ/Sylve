// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

/**
 * Human-readable text for the snake_case reason/warning codes the migration
 * backend returns (preflight validation reasons + warnings, and task-payload
 * warnings). Codes may carry a free-form ": <detail>" suffix which we keep.
 *
 * Unknown codes fall back to a humanized (de-underscored, title-cased) form so
 * nothing ever renders as raw "snake_case" and new backend codes degrade safely.
 */

type ReasonFormatter = (detail: string) => string;

const REASON_MESSAGES: Record<string, string | ReasonFormatter> = {
	// --- Hard blocks ---
	target_check_unsupported:
		'The target node runs an older version that cannot verify this guest\u2019s dependencies. Update the target node, then retry.',
	target_check_failed: (d) => `Could not verify the target node\u2019s readiness${d ? `: ${d}` : ''}.`,
	target_missing_pool: (d) =>
		`The target node is missing the required ZFS pool${d ? ` \u201c${d}\u201d` : ''}.`,
	target_already_has_guest: 'A guest with this ID already exists on the target node.',
	target_node_offline: 'The target node is offline.',
	target_node_not_found: 'The target node could not be found in the cluster.',
	target_node_is_source: 'The target node is the same as the source node.',
	target_is_source_node: 'The target node is the same as the source node.',
	target_ssh_identity_unavailable: (d) =>
		`The cluster SSH identity is unavailable${d ? `: ${d}` : ''}.`,
	cluster_ssh_key_unavailable: (d) => `The cluster SSH key is unavailable${d ? `: ${d}` : ''}.`,
	vm_not_found: 'The VM could not be found.',
	guest_has_running_replication_event:
		'A replication job is currently running for this guest. Wait for it to finish, then retry.',
	guest_has_running_backup_event:
		'A backup job is currently running for this guest. Wait for it to finish, then retry.',
	guest_has_active_lifecycle_task: 'Another lifecycle task is already running for this guest.',
	guest_has_active_transition: 'This guest has an active HA transition in progress.',

	// --- Warnings (migration proceeds) ---
	warning_pci_passthrough_not_migrated:
		'PCI passthrough devices are host-specific and will be dropped on the target.',
	warning_cpu_pinning_reset: 'CPU pinning is host-specific and will be reset on the target.',
	warning_target_insufficient_memory: (d) =>
		`The target node may not have enough free memory${d ? ` (${d})` : ''}.`,
	warning_target_missing_iso: (d) =>
		`The CD/ISO${d ? ` \u201c${d}\u201d` : ''} is not present on the target. The VM will start without it; the drive re-attaches automatically once the ISO is available there.`,
	warning_target_missing_switch: (d) =>
		`The network switch${d ? ` \u201c${d}\u201d` : ''} does not exist on the target. The NIC stays disconnected until you create it there.`,
	warning_target_missing_bridge: (d) =>
		`The network bridge${d ? ` \u201c${d}\u201d` : ''} does not exist on the target.`,
	warning_9p_share_not_migrated: (d) =>
		`The shared folder${d ? ` \u201c${d}\u201d` : ''} is not present on the target and will be skipped.`,
	warning_target_vnc_port_in_use: (d) =>
		`VNC port${d ? ` ${d}` : ''} is already in use on the target; a free port will be assigned.`,
	warning_stale_dataset_on_target: (d) =>
		`A leftover dataset${d ? ` \u201c${d}\u201d` : ''} already exists on the target and will be reused.`,
	warning_ownership_reassignment_failed:
		'The guest moved successfully, but updating cluster ownership (replication/HA) failed. The cluster may still treat the source node as the owner \u2014 check replication policies or re-run the migration.',
	warning_ownership_reassignment_skipped_no_guard:
		'Cluster ownership (replication/HA) was not reassigned because the cluster service was unavailable.'
};

// Codes that embed a dynamic segment (e.g. a node id) before the ":" detail.
const PREFIX_MESSAGES: Array<{ prefix: string; format: ReasonFormatter }> = [
	{
		prefix: 'target_guest_check_failed_',
		format: (d) => `Could not verify the guest on the target node${d ? `: ${d}` : ''}.`
	},
	{
		prefix: 'target_pool_check_failed_',
		format: (d) => `Could not verify a ZFS pool on the target node${d ? `: ${d}` : ''}.`
	}
];

function humanize(code: string): string {
	return code.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

export function formatMigrationReason(raw: string): string {
	if (!raw) return '';

	const idx = raw.indexOf(':');
	const code = (idx >= 0 ? raw.slice(0, idx) : raw).trim();
	const detail = (idx >= 0 ? raw.slice(idx + 1) : '').trim();

	const exact = REASON_MESSAGES[code];
	if (exact !== undefined) {
		return typeof exact === 'function' ? exact(detail) : exact;
	}

	for (const { prefix, format } of PREFIX_MESSAGES) {
		if (code.startsWith(prefix)) {
			return format(detail);
		}
	}

	if (/^network_\d+_switch_lookup_failed$/.test(code)) {
		return `Could not resolve a network switch for this VM${detail ? `: ${detail}` : ''}.`;
	}

	// Safe fallback: never show raw snake_case.
	const humanized = humanize(code);
	return detail ? `${humanized}: ${detail}` : humanized;
}

/**
 * Formats a "; "-joined list of reason codes (as produced by the backend when a
 * migration is hard-blocked) into a single readable sentence.
 */
export function formatMigrationReasons(raw: string): string {
	if (!raw) return '';
	return raw
		.split('; ')
		.map((part) => part.trim())
		.filter(Boolean)
		.map(formatMigrationReason)
		.join(' ');
}
