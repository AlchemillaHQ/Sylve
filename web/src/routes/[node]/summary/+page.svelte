<script lang="ts">
	import { storage } from '$lib';
	import { getBasicInfo } from '$lib/api/info/basic';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getNetworkInterfaceInfoHistorical } from '$lib/api/info/network';
	import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
	import { getPoolsDiskUsage } from '$lib/api/zfs/pool';
	import AreaChart from '$lib/components/custom/Charts/Area.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { BasicInfo } from '$lib/types/info/basic';
	import type { CPUInfo, CPUInfoHistorical } from '$lib/types/info/cpu';
	import type { HistoricalNetworkInterface } from '$lib/types/info/network';
	import type { RAMInfo, RAMInfoHistorical } from '$lib/types/info/ram';
	import { updateCache } from '$lib/utils/http';
	import { bytesToHumanReadable, floatToNDecimals } from '$lib/utils/numbers';
	import { formatUptime } from '$lib/utils/time';
	import type { Chart } from 'chart.js';
	import { resource, useInterval } from 'runed';
	import { untrack } from 'svelte';

	interface Data {
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

	const basicInfo = resource(
		() => 'basic-info',
		async (key, prevKey, { signal }) => {
			const result = await getBasicInfo();
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.basicInfo
		}
	);

	const cpuInfo = resource(
		() => 'cpu-info',
		async (key, prevKey, { signal }) => {
			const result = await getCPUInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.cpuInfo
		}
	);

	const ramInfo = resource(
		() => 'ram-info',
		async (key, prevKey, { signal }) => {
			const result = await getRAMInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.ramInfo
		}
	);

	const swapInfo = resource(
		() => 'swap-info',
		async (key, prevKey, { signal }) => {
			const result = await getSwapInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.swapInfo
		}
	);

	const totalDiskUsage = resource(
		() => 'total-disk-usage',
		async (key, prevKey, { signal }) => {
			const result = await getPoolsDiskUsage();
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.totalDiskUsage
		}
	);

	const cpuInfoHistorical = resource(
		() => 'cpu-info-historical',
		async (key, prevKey, { signal }) => {
			const result = await getCPUInfo('historical');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.cpuInfoHistorical
		}
	);

	const ramInfoHistorical = resource(
		() => 'ram-info-historical',
		async (key, prevKey, { signal }) => {
			const result = await getRAMInfo('historical');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.ramInfoHistorical
		}
	);

	const swapInfoHistorical = resource(
		() => 'swap-info-historical',
		async (key, prevKey, { signal }) => {
			const result = await getSwapInfo('historical');
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.swapInfoHistorical
		}
	);

	const networkUsageHistorical = resource(
		() => 'network-usage-historical',
		async (key, prevKey, { signal }) => {
			const result = await getNetworkInterfaceInfoHistorical();
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.networkUsageHistorical
		}
	);

	useInterval(() => 2000, {
		callback: () => {
			cpuInfo.refetch();
			ramInfo.refetch();
		}
	});

	useInterval(() => 6000, {
		callback: () => {
			swapInfo.refetch();
			totalDiskUsage.refetch();
		}
	});

	useInterval(() => 30000, {
		callback: () => {
			cpuInfoHistorical.refetch();
			ramInfoHistorical.refetch();
			swapInfoHistorical.refetch();
			networkUsageHistorical.refetch();
		}
	});

	$effect(() => {
		if (storage.visible) {
			untrack(() => {
				basicInfo.refetch();
				cpuInfo.refetch();
				ramInfo.refetch();
				swapInfo.refetch();
				totalDiskUsage.refetch();
				cpuInfoHistorical.refetch();
				ramInfoHistorical.refetch();
				swapInfoHistorical.refetch();
				networkUsageHistorical.refetch();
			});
		}
	});

	let chartElements = $derived.by(() => {
		return [
			{
				field: 'cpuUsage',
				label: 'CPU Usage',
				color: 'chart-1',
				data: cpuInfoHistorical.current
					.map((data) => ({
						date: new Date(data.createdAt),
						value: data.usage.toFixed(2)
					}))
					.slice(-16)
			},
			{
				field: 'ramUsage',
				label: 'RAM Usage',
				color: 'chart-3',
				data: ramInfoHistorical.current
					.map((data) => ({
						date: new Date(data.createdAt),
						value: data.usage.toFixed(2)
					}))
					.slice(-16)
			},
			{
				field: 'swapUsage',
				label: 'Swap Usage',
				color: 'chart-4',
				data: swapInfoHistorical.current
					.map((data) => ({
						date: new Date(data.createdAt),
						value: data.usage.toFixed(2)
					}))
					.slice(-16)
			},
			{
				field: 'networkUsageRx',
				label: 'Network RX',
				color: 'chart-1',
				data: networkUsageHistorical.current
					.map((data) => ({
						date: new Date(data.createdAt),
						value: data.receivedBytes.toFixed(2)
					}))
					.slice(-16)
			},
			{
				field: 'networkUsageTx',
				label: 'Network TX',
				color: 'chart-4',
				data: networkUsageHistorical.current
					.map((data) => ({
						date: new Date(data.createdAt),
						value: data.sentBytes.toFixed(2)
					}))
					.slice(-16)
			}
		];
	});

	let cpuUsageRef: Chart | null = $state(null);
	let memoryUsageRef: Chart | null = $state(null);
	let networkUsageRef: Chart | null = $state(null);
</script>

<div class="flex h-full w-full flex-col">
	<div class="min-h-0 flex-1">
		<ScrollArea orientation="both" class="h-full w-full">
			<div class="space-y-4 p-4">
				<Card.Root class="w-full gap-0 p-0">
					<Card.Header class="p-4 pb-0">
						<Card.Description class="text-md font-normal text-blue-600 dark:text-blue-500">
							{basicInfo.current.hostname}
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
										{`${floatToNDecimals(ramInfo.current.usedPercent, 2)}% of ${bytesToHumanReadable(ramInfo.current.total)}`}
									</p>
								</div>
								<Progress value={ramInfo.current.usedPercent || 0} max={100} class="h-2 w-full" />
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
										{`${floatToNDecimals(swapInfo.current.usedPercent, 2)}% of ${bytesToHumanReadable(swapInfo.current.total)}`}
									</p>
								</div>
								<Progress value={swapInfo.current.usedPercent || 0} max={100} class="h-2 w-full" />
							</div>
						</div>

						<Table.Root class="mt-5">
							<Table.Body>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">CPU(s)</Table.Cell>
									<Table.Cell class="p-1.5 px-4">
										{`${cpuInfo.current.logicalCores} x ${cpuInfo.current.name}`}
									</Table.Cell>
								</Table.Row>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Operating System</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{basicInfo.current.os}</Table.Cell>
								</Table.Row>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Uptime</Table.Cell>
									<Table.Cell class="p-1.5 px-4"
										>{formatUptime(basicInfo.current.uptime)}</Table.Cell
									>
								</Table.Row>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Load Average</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{basicInfo.current.loadAverage}</Table.Cell>
								</Table.Row>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Boot Mode</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{basicInfo.current.bootMode}</Table.Cell>
								</Table.Row>

								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Sylve Version</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{basicInfo.current.sylveVersion}</Table.Cell>
								</Table.Row>
							</Table.Body>
						</Table.Root>
					</Card.Content>
				</Card.Root>

				<AreaChart
					title="CPU / RAM Usage"
					elements={[chartElements[1], chartElements[0], chartElements[2]]}
					icon="icon-[solar--cpu-bold]"
					chart={cpuUsageRef}
					percentage={true}
				/>

				<AreaChart
					title="Network Usage"
					elements={[chartElements[3], chartElements[4]]}
					formatSize={true}
					icon="icon-[gg--smartphone-ram]"
					chart={networkUsageRef}
				/>
			</div>
		</ScrollArea>
	</div>
</div>
