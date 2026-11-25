import { getZFSDashboardHistory, getZFSDashboardSnapshot } from '$lib/api/zfs/pool';
import { ui } from '$lib/stores/interface.svelte';
import { ZFSDashboardHistorySchema, ZFSDashboardSnapshotSchema } from '$lib/types/zfs/pool';
import { cachedFetch } from '$lib/utils/http';

const FIVE_MINUTES = 5 * 60 * 1000;
const FIVE_SECONDS = 5 * 1000;
const THIRTY_SECONDS = 30 * 1000;

export async function load({ params }) {
	const historySeconds = ui.zen ? 3600 : 86400;
	const [snapshot, history] = await Promise.all([
		cachedFetch(
			`zfs-dashboard-snapshot:${params.node}`,
			() => getZFSDashboardSnapshot({ hostname: params.node }),
			ui.zen ? THIRTY_SECONDS : FIVE_SECONDS,
			false,
			params.node,
			ZFSDashboardSnapshotSchema
		),
		cachedFetch(
			`zfs-dashboard-history:${params.node}:${historySeconds}:all`,
			() => getZFSDashboardHistory(historySeconds, '', 900, { hostname: params.node }),
			FIVE_MINUTES,
			false,
			params.node,
			ZFSDashboardHistorySchema
		)
	]);

	return {
		hostname: params.node,
		snapshot,
		history
	};
}
