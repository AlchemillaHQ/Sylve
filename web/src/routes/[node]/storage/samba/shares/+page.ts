import { listGroups } from '$lib/api/auth/groups';
import { listUsers } from '$lib/api/auth/local';
import { getSambaConfig } from '$lib/api/samba/config';
import { getSambaShares, getSambaSharesResult, getSambaStatus } from '$lib/api/samba/share';
import { getDatasets } from '$lib/api/zfs/datasets';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import type { APIResponse } from '$lib/types/common';
import { SambaShareSchema, SambaStatusSchema } from '$lib/types/samba/shares';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch, isAPIResponse } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const node = params.node;
	if (ui.zen) {
		if (!(await canReadHost(node))) {
			return {
				zen: true as const,
				hostname: node,
				denied: true,
				shares: [],
				fresh: false,
				status: null,
				statusFresh: false
			};
		}
		let fresh = false;
		let statusFresh = false;
		const [shares, status] = await Promise.all([
			cachedFetch(
				'samba-shares',
				() => {
					fresh = true;
					return getSambaSharesResult({ hostname: node });
				},
				SEVEN_DAYS,
				false,
				node,
				SambaShareSchema.array()
			),
			cachedFetch(
				'samba-status',
				() => {
					statusFresh = true;
					return getSambaStatus({ hostname: node });
				},
				SEVEN_DAYS,
				false,
				node,
				SambaStatusSchema
			)
		]);
		return {
			zen: true as const,
			hostname: node,
			denied: false,
			shares,
			fresh,
			status,
			statusFresh
		};
	}

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
		zen: false as const,
		node,
		datasets,
		shares,
		groups: isAPIResponse(groupsResult) ? [] : groupsResult,
		users: isAPIResponse(usersResult) ? [] : usersResult,
		sambaConfig,
		loadErrors
	};
};
