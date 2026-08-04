import { getZFSDashboardHistory, getZFSDashboardSnapshot } from '$lib/api/zfs/pool';
import { cachedFetch } from '$lib/utils/http';

const FIVE_MINUTES = 5 * 60 * 1000;
const FIVE_SECONDS = 5 * 1000;

export async function load({ params }) {
	const [snapshot, history] = await Promise.all([
		cachedFetch(
			`zfs-dashboard-snapshot:${params.node}`,
			() => getZFSDashboardSnapshot({ hostname: params.node }),
			FIVE_SECONDS
		),
		cachedFetch(
			`zfs-dashboard-history:${params.node}:86400:all`,
			() => getZFSDashboardHistory(86400, '', 900, { hostname: params.node }),
			FIVE_MINUTES
		)
	]);

	return {
		hostname: params.node,
		snapshot,
		history
	};
}
