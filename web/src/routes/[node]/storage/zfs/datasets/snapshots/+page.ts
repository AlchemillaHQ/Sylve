import { basicSettingsOrFallback, getBasicSettings } from '$lib/api/system/settings';
import {
	getPeriodicSnapshots,
	getPeriodicSnapshotsResult,
	getSnapshotSummariesResult
} from '$lib/api/zfs/datasets';
import { ui } from '$lib/stores/interface.svelte';
import { canReadHost } from '$lib/stores/permissions.svelte';
import {
	PeriodicSnapshotSchema,
	SnapshotSummarySchema,
	type SnapshotSummary
} from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const hostname = params.node;
	if (ui.zen) {
		if (!(await canReadHost(hostname))) {
			return {
				hostname,
				denied: true,
				snapshots: [],
				periodicSnapshots: [],
				basicSettings: { pools: [], services: [], initialized: false },
				fresh: false
			};
		}
		let fresh = false;
		const [snapshots, periodicSnapshots] = await Promise.all([
			cachedFetch(
				'zfs-snapshot-summaries',
				() => {
					fresh = true;
					return getSnapshotSummariesResult(hostname);
				},
				SEVEN_DAYS,
				false,
				hostname,
				SnapshotSummarySchema.array()
			),
			cachedFetch(
				'zfs-periodic-snapshots',
				() => getPeriodicSnapshotsResult(hostname),
				SEVEN_DAYS,
				false,
				hostname,
				PeriodicSnapshotSchema.array()
			)
		]);
		const pools = Array.isArray(snapshots)
			? [...new Set(snapshots.map((snapshot) => snapshot.pool))]
			: [];
		return {
			hostname,
			denied: false,
			snapshots,
			periodicSnapshots,
			basicSettings: { pools, services: [], initialized: true },
			fresh
		};
	}

	const [basicSettings, periodicSnapshots] = await Promise.all([
		cachedFetch('basic-settings', () => getBasicSettings(), SEVEN_DAYS),
		cachedFetch('zfs-periodic-snapshots', async () => await getPeriodicSnapshots(), SEVEN_DAYS)
	]);

	return {
		hostname,
		denied: false,
		snapshots: [] as SnapshotSummary[],
		periodicSnapshots,
		basicSettings: basicSettingsOrFallback(basicSettings),
		fresh: true
	};
};
