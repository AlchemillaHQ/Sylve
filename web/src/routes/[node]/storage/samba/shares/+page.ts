import { listGroups } from '$lib/api/auth/groups';
import { listUsers } from '$lib/api/auth/local';
import { getSambaConfig } from '$lib/api/samba/config';
import { getSambaShares } from '$lib/api/samba/share';
import { getDatasets } from '$lib/api/zfs/datasets';
import type { APIResponse } from '$lib/types/common';
import { SambaShareSchema } from '$lib/types/samba/shares';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';

export async function load({ params }) {
	const node = params.node;
	const cacheDuration = SEVEN_DAYS;
	const [datasets, shares, groupsResult, usersResult, sambaConfig] = await Promise.all([
		cachedFetch(
			'zfs-filesystems',
			async () => await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM, { hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'samba-shares',
			async () => await getSambaShares({ hostname: node }),
			cacheDuration,
			false,
			node,
			SambaShareSchema.array()
		),
		cachedFetch(
			'groups',
			async () => await listGroups({ hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'users',
			async () => await listUsers(undefined, { hostname: node }),
			cacheDuration,
			false,
			node
		),
		cachedFetch(
			'samba-config',
			async () => await getSambaConfig({ hostname: node }),
			cacheDuration,
			false,
			node
		)
	]);

	const loadErrors: APIResponse[] = [];
	if (isAPIResponse(groupsResult)) loadErrors.push(groupsResult);
	if (isAPIResponse(usersResult)) loadErrors.push(usersResult);

	return {
		node,
		datasets,
		shares,
		groups: isAPIResponse(groupsResult) ? [] : groupsResult,
		users: isAPIResponse(usersResult) ? [] : usersResult,
		sambaConfig,
		loadErrors
	};
}
