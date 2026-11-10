import { getDetailsData, getNodes } from '$lib/api/cluster/cluster';
import { getCPUInfo } from '$lib/api/info/cpu';
import { getRAMInfo } from '$lib/api/info/ram';
import { getPoolsDiskUsageFull } from '$lib/api/zfs/pool';
import { isDemoMode } from '$lib/demo/runtime';
import { can, loadPermissions, permissions } from '$lib/stores/permissions.svelte';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';

export async function load() {
	if (!isDemoMode) await loadPermissions();
	// Everyone lands here after login, so users without host access get a pointer to the tree.
	if (permissions.data && !can('Sys.Audit', '/')) return { limited: true as const };

	const cacheDuration = SEVEN_DAYS;
	const [nodes, details, cpu, ram, disk] = await Promise.all([
		cachedFetch('cluster-nodes', async () => getNodes(), cacheDuration),
		cachedFetch('cluster-details', async () => getDetailsData(), cacheDuration),
		cachedFetch('cpu-info', async () => getCPUInfo(), cacheDuration),
		cachedFetch('ram-info', async () => getRAMInfo(), cacheDuration),
		cachedFetch('total-disk-usage', async () => getPoolsDiskUsageFull(), cacheDuration)
	]);

	return {
		limited: false as const,
		nodes: nodes,
		details: details,
		cpu: cpu,
		ram: ram,
		disk: disk
	};
}
