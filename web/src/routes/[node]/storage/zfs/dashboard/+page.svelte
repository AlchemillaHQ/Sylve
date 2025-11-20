<script lang="ts">
	import { getDatasets } from '$lib/api/zfs/datasets';
	import { getPools, getPoolStats } from '$lib/api/zfs/pool';
	import AreaChart from '$lib/components/custom/Charts/Area.svelte';
	import BarChart from '$lib/components/custom/Charts/Bar.svelte';
	import LineGraph from '$lib/components/custom/Charts/LineGraph.svelte';
	import PieChart from '$lib/components/custom/Charts/Pie.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import type { PoolStatPointsResponse, Zpool } from '$lib/types/zfs/pool';
	import { updateCache } from '$lib/utils/http';
	import {
		getDatasetCompressionHist,
		getPoolStatsCombined,
		getPoolUsagePieData,
		type StatType
	} from '$lib/utils/zfs/pool';
	import { createQueries } from '@tanstack/svelte-query';
	import type { Chart } from 'chart.js';
	import { onMount } from 'svelte';

	interface Data {
		pools: Zpool[];
		datasets: Dataset[];
		poolStats: PoolStatPointsResponse;
	}

	type CardType = 'pools' | 'datasets' | 'file_systems' | 'volumes' | 'snapshots';

	let { data }: { data: Data } = $props();
	let poolStatsRef: Chart | null = $state(null);
	let datasetChartRef: Chart | null = $state(null);
	let poolStatsInterval = $state('1');

	const results = createQueries(() => ({
		queries: [
			{
				queryKey: ['poolList'],
				queryFn: async () => {
					return await getPools();
				},
				refetchInterval: 1000,
				keepPreviousData: false,
				initialData: data.pools,
				onSuccess: (data: Zpool[]) => {
					updateCache('pools', data);
				}
			},
			{
				queryKey: ['datasetList'],
				queryFn: async () => {
					return await getDatasets();
				},
				refetchInterval: 1000,
				keepPreviousData: false,
				initialData: data.datasets,
				onSuccess: (data: Dataset[]) => {
					updateCache('datasets', data);
				}
			},
			{
				queryKey: ['pool-stats', poolStatsInterval],
				queryFn: async () => {
					return await getPoolStats(Number(poolStatsInterval), 128);
				},
				refetchInterval: 1000,
				keepPreviousData: false,
				initialData: Array.isArray(data.poolStats) ? data.poolStats[0] : data.poolStats,
				onSuccess: (data: PoolStatPointsResponse) => {
					updateCache('pool-stats', data);
				}
			}
		]
	}));

	let pools: Zpool[] = $derived(results[0].data as Zpool[]);
	let datasets: Dataset[] = $derived(results[1].data as Dataset[]);
	let poolStats: PoolStatPointsResponse = $derived(results[2].data as PoolStatPointsResponse);

	let filesystems: Dataset[] = $derived.by(() => {
		return datasets.filter((dataset) => dataset.type === 'filesystem');
	});

	let volumes: Dataset[] = $derived.by(() => {
		return datasets.filter((dataset) => dataset.type === 'volume');
	});

	let snapshots: Dataset[] = $derived.by(() => {
		return datasets.filter((dataset) => dataset.type === 'snapshot');
	});

	const counts = $derived({
		pools: pools.length,
		datasets: datasets.length,
		file_systems: filesystems.length,
		volumes: volumes.length,
		snapshots: snapshots.length
	});

	function getCardDetails(type: string) {
		switch (type) {
			case 'pools':
				return {
					title: 'Pools',
					icon: 'bi--hdd-stack-fill'
				};
			case 'datasets':
				return {
					title: 'Datasets',
					icon: 'material-symbols--dataset'
				};
			case 'file_systems':
				return {
					title: 'Filesystems',
					icon: 'eos-icons--file-system'
				};
			case 'volumes':
				return {
					title: 'Volumes',
					icon: 'carbon--volume-block-storage'
				};
			case 'snapshots':
				return {
					title: 'Snapshots',
					icon: 'carbon--ibm-cloud-vpc-block-storage-snapshots'
				};
			default:
				return {
					title: '',
					icon: ''
				};
		}
	}

	let comboBoxes = $state({
		poolUsage: {
			open: false,
			value: '',
			data: [] as { value: string; label: string }[]
		},
		datasetCompression: {
			open: false,
			value: '',
			data: [] as { value: string; label: string }[]
		},
		poolStats: {
			interval: {
				open: false,
				value: '1',
				data: [] as { value: string; label: string }[]
			},
			statType: {
				open: false,
				value: 'allocated',
				data: [
					{ value: 'allocated', label: 'Allocated' },
					{ value: 'free', label: 'Free' },
					{ value: 'size', label: 'Size' },
					{ value: 'dedupRatio', label: 'Dedup Ratio' }
				]
			}
		}
	});

	onMount(() => {
		if (pools) {
			comboBoxes.poolUsage.value = pools[0]?.name || '';
			comboBoxes.poolUsage.data = pools.map((pool) => ({
				value: pool.name,
				label: pool.name
			}));

			comboBoxes.datasetCompression.value = pools[0]?.name || '';
			comboBoxes.datasetCompression.data = pools.map((pool) => ({
				value: pool.name,
				label: pool.name
			}));
		}

		if (poolStats?.intervalMap) {
			comboBoxes.poolStats.interval.data = poolStats.intervalMap;
		}

		if (poolStatsInterval) {
			comboBoxes.poolStats.interval.value = poolStatsInterval;
		}
	});

	let pieCharts = $derived.by(() => {
		return {
			poolUsage: {
				data: getPoolUsagePieData(pools, comboBoxes.poolUsage.value)
			}
		};
	});

	let histograms = $derived.by(() => {
		return {
			compression: {
				data: getDatasetCompressionHist(comboBoxes.datasetCompression.value, datasets)
			}
		};
	});

	let { poolStatSeries } = $derived.by(() => {
		const statType = comboBoxes.poolStats.statType.value;
		const { poolStatsData, poolStatsKeys } = getPoolStatsCombined(
			poolStats.poolStatPoint,
			statType as StatType
		);

		const poolStatSeries = poolStatsKeys.map((series, index) => ({
			field: series.key,
			label: series.title,
			color: series.color,
			data: poolStatsData[index].map((item) => ({
				date: item.date,
				value: item[series.key]
			}))
		}));

		return { poolStatSeries };
	});
