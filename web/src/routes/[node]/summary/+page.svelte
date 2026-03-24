<script lang="ts">
	import { storage } from '$lib';
	import { getBasicInfo } from '$lib/api/info/basic';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getNetworkInterfaceInfoHistorical } from '$lib/api/info/network';
	import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
	import { getPoolsDiskUsage } from '$lib/api/zfs/pool';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';
	import LineBrushMultiple from '$lib/components/custom/Charts/LineBrush/Multiple.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { BasicInfo } from '$lib/types/info/basic';
	import type { CPUInfo, CPUInfoHistorical } from '$lib/types/info/cpu';
	import type { HistoricalNetworkInterface } from '$lib/types/info/network';
	import type { RAMInfo, RAMInfoHistorical } from '$lib/types/info/ram';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { updateCache } from '$lib/utils/http';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { formatUptime } from '$lib/utils/time';
	import { resource, useInterval } from 'runed';
	import { fade } from 'svelte/transition';
	import { watch } from 'runed';

	interface Data {
		hostname: string;
		basicInfo: BasicInfo;
		cpuInfo: CPUInfo;
		cpuInfoHistorical: CPUInfoHistorical;
		ramInfo: RAMInfo;
		ramInfoHistorical: RAMInfoHistorical;
		swapInfo: RAMInfo;
		swapInfoHistorical: RAMInfoHistorical;
		totalDiskUsage: number;
		networkUsageHistorical: HistoricalNetworkInterface[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const basicInfo = resource(
		() => 'basic-info',
		async (key, prevKey, { signal }) => {
			const result = await getBasicInfo();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.basicInfo
		}
	);

	// svelte-ignore state_referenced_locally
	const cpuInfo = resource(
		() => 'cpu-info',
		async (key, prevKey, { signal }) => {
			const result = await getCPUInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.cpuInfo
		}
	);

	// svelte-ignore state_referenced_locally
	const cpuInfoHistorical = resource(
		() => 'cpu-info-historical',
		async (key, prevKey, { signal }) => {
			const result = await getCPUInfo('historical');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.cpuInfoHistorical
		}
	);

	// svelte-ignore state_referenced_locally
	const ramInfo = resource(
		() => 'ram-info',
		async (key, prevKey, { signal }) => {
			const result = await getRAMInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.ramInfo
		}
	);

	// svelte-ignore state_referenced_locally
	const ramInfoHistorical = resource(
		() => 'ram-info-historical',
		async (key, prevKey, { signal }) => {
			const result = await getRAMInfo('historical');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.ramInfoHistorical
		}
	);

	// svelte-ignore state_referenced_locally
	const swapInfo = resource(
		() => 'swap-info',
		async (key, prevKey, { signal }) => {
			const result = await getSwapInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.swapInfo
		}
	);

	// svelte-ignore state_referenced_locally
	const swapInfoHistorical = resource(
		() => 'swap-info-historical',
		async (key, prevKey, { signal }) => {
			const result = await getSwapInfo('historical');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.swapInfoHistorical
		}
	);

	// svelte-ignore state_referenced_locally
	const totalDiskUsage = resource(
		() => 'total-disk-usage',
		async (key, prevKey, { signal }) => {
			const result = await getPoolsDiskUsage();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.totalDiskUsage
		}
	);

	// svelte-ignore state_referenced_locally
	const networkUsageHistorical = resource(
		() => 'network-usage-historical',
		async (key, prevKey, { signal }) => {
			const result = await getNetworkInterfaceInfoHistorical();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.networkUsageHistorical
		}
	);

	function toNetworkDeltaPoints(
		history: HistoricalNetworkInterface[],
		direction: 'receivedBytes' | 'sentBytes'
	): { date: number; value: number }[] {
		return history
			.map((sample) => {
				const date = new Date(sample.createdAt).getTime();
				const bytes = Number(sample[direction]);
				if (!Number.isFinite(date)) return null;

				return {
					date,
					value: Number.isFinite(bytes) && bytes > 0 ? bytes : 0
				};
			})
			.filter((x): x is { date: number; value: number } => x !== null)
			.sort((a, b) => a.date - b.date);
	}

	useInterval(() => 2000, {
		callback: () => {
			if (storage.visible) {
				cpuInfo.refetch();
				ramInfo.refetch();
			}
		}
	});

	useInterval(() => 6000, {
		callback: () => {
			if (storage.visible) {
				swapInfo.refetch();
				totalDiskUsage.refetch();
			}
		}
	});

	useInterval(() => 30000, {
		callback: () => {
			if (storage.visible) {
				cpuInfoHistorical.refetch();
				ramInfoHistorical.refetch();
				swapInfoHistorical.refetch();
				networkUsageHistorical.refetch();
			}
		}
	});

	watch(
		[() => storage.visible, () => data.hostname],
		([visible, hostname], [prevViisible, prevHostname]) => {
			if (visible || hostname !== prevHostname) {
				basicInfo.refetch();
				cpuInfo.refetch();
				ramInfo.refetch();
				swapInfo.refetch();
				totalDiskUsage.refetch();
				cpuInfoHistorical.refetch();
				ramInfoHistorical.refetch();
				swapInfoHistorical.refetch();
				networkUsageHistorical.refetch();
			}
		}
	);
</script>

<div class="space-y-4 p-4" transition:fade|global={{ duration: 300 }}>
	<Card.Root class="w-full gap-0 p-0">
		<Card.Header class="p-4 pb-0">
			<Card.Description class="text-md font-normal text-blue-600 dark:text-blue-500">
				{data.hostname}
			</Card.Description>
		</Card.Header>
		<Card.Content class="p-4 pt-2.5">
			<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
				<div>
					<div class="flex w-full justify-between pb-1">
						<p class="inline-flex items-center">
							<span class="icon-[solar--cpu-bold] mr-1 h-5 w-5"></span>

							<span>CPU Usage</span>
						</p>
						<p>
							{`${floatToNDecimals(cpuInfo.current.usage, 2)}% of ${cpuInfo.current.logicalCores} CPU(s)`}
						</p>
					</div>
					<Progress value={cpuInfo.current.usage || 0} max={100} class="h-2 w-full" />
				</div>
				<div>
					<div class="flex w-full justify-between pb-1">
						<p class="inline-flex items-center">
							<span class="icon-[ri--ram-fill] mr-1 h-5 w-5"></span>
							{'RAM Usage'}
						</p>
						<p>
							{`${floatToNDecimals(ramInfo.current?.usedPercent || 0, 2)}% of ${formatBytesBinary(ramInfo.current?.total || 0)}`}
						</p>
					</div>
					<Progress value={ramInfo.current?.usedPercent || 0} max={100} class="h-2 w-full" />
				</div>
				<div>
					<div class="flex w-full justify-between pb-1">
						<p class="inline-flex items-center">
							<span class="icon-[bxs--server] mr-1 h-5 w-5"></span>
							{'Disk Usage'}
						</p>
						<p>
							{floatToNDecimals(totalDiskUsage.current, 2)} %
						</p>
					</div>
					<Progress
						value={floatToNDecimals(totalDiskUsage.current, 2)}
						max={100}
						class="h-2 w-full"
					/>
				</div>
				<div>
					<div class="flex w-full justify-between pb-1">
						<p class="inline-flex items-center">
							<span class="icon-[ic--baseline-loop] mr-1 h-5 w-5"></span>{'Swap Usage'}
						</p>
						<p>
							{`${floatToNDecimals(swapInfo.current.usedPercent, 2)}% of ${formatBytesBinary(swapInfo.current.total)}`}
						</p>
					</div>
					<Progress value={swapInfo.current.usedPercent || 0} max={100} class="h-2 w-full" />
				</div>
			</div>

			<Table.Root class="mt-5 w-full">
				<Table.Body>
					<Table.Row>
						<Table.Cell>CPU(s)</Table.Cell>
						<Table.Cell>
							{`${cpuInfo.current.logicalCores} x ${cpuInfo.current.name}`}
						</Table.Cell>
					</Table.Row>
					<Table.Row>
						<Table.Cell>Operating System</Table.Cell>
						<Table.Cell class="wrap-break-words whitespace-normal"
							>{basicInfo.current.os}</Table.Cell
						>
					</Table.Row>
					<Table.Row>
						<Table.Cell>Uptime</Table.Cell>
						<Table.Cell>{formatUptime(basicInfo.current.uptime)}</Table.Cell>
					</Table.Row>
					<Table.Row>
						<Table.Cell>Load Average</Table.Cell>
						<Table.Cell>{basicInfo.current.loadAverage}</Table.Cell>
					</Table.Row>
					<Table.Row>
						<Table.Cell>Boot Mode</Table.Cell>
						<Table.Cell>{basicInfo.current.bootMode}</Table.Cell>
					</Table.Row>

					<Table.Row>
						<Table.Cell>Sylve Version</Table.Cell>
						<Table.Cell>{basicInfo.current.sylveVersion}</Table.Cell>
					</Table.Row>
				</Table.Body>
			</Table.Root>
		</Card.Content>
	</Card.Root>

	<LineBrush
		title="CPU Usage"
		percentage={true}
		points={cpuInfoHistorical.current.map((data) => ({
			date: new Date(data.createdAt).getTime(),
			value: Number(data.usage)
		}))}
		color="one"
		containerContentHeight="h-64"
		titleIconClass="icon-[solar--cpu-bold]"
	/>

	<LineBrush
		title="RAM Usage"
		percentage={true}
		points={ramInfoHistorical.current.map((data) => ({
			date: new Date(data.createdAt).getTime(),
			value: Number(data.usage)
		}))}
		color="two"
		containerContentHeight="h-64"
		titleIconClass="icon-[ph--memory]"
	/>

	<LineBrushMultiple
		title="Network Usage"
		percentage={false}
		data={true}
		types="bytes"
		smooth={false}
		series={[
			{
				name: 'Received',
				color: 'two',
				points: toNetworkDeltaPoints(networkUsageHistorical.current, 'receivedBytes')
			},
			{
				name: 'Sent',
				color: 'one',
				points: toNetworkDeltaPoints(networkUsageHistorical.current, 'sentBytes')
			}
		]}
		titleIconClass="icon-[mdi--network]"
	/>
</div>
