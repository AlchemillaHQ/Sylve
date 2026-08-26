/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { BackupJob, BackupTarget } from '../types/cluster/backups';
import type { ClusterDetails, ClusterNode } from '../types/cluster/cluster';

export type BackupJobsClusterMode = 'standalone' | 'clustered' | 'unavailable';

export type BackupJobsPageAvailability = {
	cluster: boolean;
	nodes: boolean;
	targets: boolean;
	jobs: boolean;
};

export type BackupJobsPageData = {
	targets: BackupTarget[];
	jobs: BackupJob[];
	nodes: ClusterNode[];
	localNodeId: string;
	clusterMode: BackupJobsClusterMode;
	availability: BackupJobsPageAvailability;
};

export type BackupJobsPageResults = {
	targets: unknown;
	jobs: unknown;
	nodes: unknown;
	details: unknown;
	basicInfo: unknown;
};

export function isPositiveSafeInteger(value: number): boolean {
	return Number.isSafeInteger(value) && value > 0;
}

export function parsePositiveSafeInteger(value: string): number | null {
	if (!/^[0-9]+$/.test(value)) return null;

	const parsed = Number(value);
	return isPositiveSafeInteger(parsed) ? parsed : null;
}

export function backupJobsUnavailableSources(
	clusterMode: BackupJobsClusterMode,
	availability: BackupJobsPageAvailability
): string[] {
	const sources: string[] = [];
	if (clusterMode === 'unavailable') sources.push('cluster state');
	if (clusterMode === 'clustered' && !availability.nodes) sources.push('cluster nodes');
	if (!availability.targets) sources.push('backup targets');
	if (!availability.jobs) sources.push('backup jobs');
	return sources;
}

function syntheticLocalNode(nodeUUID: string, hostname: string): ClusterNode {
	const nowISO = new Date().toISOString();

	return {
		id: 0,
		nodeUUID,
		status: 'online',
		hostname,
		api: '',
		cpu: 0,
		cpuUsage: 0,
		memory: 0,
		memoryUsage: 0,
		disk: 0,
		diskUsage: 0,
		createdAt: nowISO,
		updatedAt: nowISO,
		guestIDs: []
	};
}

function asClusterDetails(value: unknown): ClusterDetails | null {
	if (!value || typeof value !== 'object') return null;

	const candidate = value as {
		nodeId?: unknown;
		cluster?: { enabled?: unknown };
	};
	if (
		typeof candidate.nodeId !== 'string' ||
		candidate.nodeId.trim() === '' ||
		!candidate.cluster ||
		typeof candidate.cluster.enabled !== 'boolean'
	) {
		return null;
	}

	return value as ClusterDetails;
}

function asArray<T>(value: unknown): T[] | null {
	return Array.isArray(value) ? (value as T[]) : null;
}

function hostnameFromBasicInfo(value: unknown): string {
	if (!value || typeof value !== 'object' || !('hostname' in value)) return 'Local node';

	const hostname = (value as { hostname?: unknown }).hostname;
	return typeof hostname === 'string' && hostname.trim() !== '' ? hostname.trim() : 'Local node';
}

export function resolveBackupJobsPageData(results: BackupJobsPageResults): BackupJobsPageData {
	const targets = asArray<BackupTarget>(results.targets);
	const jobs = asArray<BackupJob>(results.jobs);
	const nodes = asArray<ClusterNode>(results.nodes);
	const details = asClusterDetails(results.details);
	const localNodeId = details?.nodeId.trim() || '';

	const clusterMode: BackupJobsClusterMode = !details
		? 'unavailable'
		: details.cluster.enabled
			? 'clustered'
			: 'standalone';
	const effectiveNodes =
		clusterMode === 'standalone'
			? [syntheticLocalNode(localNodeId, `${hostnameFromBasicInfo(results.basicInfo)} (Local)`)]
			: (nodes ?? []);

	return {
		targets: targets ?? [],
		jobs: jobs ?? [],
		nodes: effectiveNodes,
		localNodeId,
		clusterMode,
		availability: {
			cluster: details !== null,
			nodes: nodes !== null,
			targets: targets !== null,
			jobs: jobs !== null
		}
	};
}
