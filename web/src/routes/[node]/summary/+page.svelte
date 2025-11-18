<script lang="ts">
	import { page } from '$app/state';
	import { getBasicInfo } from '$lib/api/info/basic';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getNetworkInterfaceInfoHistorical } from '$lib/api/info/network';
	import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
	import { getIODelay, getPoolsDiskUsage } from '$lib/api/zfs/pool';
	import AreaChart from '$lib/components/custom/Charts/Area.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { BasicInfo } from '$lib/types/info/basic';
	import type { CPUInfo, CPUInfoHistorical } from '$lib/types/info/cpu';
	import type { HistoricalNetworkInterface } from '$lib/types/info/network';
	import type { RAMInfo, RAMInfoHistorical } from '$lib/types/info/ram';
	import type { IODelay, IODelayHistorical } from '$lib/types/zfs/pool';
	import { updateCache } from '$lib/utils/http';
	import { bytesToHumanReadable, floatToNDecimals } from '$lib/utils/numbers';
	import { formatUptime } from '$lib/utils/time';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import type { Chart } from 'chart.js';

	interface Data {
		basicInfo: BasicInfo;
		cpuInfo: CPUInfo;
		cpuInfoHistorical: CPUInfoHistorical;
		ramInfo: RAMInfo;
		ramInfoHistorical: RAMInfoHistorical;
		swapInfo: RAMInfo;
		swapInfoHistorical: RAMInfoHistorical;
		ioDelay: IODelay;
		totalDiskUsage: number;
		ioDelayHistorical: IODelayHistorical;
		networkUsageHistorical: HistoricalNetworkInterface[];
	}

	let { data }: { data: Data } = $props();

	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: ['basicInfo'],
			queryFn: getBasicInfo,
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.basicInfo,
			onSuccess: (data: BasicInfo) => {
				updateCache('basicInfo', data);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['cpuInfo'],
			queryFn: getCPUInfo,
			refetchInterval: 5000,
			keepPreviousData: true,
			initialData: data.cpuInfo,
			onSuccess: (data: CPUInfo | CPUInfoHistorical) => {
				updateCache('cpuInfo', data as CPUInfo);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['ramInfo'],
			queryFn: getRAMInfo,
			refetchInterval: 5000,
			keepPreviousData: true,
			initialData: data.ramInfo,
			onSuccess: (data: RAMInfo | RAMInfoHistorical) => {
				updateCache('ramInfo', data);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['swapInfo'],
			queryFn: getSwapInfo,
			refetchInterval: 60000,
			keepPreviousData: true,
			initialData: data.swapInfo,
			onSuccess: (data: RAMInfo | RAMInfoHistorical) => {
				updateCache('swapInfo', data);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['ioDelay'],
			queryFn: getIODelay,
			refetchInterval: 60000,
			keepPreviousData: true,
			initialData: data.ioDelay,
			onSuccess: (data: IODelay | IODelayHistorical) => {
				updateCache('ioDelay', data as IODelay);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['totalDiskUsage'],
			queryFn: getPoolsDiskUsage,
			refetchInterval: 120000,
			keepPreviousData: true,
			initialData: data.totalDiskUsage,
			onSuccess: (data: number) => {
				updateCache('totalDiskUsage', data);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['cpuInfoHistorical'],
			queryFn: getCPUInfo,
			refetchInterval: 30000,
			keepPreviousData: true,
			initialData: data.cpuInfoHistorical,
			onSuccess: (data: CPUInfo | CPUInfoHistorical) => {
				updateCache('cpuInfoHistorical', data as CPUInfoHistorical);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['ioDelayHistorical'],
			queryFn: getIODelay,
			refetchInterval: 30000,
			keepPreviousData: true,
			initialData: data.ioDelayHistorical,
			onSuccess: (data: IODelay | IODelayHistorical) => {
				updateCache('ioDelayHistorical', data as IODelayHistorical);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['ramInfoHistorical'],
			queryFn: getRAMInfo,
			refetchInterval: 30000,
			keepPreviousData: true,
			initialData: data.ramInfoHistorical,
			onSuccess: (data: RAMInfo | RAMInfoHistorical) => {
				updateCache('ramInfoHistorical', data as RAMInfoHistorical);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['swapInfoHistorical'],
			queryFn: getSwapInfo,
			refetchInterval: 30000,
			keepPreviousData: true,
			initialData: data.swapInfoHistorical,
			onSuccess: (data: RAMInfo | RAMInfoHistorical) => {
				updateCache('swapInfoHistorical', data as RAMInfoHistorical);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		},
		{
			queryKey: ['networkUsageHistorical'],
			queryFn: getNetworkInterfaceInfoHistorical,
			refetchInterval: 30000,
			keepPreviousData: true,
			initialData: data.networkUsageHistorical,
			onSuccess: (data: HistoricalNetworkInterface[]) => {
				updateCache('networkUsageHistorical', data);
			},
			refetchOnMount: true,
			refetchOnWindowFocus: true
		}
	]);

	$effect(() => {
		if (page.url) {
			queryClient.refetchQueries('cpuInfo');
			queryClient.refetchQueries('basicInfo');
			queryClient.refetchQueries('ramInfo');
			queryClient.refetchQueries('swapInfo');
			queryClient.refetchQueries('ioDelay');
			queryClient.refetchQueries('totalDiskUsage');
			queryClient.refetchQueries('cpuInfoHistorical');
			queryClient.refetchQueries('ioDelayHistorical');
			queryClient.refetchQueries('ramInfoHistorical');
			queryClient.refetchQueries('swapInfoHistorical');
			queryClient.refetchQueries('networkUsageHistorical');
		}
	});

	let basicInfo = $derived($results[0].data as BasicInfo);
	let cpuInfo = $derived($results[1].data as CPUInfo);
	let ramInfo = $derived($results[2].data as RAMInfo);
	let swapInfo = $derived($results[3].data as RAMInfo);
	let ioDelay = $derived($results[4].data as IODelay);
	let totalDiskUsage = $derived($results[5].data as number);
	let cpuInfoHistorical = $derived($results[6].data as CPUInfoHistorical);
	let ioDelayHistorical = $derived($results[7].data as IODelayHistorical);
	let ramInfoHistorical = $derived($results[8].data as RAMInfoHistorical);
	let swapInfoHistorical = $derived($results[9].data as RAMInfoHistorical);
	let networkUsageHistorical = $derived($results[10].data as HistoricalNetworkInterface[]);

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
				field: 'ioDelay',
				label: 'I/O Delay',
				color: 'chart-2',
				data: ioDelayHistorical
					.map((data) => ({
						date: new Date(data.createdAt),
						value: data.delay.toFixed(2)
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
										<span class="icon-[lets-icons--time-light] mr-1 h-5 w-5"></span>
										{'I/O Delay'}
									</p>
									<p>{floatToNDecimals(ioDelay.delay, 3) || 0} %</p>
								</div>
								<Progress
									value={floatToNDecimals(ioDelay.delay, 3) || 0}
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
					title="CPU Usage"
					elements={[chartElements[1], chartElements[0]]}
					icon="icon-[solar--cpu-bold]"
					chart={cpuUsageRef}
					percentage={true}
				/>
				<AreaChart
					title="Memory Usage"
					elements={[chartElements[3], chartElements[2]]}
					icon="icon-[la--memory]"
					chart={memoryUsageRef}
					percentage={true}
				/>
				<AreaChart
					title="Network Usage"
					elements={[chartElements[4], chartElements[5]]}
					formatSize={true}
					icon="icon-[gg--smartphone-ram]"
					chart={networkUsageRef}
				/>
			</div>
		</ScrollArea>
	</div>
</div>
