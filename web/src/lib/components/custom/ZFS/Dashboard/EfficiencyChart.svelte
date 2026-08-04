<script lang="ts">
	import { Chart, type ECMouseEvent } from '@alchemilla/svelte-echarts';
	import { BarChart } from 'echarts/charts';
	import { GridComponent, TooltipComponent } from 'echarts/components';
	import { init, use } from 'echarts/core';
	import { CanvasRenderer } from 'echarts/renderers';
	import { mode } from 'mode-watcher';
	import type { EChartsOption } from 'echarts';
	import type { Zpool } from '$lib/types/zfs/pool';

	use([BarChart, GridComponent, TooltipComponent, CanvasRenderer]);

	interface Props {
		pools: Zpool[];
		selectedGUID: string;
		onSelect: (guid: string) => void;
	}

	let { pools, selectedGUID, onSelect }: Props = $props();
	let sortedPools = $derived([...pools].sort((left, right) => right.fragmentation - left.fragmentation));
	let chartHeight = $derived(Math.max(240, sortedPools.length * 48 + 54));

	let options: EChartsOption = $derived.by(() => {
		const dark = mode.current === 'dark';
		const foreground = dark ? '#d4d4d8' : '#52525b';
		const grid = dark ? 'rgba(255,255,255,0.075)' : 'rgba(24,24,27,0.075)';
		return {
			animationDuration: 650,
			grid: { left: 8, right: 78, top: 8, bottom: 22, containLabel: true },
			tooltip: {
				trigger: 'axis',
				axisPointer: { type: 'shadow' },
				backgroundColor: dark ? '#343434' : '#fafaf9',
				borderColor: dark ? '#525252' : '#d6d3d1',
				textStyle: { color: dark ? '#f4f4f5' : '#27272a' },
				formatter: (params) => {
					const entries = Array.isArray(params) ? params : [params];
					const pool = sortedPools[Number(entries[0]?.dataIndex ?? 0)];
					if (!pool) return '';
					return `<strong>${pool.name}</strong><br/>${pool.fragmentation.toFixed(1)}% fragmented<br/>${pool.dedupRatio.toFixed(2)}× dedup ratio`;
				}
			},
			xAxis: {
				type: 'value',
				min: 0,
				max: 100,
				axisLine: { show: false },
				axisTick: { show: false },
				axisLabel: { color: foreground, fontSize: 10, formatter: '{value}%' },
				splitLine: { lineStyle: { color: grid, type: 'dashed' } }
			},
			yAxis: {
				type: 'category',
				inverse: true,
				data: sortedPools.map((pool) => pool.name),
				axisLine: { show: false },
				axisTick: { show: false },
				axisLabel: { color: foreground, fontSize: 11, fontWeight: 500, width: 96, overflow: 'truncate' }
			},
			series: [
				{
					type: 'bar',
					barWidth: 12,
					showBackground: true,
					backgroundStyle: { color: dark ? 'rgba(255,255,255,0.055)' : 'rgba(24,24,27,0.055)', borderRadius: 6 },
					data: sortedPools.map((pool) => ({
						value: pool.fragmentation,
						itemStyle: {
							color:
								pool.guid === selectedGUID
									? '#2563eb'
									: pool.fragmentation >= 70
										? '#ef4444'
										: pool.fragmentation >= 40
											? '#f59e0b'
											: '#fb923c',
							borderRadius: 6,
							opacity: selectedGUID && pool.guid !== selectedGUID ? 0.58 : 0.9
						}
					})),
					label: {
						show: true,
						position: 'right',
						color: foreground,
						fontSize: 10,
						formatter: (params) => {
							const pool = sortedPools[Number(params.dataIndex)];
							return pool ? `${pool.fragmentation.toFixed(0)}% · ${pool.dedupRatio.toFixed(2)}×` : '';
						}
					},
					emphasis: { disabled: true }
				}
			]
		};
	});

	function handleClick(event: ECMouseEvent) {
		const pool = sortedPools[Number(event.dataIndex)];
		if (pool) onSelect(pool.guid);
	}
</script>

<div style:height={`${chartHeight}px`} class="w-full">
	<Chart {init} {options} onclick={handleClick} class="h-full w-full cursor-pointer" />
</div>
