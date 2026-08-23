<script lang="ts">
	import { Chart, type ECMouseEvent } from '@alchemilla/svelte-echarts';
	import { BarChart } from 'echarts/charts';
	import { GridComponent, TooltipComponent } from 'echarts/components';
	import { init, use } from 'echarts/core';
	import { CanvasRenderer } from 'echarts/renderers';
	import { mode } from 'mode-watcher';
	import type { EChartsOption } from 'echarts';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';

	use([BarChart, GridComponent, TooltipComponent, CanvasRenderer]);

	interface Props {
		pools: Zpool[];
		selectedGUID: string;
		onSelect: (guid: string) => void;
	}

	let { pools, selectedGUID, onSelect }: Props = $props();

	let chartHeight = $derived(Math.max(230, pools.length * 54 + 58));
	let options: EChartsOption = $derived.by(() => {
		const dark = mode.current === 'dark';
		const labelColor = dark ? '#d4d4d8' : '#52525b';
		const mutedBar = dark ? 'rgba(255,255,255,0.075)' : 'rgba(24,24,27,0.07)';
		const used = pools.map((pool) => (pool.size > 0 ? (pool.allocated / pool.size) * 100 : 0));

		return {
			animationDuration: 650,
			animationEasing: 'cubicOut',
			grid: { left: 8, right: 42, top: 8, bottom: 8, containLabel: true },
			tooltip: {
				trigger: 'axis',
				axisPointer: { type: 'shadow' },
				backgroundColor: dark ? '#343434' : '#fafaf9',
				borderColor: dark ? '#525252' : '#d6d3d1',
				textStyle: { color: dark ? '#f4f4f5' : '#27272a' },
				formatter: (params) => {
					const entries = Array.isArray(params) ? params : [params];
					const index = Number(entries[0]?.dataIndex ?? 0);
					const pool = pools[index];
					if (!pool) return '';
					return [
						`<strong>${pool.name}</strong>`,
						`${used[index].toFixed(1)}% allocated`,
						`${formatBytesBinary(pool.allocated)} used · ${formatBytesBinary(pool.free)} free`
					].join('<br/>');
				}
			},
			xAxis: {
				type: 'value',
				min: 0,
				max: 100,
				show: false
			},
			yAxis: {
				type: 'category',
				inverse: true,
				data: pools.map((pool) => pool.name),
				axisLine: { show: false },
				axisTick: { show: false },
				axisLabel: {
					color: labelColor,
					fontSize: 12,
					fontWeight: 500,
					margin: 14,
					width: 112,
					overflow: 'truncate'
				}
			},
			series: [
				{
					name: 'Allocated',
					type: 'bar',
					stack: 'capacity',
					barWidth: 14,
					data: pools.map((pool, index) => ({
						value: used[index],
						itemStyle: {
							color: pool.guid === selectedGUID ? '#2563eb' : '#3b82f6',
							borderRadius: [7, used[index] >= 99.5 ? 7 : 0, used[index] >= 99.5 ? 7 : 0, 7],
							opacity: selectedGUID && pool.guid !== selectedGUID ? 0.52 : 1
						}
					})),
					emphasis: { disabled: true }
				},
				{
					name: 'Free',
					type: 'bar',
					stack: 'capacity',
					barWidth: 14,
					data: used.map((value, index) => ({
						value: Math.max(0, 100 - value),
						itemStyle: {
							color: mutedBar,
							borderRadius: [value <= 0.5 ? 7 : 0, 7, 7, value <= 0.5 ? 7 : 0],
							opacity: selectedGUID && pools[index]?.guid !== selectedGUID ? 0.7 : 1
						}
					})),
					label: {
						show: true,
						position: 'right',
						color: labelColor,
						fontSize: 11,
						formatter: (params) => `${used[Number(params.dataIndex)].toFixed(0)}%`
					},
					emphasis: { disabled: true }
				}
			]
		};
	});

	function handleClick(event: ECMouseEvent) {
		const index = Number(event.dataIndex);
		if (Number.isInteger(index) && pools[index]) onSelect(pools[index].guid);
	}
</script>

<div style:height={`${chartHeight}px`} class="w-full">
	<Chart {init} {options} onclick={handleClick} class="h-full w-full cursor-pointer" />
</div>
