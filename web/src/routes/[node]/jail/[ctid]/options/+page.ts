import { getJailByCTID } from '$lib/api/jail/jail';
import { getBasicInfoResult } from '$lib/api/info/basic';
import { getDatasetsResult } from '$lib/api/zfs/datasets';
import type { APIResponse } from '$lib/types/common';
import type { BasicInfo } from '$lib/types/info/basic';
import type { Jail } from '$lib/types/jail/jail';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import { error } from '@sveltejs/kit';

export async function load({ params }) {
	const cacheDuration = SEVEN_DAYS;
	const ctId = Number(params.ctid);
	const node = String(params.node);
	if (!Number.isSafeInteger(ctId) || ctId <= 0) {
		error(404, 'Invalid jail CTID');
	}

	const [jailResult, basicInfoResult, filesystemsResult] = await Promise.all([
		cachedFetch(
			`jail-${ctId}`,
			async () => getJailByCTID(ctId, { hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'basic-info',
			async () => getBasicInfoResult({ hostname: node }),
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
		)
	]);
	const jailError = (isAPIResponse(jailResult) ? jailResult : null) as APIResponse | null;
	const jail = (isAPIResponse(jailResult) ? null : jailResult) as Jail | null;
	const basicInfoError = (
		isAPIResponse(basicInfoResult) ? basicInfoResult : null
	) as APIResponse | null;
	const basicInfo = (isAPIResponse(basicInfoResult) ? null : basicInfoResult) as BasicInfo | null;
	const filesystemsError = (
		isAPIResponse(filesystemsResult) ? filesystemsResult : null
	) as APIResponse | null;

	return {
		node,
		ctId,
		jail,
		jailError,
		devFSDisabled: basicInfo?.devFSDisabled ?? false,
		basicInfoError,
		filesystems: isAPIResponse(filesystemsResult) ? [] : filesystemsResult,
		filesystemsError
	};
}