</script>

{#snippet card(type: string)}
	<Card.Root class="gap-2 p-5">
		<Card.Header class="p-0">
			<Card.Title class="p-0">
				<div class="flex items-center">
					<span
						class={`icon-[${getCardDetails(type).icon}] mr-2`}
						style="width: 18px; height: 18px;"
					></span>

					<span class="font-normal">{getCardDetails(type).title}</span>
				</div>
			</Card.Title>
		</Card.Header>
		<Card.Content class="p-0 pl-1">
			<p class="text-xl font-semibold">
				{counts[type as CardType]}
			</p>
		</Card.Content>
	</Card.Root>
{/snippet}

<div class="p-4">
	<div class="grid grid-cols-1 gap-4 md:grid-cols-3 lg:grid-cols-5">
		{#each ['pools', 'datasets', 'file_systems', 'volumes', 'snapshots'] as type}
			<div>
				{@render card(type)}
			</div>
		{/each}
	</div>

	{#if pools.length > 0}
		<Card.Root class="mt-4 w-full flex-col">
			<Card.Header>
				<Card.Title class="mb-[-100px]">
					<div
						class="flex w-full flex-col items-start justify-between gap-2 md:flex-row md:items-center"
					>
						<div class="flex items-center">
							<span class="icon-[mdi--data-usage] mr-2"></span>

							<span class="text-sm font-bold md:text-lg xl:text-xl">Pool Stats</span>
						</div>
						<div class="flex items-center gap-2">
							<CustomComboBox
								bind:open={comboBoxes.poolStats.statType.open}
								label=""
								bind:value={comboBoxes.poolStats.statType.value}
								data={comboBoxes.poolStats.statType.data}
								classes=""
								placeholder="Select a stat type"
								width="w-48"
								disallowEmpty={true}
							/>
							<CustomComboBox
								bind:open={comboBoxes.poolStats.interval.open}
								label=""
								bind:value={poolStatsInterval}
								data={comboBoxes.poolStats.interval.data}
								classes=""
								placeholder="Select a interval"
								width="w-48"
								disallowEmpty={true}
							/>
							<Button
								onclick={() => poolStatsRef?.resetZoom()}
								variant="outline"
								size="sm"
								class="h-9"
							>
								<span class="icon-[carbon--reset] h-4 w-4"></span>

								Reset zoom
							</Button>
						</div>
					</div>
				</Card.Title>
			</Card.Header>

			<Card.Content class="mt-10 flex-1 overflow-hidden md:mt-2">
				<AreaChart
					bind:chart={poolStatsRef}
					elements={poolStatSeries}
					formatSize={comboBoxes.poolStats.statType.value !== 'dedupRatio'}
					containerClass="border-none shadow-none !p-0"
					icon=""
					showResetButton={false}
				/>
			</Card.Content>
		</Card.Root>

		<div class="mt-4 grid h-full w-full grid-cols-12 gap-4">
			<Card.Root class="col-span-12 min-h-[300px]  w-full flex-col md:col-span-8">
				<Card.Header>
					<Card.Title class="mb-[-100px]">
						<div
							class="flex w-full flex-col items-start justify-between gap-2 md:flex-row md:items-center"
						>
							<div class="flex items-center">
								<span class="icon-[mdi--data-usage] mr-2"></span>

								<span class="text-sm font-bold md:text-lg xl:text-xl">Dataset Compression</span>
							</div>
							<div class="flex items-center gap-2">
								<CustomComboBox
									bind:open={comboBoxes.datasetCompression.open}
									label=""
									bind:value={comboBoxes.datasetCompression.value}
									data={comboBoxes.datasetCompression.data}
									classes=""
									placeholder="Select a pool"
									width="w-48"
									disallowEmpty={true}
								/>
								<Button
									onclick={() => datasetChartRef?.resetZoom()}
									variant="outline"
									size="sm"
									class="h-9"
								>
									<span class="icon-[carbon--reset] h-4 w-4"></span>

									Reset zoom
								</Button>
							</div>
						</div>
					</Card.Title>
				</Card.Header>

				<Card.Content class="mt-10 flex-1 overflow-hidden md:mt-2">
					{#if histograms.compression.data.length === 0}
						<div class="flex h-full items-center justify-center">
							<p class="text-md">No data available</p>
						</div>
					{:else}
						<BarChart
							bind:chart={datasetChartRef}
							data={histograms.compression.data}
							colors={{
								baseline: 'chart-3',
								value: 'chart-4'
							}}
							formatter="size-formatter"
							showResetButton={false}
						/>
					{/if}
				</Card.Content>
			</Card.Root>

			<Card.Root class="col-span-12 flex w-full flex-col md:col-span-4">
				<Card.Header>
					<Card.Title class="mb-[-100px]">
						<div class="flex w-full items-center justify-between">
							<div class="flex items-center">
								<span class="icon-[mdi--data-usage] mr-2"></span>

								<span class="text-sm font-bold md:text-lg xl:text-xl">Pool Usage</span>
							</div>
							<CustomComboBox
								bind:open={comboBoxes.poolUsage.open}
								label=""
								bind:value={comboBoxes.poolUsage.value}
								data={comboBoxes.poolUsage.data}
								classes=""
								placeholder="Select a pool"
								width="w-48"
								disallowEmpty={true}
							/>
						</div>
					</Card.Title>
				</Card.Header>

				<Card.Content class="flex-1 overflow-hidden">
					<div class="mt-4 flex h-full items-center justify-center">
						<PieChart
							containerClass="h-full w-full rounded flex items-start justify-center"
							data={pieCharts.poolUsage.data}
							formatter={'size-formatter'}
						/>
					</div>
				</Card.Content>
			</Card.Root>
		</div>
	{/if}
</div>
