<script lang="ts">
	import { getDetails, getNodes } from '$lib/api/cluster/cluster';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getRAMInfo } from '$lib/api/info/ram';
	import { getPoolsDiskUsageFull } from '$lib/api/zfs/pool';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { PoolsDiskUsage } from '$lib/types/zfs/pool';
	import { reload } from '$lib/stores/api.svelte';
	import { getQuorumStatus } from '$lib/utils/cluster';
	import { updateCache } from '$lib/utils/http';
	import { capitalizeFirstLetter } from '$lib/utils/string';
	import { dateToAgo } from '$lib/utils/time';
	import humanFormat from 'human-format';
	import { resource, watch } from 'runed';
	import { onMount } from 'svelte';
	import { fade } from 'svelte/transition';

	interface Data {
		nodes: ClusterNode[];
		details: ClusterDetails;
		cpu: CPUInfo;
		ram: RAMInfo;
		disk: PoolsDiskUsage;
	}

	let { data }: { data: Data } = $props();
	let lazyArc = $state<Promise<typeof import('$lib/components/custom/Charts/Arc.svelte')> | null>(
		null
	);

	// svelte-ignore state_referenced_locally
	let nodes = resource(
		() => 'cluster-nodes',
		async (key, prevKey, { signal }) => {
			const result = await getNodes();
			updateCache('cluster-nodes', result);
			return result;
		},
		{
			initialValue: data.nodes
		}
	);

	// svelte-ignore state_referenced_locally
	let clusterDetails = resource(
		() => 'cluster-details',
		async (key, prevKey, { signal }) => {
			const result = await getDetails();
			updateCache('cluster-details', result);
			return result;
		},
		{
			initialValue: data.details
		}
	);

	// svelte-ignore state_referenced_locally
	let cpuInfo = resource(
		() => 'cpu-info',
		async (key, prevKey, { signal }) => {
			const result = await getCPUInfo('current');
			updateCache('cpu-info', result);
			return result;
		},
		{
			initialValue: data.cpu
		}
	);

	// svelte-ignore state_referenced_locally
	let ramInfo = resource(
		() => 'ram-info',
		async (key, prevKey, { signal }) => {
			const result = await getRAMInfo('current');
			updateCache('ram-info', result);
			return result;
		},
		{
			initialValue: data.ram
		}
	);

	// svelte-ignore state_referenced_locally
	let diskInfo = resource(
		() => 'total-disk-usage',
		async (key, prevKey, { signal }) => {
			const result = await getPoolsDiskUsageFull();
			updateCache('total-disk-usage', result);
			return result;
		},
		{
			initialValue: data.disk
		}
	);

	let clustered = $derived.by(() => {
		return clusterDetails?.current.cluster.enabled ?? false;
	});

	function clampPercent(value: number | null | undefined): number {
		const safe = Number(value ?? 0);
		if (!Number.isFinite(safe)) return 0;
		return Math.min(100, Math.max(0, safe));
	}

	function safeUsage(used: number, total: number): number {
		if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0) return 0;
		return clampPercent((used / total) * 100);
	}

	let total = $derived.by(() => {
		const onlineNodes = nodes.current.filter(
			(node) => String(node.status || '').toLowerCase() === 'online'
		);

		if (onlineNodes.length === 0) {
			return {
				cpu: { total: 0, usage: 0 },
				ram: { total: 0, usage: 0 },
				disk: { total: 0, usage: 0 }
			};
		}

		const localNodeId = clusterDetails.current?.nodeId;
		const normalizedNodes = onlineNodes.map((node) => {
			if (node.nodeUUID !== localNodeId) return node;

			return {
				...node,
				// Use live local CPU stats to avoid stale 0% values from cluster snapshots.
				cpu: node.cpu > 0 ? node.cpu : (cpuInfo.current?.logicalCores ?? 0),
				cpuUsage: clampPercent(cpuInfo.current?.usage ?? node.cpuUsage)
			};
		});

		const totalCPUs = normalizedNodes.reduce((acc, node) => acc + Math.max(0, node.cpu ?? 0), 0);
		const used = normalizedNodes.reduce(
			(acc, node) =>
				acc + (Math.max(0, node.cpu ?? 0) * clampPercent(node.cpuUsage ?? 0)) / 100,
			0
		);

		const totalMemory = normalizedNodes.reduce((acc, node) => acc + Math.max(0, node.memory ?? 0), 0);
		const usedMemory = normalizedNodes.reduce(
			(acc, node) => acc + (Math.max(0, node.memory ?? 0) * clampPercent(node.memoryUsage ?? 0)) / 100,
			0
		);

		const totalDisk = normalizedNodes.reduce((acc, node) => acc + Math.max(0, node.disk ?? 0), 0);
		const usedDisk = normalizedNodes.reduce(
			(acc, node) => acc + (Math.max(0, node.disk ?? 0) * clampPercent(node.diskUsage ?? 0)) / 100,
			0
		);

		return {
			cpu: {
				total: totalCPUs,
				usage: safeUsage(used, totalCPUs)
			},
			ram: {
				total: totalMemory,
				usage: safeUsage(usedMemory, totalMemory)
			},
			disk: {
				total: totalDisk,
				usage: safeUsage(usedDisk, totalDisk)
			}
		};
	});

	let quorumStatus = $derived(getQuorumStatus(clusterDetails.current, nodes.current));
	let statusCounts = $derived.by(() => {
		return nodes.current.reduce(
			(acc, node) => {
				acc[node.status] = (acc[node.status] || 0) + 1;
				return acc;
			},
			{} as Record<string, number>
		);
	});

	watch(
		() => reload.leftPanel,
		(shouldReload) => {
			if (!shouldReload) return;

			nodes.refetch();
			clusterDetails.refetch();
		}
	);

	watch(
		() => reload.clusterDetails,
		(shouldReload) => {
			if (!shouldReload) return;

			clusterDetails.refetch();
		}
	);

	onMount(() => {
		lazyArc = import('$lib/components/custom/Charts/Arc.svelte');
	});

	const resourceGridClass = 'grid grid-cols-1 gap-2 md:grid-cols-3';
	const resourceSlotClass = 'flex items-center justify-center';
	const resourceChartBoxClass = 'h-35 w-50';
