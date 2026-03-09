import { listBackupJobs, listBackupTargets } from '$lib/api/cluster/backups';
import { getDetails, getNodes } from '$lib/api/cluster/cluster';
import { getBasicInfo } from '$lib/api/info/basic';
import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
import type { BackupJob, BackupTarget } from '$lib/types/cluster/backups';
import { cachedFetch } from '$lib/utils/http';

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
	if (!value || typeof value !== 'object' || !('nodeId' in value)) {
		return null;
	}

	const details = value as ClusterDetails;
	return typeof details.nodeId === 'string' && details.nodeId.trim() !== '' ? details : null;
}

export async function load() {
	const [targets, jobs, nodes, details, basicInfo] = await Promise.all([
		cachedFetch('backup-targets', async () => listBackupTargets(), 1000),
		cachedFetch('backup-jobs', async () => listBackupJobs(), 1000),
		cachedFetch('cluster-nodes', async () => getNodes(), 1000),
		cachedFetch('cluster-details', async () => getDetails(), 1000),
		cachedFetch('basic-info', async () => getBasicInfo(), 1000)
	]);

	const nodeRows = nodes as ClusterNode[];
	const clusterDetails = asClusterDetails(details);
	const localNodeId = clusterDetails?.nodeId?.trim() || '';
	const localHostname =
		(typeof basicInfo === 'object' &&
			basicInfo !== null &&
			'hostname' in basicInfo &&
			typeof basicInfo.hostname === 'string' &&
			basicInfo.hostname.trim() !== '' &&
			basicInfo.hostname.trim()) ||
		'Local node';

	const standaloneMode = nodeRows.length === 0;
	const effectiveNodes =
		standaloneMode
			? [syntheticLocalNode(localNodeId, `${localHostname} (Local)`)]
			: nodeRows;

	return {
		targets: targets as BackupTarget[],
		jobs: jobs as BackupJob[],
		nodes: effectiveNodes,
		localNodeId,
		standaloneMode
	};
}
