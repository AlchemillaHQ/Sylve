import { getDownloadsResult } from '$lib/api/utilities/downloader';
import { getVmByIdResult, getVMsResult } from '$lib/api/vm/vm';
import { getDatasetsResult } from '$lib/api/zfs/datasets';
import { getPoolsResult } from '$lib/api/zfs/pool';
import type { APIResponse } from '$lib/types/common';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const rid = Number(params.rid);
	const node = params.node;
	if (!Number.isSafeInteger(rid) || rid <= 0) {
		error(404, 'Invalid VM RID');
	}

	const [vmsResult, vmResult, filesystemsResult, volumesResult, poolsResult, downloadsResult] =
		await Promise.all([
			cachedFetch(
				'vm-list',
				async () => getVMsResult({ hostname: node }),
				cacheDuration,
				false,
				node
			),
			cachedFetch(
				`vm-${rid}`,
				async () => getVmByIdResult(rid, { hostname: node }),
				cacheDuration,
				false,
				node
			),
			cachedFetch(
				'zfs-filesystems',
				async () => getDatasetsResult(GZFSDatasetTypeSchema.enum.FILESYSTEM, node),
				cacheDuration,
				false,
				node
			),
			cachedFetch(
				'zfs-volumes',
				async () => getDatasetsResult(GZFSDatasetTypeSchema.enum.VOLUME, node),
				cacheDuration,
				false,
				node
			),
			cachedFetch(
				'pool-list',
				async () => getPoolsResult(false, { hostname: node }),
				cacheDuration,
				false,
				node
			),
			cachedFetch(
				'download-list',
				async () => getDownloadsResult({ hostname: node }),
				cacheDuration,
				false,
				node
			)
		]);
	if (isAPIResponse(vmResult)) {
		const vmError = Array.isArray(vmResult.error) ? vmResult.error.join(', ') : vmResult.error;
		error(502, vmError || vmResult.message || 'Failed to load virtual machine');
	}

	const loadErrors = [
		vmsResult,
		filesystemsResult,
		volumesResult,
		poolsResult,
		downloadsResult
	].filter(isAPIResponse) as APIResponse[];

	return {
		node,
		rid,
		vms: isAPIResponse(vmsResult) ? [] : vmsResult,
		vm: vmResult,
		filesystems: isAPIResponse(filesystemsResult) ? [] : filesystemsResult,
		volumes: isAPIResponse(volumesResult) ? [] : volumesResult,
		pools: isAPIResponse(poolsResult) ? [] : poolsResult,
		downloads: isAPIResponse(downloadsResult) ? [] : downloadsResult,
		loadErrors
	};
}