</script>

<div class="flex h-full w-full flex-col space-y-4">
	<div class="px-4 pt-4">
		<Card.Root class="gap-2">
			<Card.Header>
				<Card.Title>
					<div class="flex items-center gap-2">
						<span class="icon-[solar--health-bold] min-h-4 min-w-4"></span>
						<span>Health</span>
					</div>
				</Card.Title>
			</Card.Header>
			<Card.Content>
				<div class="flex items-start justify-center gap-8">
					<div class="flex flex-1 flex-col items-center space-y-2 text-center">
						<span class="text-xl font-bold">Status</span>
						{#if !clustered}
							<span class="icon-[mdi--check-circle] h-12 w-12 text-green-500"></span>

							<span class="text-sm font-semibold">Single Node</span>
						{:else if quorumStatus === 'ok'}
							<span class="icon-[mdi--check-circle] h-12 w-12 text-green-500"></span>
							<span class="text-sm font-semibold">Quorate: Yes</span>
						{:else if quorumStatus === 'warning'}
							<span class="icon-[material-symbols--warning] h-12 w-12 text-yellow-500"></span>
							<span class="text-sm font-semibold">Quorate: Yes (Degraded)</span>
						{:else}
							<span class="icon-[mdi--close-circle] h-12 w-12 text-red-500"></span>
							<span class="text-sm font-semibold">Quorate: No</span>
						{/if}
					</div>

					<div class="flex flex-1 flex-col items-center space-y-2 text-center">
						<span class="text-xl font-bold">Nodes</span>

						<div class="flex items-center gap-2">
							<span class="icon-[mdi--check-circle] h-5 w-5 text-green-500"></span>
							{#if clustered}
								<span class="text-md font-semibold">Online: {statusCounts.online || 0}</span>
							{:else}
								<span class="text-md font-semibold">Online: 1</span>
							{/if}
						</div>

						<div class="flex items-center gap-2">
							<span class="icon-[mdi--close-circle] h-5 w-5 text-red-500"></span>
							{#if clustered}
								<span class="text-md font-semibold">Offline: {statusCounts.offline || 0}</span>
							{:else}
								<span class="text-md font-semibold">Offline: N/A</span>
							{/if}
						</div>
					</div>
				</div>
			</Card.Content>
		</Card.Root>
	</div>

	<div class="px-4">
		<Card.Root class="gap-2 pb-0">
			<Card.Header>
				<Card.Title>
					<div class="flex items-center gap-2">
						<span class="icon-[clarity--resource-pool-solid] min-h-4 min-w-4"></span>
						<span>Resources</span>
					</div>
				</Card.Title>
			</Card.Header>
			<Card.Content>
				{#if lazyArc}
					{#await lazyArc}
						<div class={resourceGridClass} aria-live="polite">
							{#each ['CPU', 'RAM', 'Disk'] as metric}
								<div class={resourceSlotClass}>
									<div
										class={`${resourceChartBoxClass} flex flex-col items-center justify-center gap-2`}
									>
										<div
											class="h-24 w-24 animate-pulse rounded-full border-8 border-muted bg-muted/30"
										></div>
										<div class="h-3.5 w-14 animate-pulse rounded bg-muted/70"></div>
										<div class="h-3 w-18 animate-pulse rounded bg-muted/40"></div>
										<span class="sr-only">Loading {metric} chart...</span>
									</div>
								</div>
							{/each}
						</div>
					{:then { default: Arc }}
						<div class={resourceGridClass} in:fade={{ duration: 200 }}>
							{#if clustered}
								<div class={resourceSlotClass}>
									<div class={resourceChartBoxClass}>
										<Arc value={total.cpu.usage} title="CPU" subtitle="{total.cpu.total} vCPUs" />
									</div>
								</div>
								<div class={resourceSlotClass}>
									<div class={resourceChartBoxClass}>
										<Arc
											value={total.ram.usage}
											title="RAM"
											subtitle={humanFormat(total.ram.total)}
										/>
									</div>
								</div>
								<div class={resourceSlotClass}>
									<div class={resourceChartBoxClass}>
										<Arc
											value={total.disk.usage}
											subtitle={humanFormat(total.disk.total)}
											title="Disk"
										/>
									</div>
								</div>
							{:else}
								<div class={resourceSlotClass}>
									<div class={resourceChartBoxClass}>
										<Arc
											value={cpuInfo.current?.usage}
											title="CPU"
											subtitle="{cpuInfo.current?.logicalCores} vCPUs"
										/>
									</div>
								</div>
								<div class={resourceSlotClass}>
									<div class={resourceChartBoxClass}>
										<Arc
											value={ramInfo.current?.usedPercent}
											title="RAM"
											subtitle={humanFormat(ramInfo.current?.total || 0)}
										/>
									</div>
								</div>
								<div class={resourceSlotClass}>
									<div class={resourceChartBoxClass}>
										<Arc
											value={diskInfo.current?.usage || 0}
											title="Disk"
											subtitle={humanFormat(diskInfo.current?.total || 0)}
										/>
									</div>
								</div>
							{/if}
						</div>
					{:catch}
						<div
							class={`${resourceChartBoxClass} mx-auto place-content-center text-center text-sm text-muted-foreground`}
						>
							Unable to load charts.
						</div>
					{/await}
				{/if}
			</Card.Content>
		</Card.Root>
	</div>

	{#if clustered}
		<div class="px-4 pb-4">
			<Card.Root class="gap-2">
				<Card.Header>
					<Card.Title>
						<div class="flex items-center gap-2">
							<span class="icon-[fa7-solid--hexagon-nodes] min-h-4 min-w-4"></span>
							<span>Nodes</span>
						</div>
					</Card.Title>
				</Card.Header>
				<Card.Content>
					<Table.Root>
						<Table.Header>
							<Table.Row>
								<Table.Head>Status</Table.Head>
								<Table.Head>Hostname</Table.Head>
								<Table.Head>ID</Table.Head>
								<Table.Head>Last Ping</Table.Head>
							</Table.Row>
						</Table.Header>
						<Table.Body>
							{#each nodes.current as node (node.id)}
								<Table.Row>
									<Table.Cell>
										<Badge variant="outline" class="text-muted-foreground px-1.5">
											{#if node.status === 'online'}
												<span class="icon-[mdi--check-circle] text-green-500"></span>
											{:else}
												<span class="icon-[mdi--close-circle] text-red-500"></span>
											{/if}
											{capitalizeFirstLetter(node.status)}
										</Badge>
									</Table.Cell>
									<Table.Cell>{node.hostname}</Table.Cell>
									<Table.Cell>{node.nodeUUID}</Table.Cell>
									<Table.Cell>{dateToAgo(node.updatedAt)}</Table.Cell>
								</Table.Row>
							{/each}
						</Table.Body>
					</Table.Root>
				</Card.Content>
			</Card.Root>
		</div>
	{/if}
</div>
