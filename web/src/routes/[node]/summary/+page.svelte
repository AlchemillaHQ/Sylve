<script lang="ts">
	import { storage } from '$lib';
	import { getBasicInfo } from '$lib/api/info/basic';
	import { getCPUInfo } from '$lib/api/info/cpu';
	import { getRAMInfo, getSwapInfo } from '$lib/api/info/ram';
	import { getNodeSummaryHistory } from '$lib/api/info/summary';
	import { getPoolsDiskUsage } from '$lib/api/zfs/pool';
	import LineBrush from '$lib/components/custom/Charts/LineBrush/Single.svelte';
	import LineBrushMultiple from '$lib/components/custom/Charts/LineBrush/Multiple.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { BasicInfo } from '$lib/types/info/basic';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { NodeSummaryHistory, SummaryHistoryNetworkPoint } from '$lib/types/info/summary';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { updateCache } from '$lib/utils/http';
	import { floatToNDecimals } from '$lib/utils/numbers';
	import { formatUptime } from '$lib/utils/time';
	import { resource, useInterval } from 'runed';
	import { watch } from 'runed';

	interface Data {
		hostname: string;
		basicInfo: BasicInfo;
		cpuInfo: CPUInfo;
		ramInfo: RAMInfo;
		swapInfo: RAMInfo;
		totalDiskUsage: number;
		summaryHistory: NodeSummaryHistory;
	}

	let { data }: { data: Data } = $props();
	const historyReconcileInterval = 5 * 60 * 1000;
	let lastHistoryFullRefreshAt = Date.now();

	// svelte-ignore state_referenced_locally
	const basicInfo = resource(
		() => 'basic-info',
		async (key) => {
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
		async (key) => {
			const result = await getCPUInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.cpuInfo
		}
	);

	// svelte-ignore state_referenced_locally
	const ramInfo = resource(
		() => 'ram-info',
		async (key) => {
			const result = await getRAMInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.ramInfo
		}
	);

	// svelte-ignore state_referenced_locally
	const swapInfo = resource(
		() => 'swap-info',
		async (key) => {
			const result = await getSwapInfo('current');
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.swapInfo
		}
	);

	// svelte-ignore state_referenced_locally
	const totalDiskUsage = resource(
		() => 'total-disk-usage',
		async (key) => {
			const result = await getPoolsDiskUsage();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.totalDiskUsage
		}
	);

	function mergeSummaryPoints<T extends { id: number; createdAt: string }>(
		current: T[],
		incoming: T[]
	): T[] {
		if (incoming.length === 0) return current;

		const byID: Record<number, T> = {};
		for (const point of current) byID[point.id] = point;
		for (const point of incoming) byID[point.id] = point;

		return Object.values(byID).sort((left, right) => {
			const timeDifference = Date.parse(left.createdAt) - Date.parse(right.createdAt);
			return Number.isFinite(timeDifference) && timeDifference !== 0
				? timeDifference
				: left.id - right.id;
		});
	}

	function mergeSummaryHistory(
		current: NodeSummaryHistory,
		incoming: NodeSummaryHistory
	): NodeSummaryHistory {
		if (incoming.cpu.length === 0 && incoming.ram.length === 0 && incoming.network.length === 0) {
			return current;
		}

		return {
			cpu: mergeSummaryPoints(current.cpu, incoming.cpu),
			ram: mergeSummaryPoints(current.ram, incoming.ram),
			network: mergeSummaryPoints(current.network, incoming.network),
			cursors: incoming.cursors
		};
	}

	// svelte-ignore state_referenced_locally
	const summaryHistory = resource(
		() => 'node-summary-history',
		async (key, _prevKey, { data: current, refetching, signal }) => {
			const fullRefresh = refetching === 'full';
			const incoming = await getNodeSummaryHistory(fullRefresh ? undefined : current.cursors, {
				signal
			});
			const result = fullRefresh ? incoming : mergeSummaryHistory(current, incoming);

			if (fullRefresh) {
				lastHistoryFullRefreshAt = Date.now();
				void updateCache(key, incoming);
			}
			return result;
		},
		{
			initialValue: data.summaryHistory
		}
	);

	function toNetworkDeltaPoints(
		history: SummaryHistoryNetworkPoint[],
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
				const refreshMode =
					Date.now() - lastHistoryFullRefreshAt >= historyReconcileInterval ? 'full' : 'delta';
				void summaryHistory.refetch(refreshMode);
			}
		}
	});

	watch(
		[() => storage.visible, () => data.hostname],
		([visible, hostname], [previousVisible, previousHostname]) => {
			if (hostname !== previousHostname || (visible && !previousVisible)) {
				basicInfo.refetch();
				cpuInfo.refetch();
				ramInfo.refetch();
				swapInfo.refetch();
				totalDiskUsage.refetch();
				void summaryHistory.refetch('full');
			}
		},
		{ lazy: true }
	);
</script>

<div class="space-y-4 p-4">
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
							RAM Usage
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
							Disk Usage
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
							<span class="icon-[ic--baseline-loop] mr-1 h-5 w-5"></span>Swap Usage
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
						<Table.Cell>
							<!-- @wc-ignore -->
							<span>{basicInfo.current.sylveVersion}</span>
							<!-- @wc-ignore -->
							{#if basicInfo.current.sylveCommit !== 'unknown'}
								<!-- @wc-ignore -->
								<span> - </span>
								<!-- @wc-ignore -->
								<span>{basicInfo.current.sylveCommit}</span>
							{/if}
						</Table.Cell>
					</Table.Row>
				</Table.Body>
			</Table.Root>
		</Card.Content>
	</Card.Root>

	<LineBrush
		title="CPU Usage"
		percentage={true}
		points={summaryHistory.current.cpu.map((data) => ({
			date: new Date(data.createdAt).getTime(),
			value: Number(data.usage)
		}))}
		color="one"
		animateOnMount={true}
		containerContentHeight="h-64"
		titleIconClass="icon-[solar--cpu-bold]"
	/>

	<LineBrush
		title="RAM Usage"
		percentage={true}
		points={summaryHistory.current.ram.map((data) => ({
			date: new Date(data.createdAt).getTime(),
			value: Number(data.usage)
		}))}
		color="two"
		animateOnMount={true}
		containerContentHeight="h-64"
		titleIconClass="icon-[ph--memory]"
	/>

	<LineBrushMultiple
		title="Network Usage"
		percentage={false}
		data={true}
		types="bitsPerSecond"
		smooth={false}
		animateOnMount={true}
		series={[
			{
				name: 'Received',
				color: 'two',
				points: toNetworkDeltaPoints(summaryHistory.current.network, 'receivedBytes')
			},
			{
				name: 'Sent',
				color: 'one',
				points: toNetworkDeltaPoints(summaryHistory.current.network, 'sentBytes')
			}
		]}
		titleIconClass="icon-[mdi--network]"
	/>
</div>
