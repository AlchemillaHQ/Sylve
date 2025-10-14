<script lang="ts">
	import { getDetails, getNodes } from '$lib/api/cluster/cluster';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getRAMInfo } from '$lib/api/info/ram';
	import { getPoolsDiskUsage, getPoolsDiskUsageFull } from '$lib/api/zfs/pool';
	import Arc from '$lib/components/custom/Charts/Arc.svelte';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import type { CPUInfo, CPUInfoHistorical } from '$lib/types/info/cpu';
	import type { RAMInfo, RAMInfoHistorical } from '$lib/types/info/ram';
	import type { PoolsDiskUsage } from '$lib/types/zfs/pool';
	import { getQuorumStatus } from '$lib/utils/cluster';
	import { updateCache } from '$lib/utils/http';
	import { capitalizeFirstLetter } from '$lib/utils/string';
	import { dateToAgo } from '$lib/utils/time';
	import Icon from '@iconify/svelte';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import humanFormat from 'human-format';

	interface Data {
		nodes: ClusterNode[];
		details: ClusterDetails;
		cpu: CPUInfo;
		ram: RAMInfo;
		disk: PoolsDiskUsage;
	}

	let { data }: { data: Data } = $props();

	const queryClient = useQueryClient();
	let results = useQueries([
		{
			queryKey: 'cluster-nodes',
			queryFn: async () => {
				return (await getNodes()) as ClusterNode[];
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.nodes,
			refetchOnMount: 'always',
			onSuccess: (data: ClusterNode[]) => {
				updateCache('cluster-nodes', data);
			}
		},
		{
			queryKey: 'cluster-details',
			queryFn: async () => {
				return (await getDetails()) as ClusterDetails;
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.details,
			refetchOnMount: 'always',
			onSuccess: (data: ClusterDetails) => {
				updateCache('cluster-details', data);
			}
		},
		{
			queryKey: 'cpu-info',
			queryFn: getCPUInfo,
			keepPreviousData: true,
			initialData: data.cpu,
			refetchOnMount: 'always',
			onSuccess: (data: CPUInfo | CPUInfoHistorical) => {
				updateCache('cpu-info', data as CPUInfo);
			}
		},
		{
			queryKey: 'ram-info',
			queryFn: getRAMInfo,
			keepPreviousData: true,
			initialData: data.ram,
			onSuccess: (data: RAMInfo | RAMInfoHistorical) => {
				updateCache('ram-info', data);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: 'total-disk-usage',
			queryFn: getPoolsDiskUsageFull,
			keepPreviousData: true,
			initialData: data.disk,
			onSuccess: (data: PoolsDiskUsage) => {
				updateCache('total-disk-usage', data);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		}
	]);

	let nodes = $derived($results[0].data ?? []);
	let clusterDetails = $derived($results[1].data);
	let cpuInfo = $derived($results[2].data as CPUInfo);
	let ramInfo = $derived($results[3].data as RAMInfo);
	let diskInfo = $derived($results[4].data as PoolsDiskUsage);
	let clustered = $derived(clusterDetails?.cluster.enabled || false);

	let total = $derived.by(() => {
		if (nodes.length === 0) {
			return {
				cpu: { total: 0, usage: 0 },
				ram: { total: 0, usage: 0 },
				disk: { total: 0, usage: 0 }
			};
		}

		const totalCPUs = nodes.reduce((acc, node) => acc + node.cpu, 0);
		const used = nodes.reduce((acc, node) => acc + (node.cpu * node.cpuUsage) / 100, 0);

		const totalMemory = nodes.reduce((acc, node) => acc + node.memory, 0);
		const usedMemory = nodes.reduce(
			(acc, node) => acc + ((node.memory ?? 0) * (node.memoryUsage ?? 0)) / 100,
			0
		);

		const totalDisk = nodes.reduce((acc, node) => acc + node.disk, 0);
		const usedDisk = nodes.reduce((acc, node) => acc + (node.disk * node.diskUsage) / 100, 0);

		return {
			cpu: {
				total: totalCPUs,
				usage: (used / totalCPUs) * 100
			},
			ram: {
				total: totalMemory,
				usage: (usedMemory / totalMemory) * 100
			},
			disk: {
				total: totalDisk,
				usage: (usedDisk / totalDisk) * 100
			}
		};
	});

	let quorumStatus = $derived(getQuorumStatus(clusterDetails as ClusterDetails, nodes));
	let statusCounts = $derived.by(() => {
		return nodes.reduce(
			(acc, node) => {
				acc[node.status] = (acc[node.status] || 0) + 1;
				return acc;
			},
			{} as Record<string, number>
		);
	});
</script>

<div class="flex h-full w-full flex-col space-y-4">
	<div class="px-4 pt-4">
		<Card.Root class="gap-2">
			<Card.Header>
				<Card.Title>
					<div class="flex items-center gap-2">
						<Icon icon="solar:health-bold" />
						<span>Health</span>
					</div>
				</Card.Title>
			</Card.Header>
			<Card.Content>
				<div class="flex items-start justify-center gap-8">
					<div class="flex flex-1 flex-col items-center space-y-2 text-center">
						<span class="text-xl font-bold">Status</span>
						{#if !clustered}
							<Icon icon="mdi:check-circle" class="h-12 w-12 text-green-500" />
							<span class="text-sm font-semibold">Single Node</span>
						{:else if quorumStatus === 'ok'}
							<Icon icon="mdi:check-circle" class="h-12 w-12 text-green-500" />
							<span class="text-sm font-semibold">Quorate: Yes</span>
						{:else if quorumStatus === 'warning'}
							<Icon icon="material-symbols:warning" class="h-12 w-12 text-yellow-500" />
							<span class="text-sm font-semibold">Quorate: Yes (Degraded)</span>
						{:else}
							<Icon icon="mdi:close-circle" class="h-12 w-12 text-red-500" />
							<span class="text-sm font-semibold">Quorate: No</span>
						{/if}
					</div>

					<div class="flex flex-1 flex-col items-center space-y-2 text-center">
						<span class="text-xl font-bold">Nodes</span>

						<div class="flex items-center gap-2">
							<Icon icon="mdi:check-circle" class="h-5 w-5 text-green-500" />
							{#if clustered}
								<span class="text-md font-semibold">Online: {statusCounts.online || 0}</span>
							{:else}
								<span class="text-md font-semibold">Online: 1</span>
							{/if}
						</div>

						<div class="flex items-center gap-2">
							<Icon icon="mdi:close-circle" class="h-5 w-5 text-red-500" />
							{#if clustered}
								<span class="text-md font-semibold">Offline: {statusCounts.offline || 0}</span>
							{:else}
								<span class="text-md font-semibold">Offline: N/A</span>
							{/if}
						</div>
					</div>
				</div>
			</Card.Content>
			<Card.Footer></Card.Footer>
		</Card.Root>
	</div>

	<div class="px-4">
		<Card.Root class="gap-2">
			<Card.Header>
				<Card.Title>
					<div class="flex items-center gap-2">
						<Icon icon="clarity:resource-pool-solid" />
						<span>Resources</span>
					</div>
				</Card.Title>
			</Card.Header>
			<Card.Content>
				<div class="flex items-center justify-center">
					{#if clustered}
						<div class="flex flex-1 justify-center">
							<Arc value={total.cpu.usage} title="CPU" subtitle="{total.cpu.total} vCPUs" />
						</div>
						<div class="flex flex-1 justify-center">
							<Arc value={total.ram.usage} title="RAM" subtitle={humanFormat(total.ram.total)} />
						</div>
						<div class="flex flex-1 justify-center">
							<Arc value={total.disk.usage} subtitle={humanFormat(total.disk.total)} title="Disk" />
						</div>
					{:else}
						<div class="flex flex-1 justify-center">
							<Arc value={cpuInfo?.usage} title="CPU" subtitle="{cpuInfo.physicalCores} vCPUs" />
						</div>
						<div class="flex flex-1 justify-center">
							<Arc
								value={ramInfo?.usedPercent}
								title="RAM"
								subtitle={humanFormat(ramInfo?.total || 0)}
							/>
						</div>
						<div class="flex flex-1 justify-center">
							<Arc
								value={diskInfo?.usage || 0}
								title="Disk"
								subtitle={humanFormat(diskInfo?.total || 0)}
							/>
						</div>
					{/if}
				</div>
			</Card.Content>
			<Card.Footer></Card.Footer>
		</Card.Root>
	</div>

	{#if clustered}
		<div class="px-4">
			<Card.Root class="gap-2">
				<Card.Header>
					<Card.Title>
						<div class="flex items-center gap-2">
							<Icon icon="fa7-solid:hexagon-nodes" />
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
							{#each nodes as node (node.id)}
								<Table.Row>
									<Table.Cell>
										<Badge variant="outline" class="text-muted-foreground px-1.5">
											{#if node.status === 'online'}
												<Icon icon="mdi:check-circle" class="text-green-500" />
											{:else}
												<Icon icon="mdi:close-circle" class="text-red-500" />
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
				<Card.Footer></Card.Footer>
			</Card.Root>
		</div>
	{/if}
</div>
