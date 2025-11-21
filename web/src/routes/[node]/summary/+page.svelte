<script lang="ts">
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
	import { useQueries } from '$lib/runes/useQuery.svelte';

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

	const {
		basicInfo: basicInfoQuery,
		cpuInfo: cpuInfoQuery,
		ramInfo: ramInfoQuery,
		swapInfo: swapInfoQuery,
		totalDiskUsage: totalDiskUsageQuery,
		cpuInfoHistorical: cpuInfoHistoricalQuery,
		ramInfoHistorical: ramInfoHistoricalQuery,
		swapInfoHistorical: swapInfoHistoricalQuery,
		networkUsageHistorical: networkUsageHistoricalQuery,
		refetchAll
	} = useQueries(() => ({
		basicInfo: () => ({
			key: 'basic-info',
			queryFn: () => getBasicInfo(),
			initialData: data.basicInfo,
			onSuccess: (data: BasicInfo) => {
				updateCache('basic-info', data);
			}
		}),
		cpuInfo: () => ({
			key: 'cpu-info',
			queryFn: () => getCPUInfo('current'),
			initialData: data.cpuInfo,
			onSuccess: (data: CPUInfo) => {
				updateCache('cpu-info', data);
			},
			refetchInterval: 2000
		}),
		ramInfo: () => ({
			key: 'ram-info',
			queryFn: () => getRAMInfo('current'),
			initialData: data.ramInfo,
			onSuccess: (data: RAMInfo) => {
				updateCache('ram-info', data);
			},
			refetchInterval: 2000
		}),
		swapInfo: () => ({
			key: 'swap-info',
			queryFn: () => getSwapInfo('current'),
			initialData: data.swapInfo,
			onSuccess: (data: RAMInfo) => {
				updateCache('swap-info', data);
			},
			refetchInterval: 60000
		}),
		totalDiskUsage: () => ({
			key: 'total-disk-usage',
			queryFn: () => getPoolsDiskUsage(),
			initialData: data.totalDiskUsage,
			onSuccess: (data: number) => {
				updateCache('total-disk-usage', data);
			},
			refetchInterval: 120000
		}),
		cpuInfoHistorical: () => ({
			key: 'cpu-info-historical',
			queryFn: () => getCPUInfo('historical'),
			initialData: data.cpuInfoHistorical,
			onSuccess: (data: CPUInfoHistorical) => {
				updateCache('cpu-info-historical', data);
			},
			refetchInterval: 30000
		}),
		ramInfoHistorical: () => ({
			key: 'ram-info-historical',
			queryFn: () => getRAMInfo('historical'),
			initialData: data.ramInfoHistorical,
			onSuccess: (data: RAMInfoHistorical) => {
				updateCache('ram-info-historical', data);
			},
			refetchInterval: 30000
		}),
		swapInfoHistorical: () => ({
			key: 'swap-info-historical',
			queryFn: () => getSwapInfo('historical'),
			initialData: data.swapInfoHistorical,
			onSuccess: (data: RAMInfoHistorical) => {
				updateCache('swap-info-historical', data);
			},
			refetchInterval: 30000
		}),
		networkUsageHistorical: () => ({
			key: 'network-usage-historical',
			queryFn: () => getNetworkInterfaceInfoHistorical(),
			initialData: data.networkUsageHistorical,
			onSuccess: (data: HistoricalNetworkInterface[]) => {
				updateCache('network-usage-historical', data);
			},
			refetchInterval: 30000
		})
	}));

	let basicInfo = $derived(basicInfoQuery.data as BasicInfo);
	let cpuInfo = $derived(cpuInfoQuery.data as CPUInfo);
	let ramInfo = $derived(ramInfoQuery.data as RAMInfo);
	let swapInfo = $derived(swapInfoQuery.data as RAMInfo);
	let totalDiskUsage = $derived(totalDiskUsageQuery.data as number);
	let cpuInfoHistorical = $derived(cpuInfoHistoricalQuery.data as CPUInfoHistorical);
	let ramInfoHistorical = $derived(ramInfoHistoricalQuery.data as RAMInfoHistorical);
	let swapInfoHistorical = $derived(swapInfoHistoricalQuery.data as RAMInfoHistorical);
	let networkUsageHistorical = $derived(
		networkUsageHistoricalQuery.data as HistoricalNetworkInterface[]
	);

	let chartElements = $derived.by(() => {
		return [
			{
				field: 'cpuUsage',
				label: 'CPU Usage',
				color: 'chart-1',
				data: cpuInfoHistorical
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
				data: ramInfoHistorical
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
				data: swapInfoHistorical
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
				data: networkUsageHistorical
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
				data: networkUsageHistorical
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
							{basicInfo.hostname}
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
										{`${floatToNDecimals(cpuInfo.usage, 2)}% of ${cpuInfo.logicalCores} CPU(s)`}
									</p>
								</div>
								<Progress value={cpuInfo.usage || 0} max={100} class="h-2 w-[100%]" />
							</div>
							<div>
								<div class="flex w-full justify-between pb-1">
									<p class="inline-flex items-center">
										<span class="icon-[ri--ram-fill] mr-1 h-5 w-5"></span>
										{'RAM Usage'}
									</p>
									<p>
										{`${floatToNDecimals(ramInfo.usedPercent, 2)}% of ${bytesToHumanReadable(ramInfo.total)}`}
									</p>
								</div>
								<Progress value={ramInfo.usedPercent || 0} max={100} class="h-2 w-[100%]" />
							</div>
							<div>
								<div class="flex w-full justify-between pb-1">
									<p class="inline-flex items-center">
										<span class="icon-[bxs--server] mr-1 h-5 w-5"></span>
										{'Disk Usage'}
									</p>
									<p>
										{floatToNDecimals(totalDiskUsage, 2)} %
									</p>
								</div>
								<Progress
									value={floatToNDecimals(totalDiskUsage, 2)}
									max={100}
									class="h-2 w-[100%]"
								/>
							</div>
							<div>
								<div class="flex w-full justify-between pb-1">
									<p class="inline-flex items-center">
										<span class="icon-[ic--baseline-loop] mr-1 h-5 w-5"></span>{'Swap Usage'}
									</p>
									<p>
										{`${floatToNDecimals(swapInfo.usedPercent, 2)}% of ${bytesToHumanReadable(swapInfo.total)}`}
									</p>
								</div>
								<Progress value={swapInfo.usedPercent || 0} max={100} class="h-2 w-[100%]" />
							</div>
						</div>

						<Table.Root class="mt-5">
							<Table.Body>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">CPU(s)</Table.Cell>
									<Table.Cell class="p-1.5 px-4">
										{`${cpuInfo.logicalCores} x ${cpuInfo.name}`}
									</Table.Cell>
								</Table.Row>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Operating System</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{basicInfo.os}</Table.Cell>
								</Table.Row>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Uptime</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{formatUptime(basicInfo.uptime)}</Table.Cell>
								</Table.Row>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Load Average</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{basicInfo.loadAverage}</Table.Cell>
								</Table.Row>
								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Boot Mode</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{basicInfo.bootMode}</Table.Cell>
								</Table.Row>

								<Table.Row>
									<Table.Cell class="p-1.5 px-4">Sylve Version</Table.Cell>
									<Table.Cell class="p-1.5 px-4">{basicInfo.sylveVersion}</Table.Cell>
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
