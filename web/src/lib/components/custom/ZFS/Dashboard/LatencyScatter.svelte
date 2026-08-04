<script lang="ts">
	import { Chart } from '@alchemilla/svelte-echarts';
	import { ScatterChart } from 'echarts/charts';
	import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components';
	import { init, use } from 'echarts/core';
	import { CanvasRenderer } from 'echarts/renderers';
	import { mode } from 'mode-watcher';
	import type { EChartsOption } from 'echarts';
	import type { PoolStatPoint } from '$lib/types/zfs/pool';

	use([ScatterChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

	interface Props {
		points: PoolStatPoint[];
	}

	let { points }: Props = $props();

	function compactNumber(value: number): string {
		return new Intl.NumberFormat(undefined, {
			notation: 'compact',
			maximumFractionDigits: 1
		}).format(value);
	}

	let options: EChartsOption = $derived.by(() => {
		const dark = mode.current === 'dark';
		const foreground = dark ? '#d4d4d8' : '#52525b';
		const grid = dark ? 'rgba(255,255,255,0.075)' : 'rgba(24,24,27,0.075)';
		const read = points
			.filter((point) => point.readIOPS > 0 && point.readLatencyNanos > 0)
			.map((point) => [point.readIOPS, point.readLatencyNanos / 1_000_000, point.time, point.readBytesPerSecond]);
		const write = points
			.filter((point) => point.writeIOPS > 0 && point.writeLatencyNanos > 0)
			.map((point) => [point.writeIOPS, point.writeLatencyNanos / 1_000_000, point.time, point.writeBytesPerSecond]);

		return {
			animationDuration: 600,
			color: ['#38bdf8', '#8b5cf6'],
			grid: { left: 12, right: 18, top: 42, bottom: 18, containLabel: true },
			legend: {
				top: 2,
				right: 4,
				itemWidth: 8,
				itemHeight: 8,
				icon: 'circle',
				textStyle: { color: foreground, fontSize: 10 }
			},
			tooltip: {
				trigger: 'item',
				backgroundColor: dark ? '#343434' : '#fafaf9',
				borderColor: dark ? '#525252' : '#d6d3d1',
				textStyle: { color: dark ? '#f4f4f5' : '#27272a' },
				formatter: (params) => {
					if (!Array.isArray(params.data)) return '';
					return [
						`<strong>${params.seriesName}</strong> · ${new Date(Number(params.data[2])).toLocaleString()}`,
						`${compactNumber(Number(params.data[0]))} IOPS`,
						`${Number(params.data[1]).toFixed(2)} ms average latency`
					].join('<br/>');
				}
			},
			xAxis: {
				type: 'value',
				min: 0,
				name: 'IOPS',
				nameLocation: 'middle',
				nameGap: 24,
				nameTextStyle: { color: foreground, fontSize: 10 },
				axisLine: { lineStyle: { color: grid } },
				axisTick: { show: false },
				axisLabel: { color: foreground, fontSize: 10, formatter: (value: number) => compactNumber(value) },
				splitLine: { lineStyle: { color: grid, type: 'dashed' } }
			},
			yAxis: {
				type: 'value',
				min: 0,
				name: 'Latency (ms)',
				nameTextStyle: { color: foreground, fontSize: 10 },
				axisLine: { show: false },
				axisTick: { show: false },
				axisLabel: { color: foreground, fontSize: 10 },
				splitLine: { lineStyle: { color: grid, type: 'dashed' } }
			},
			series: [
				{
					name: 'Read',
					type: 'scatter',
					data: read,
					symbolSize: 7,
					itemStyle: { opacity: 0.55 },
					emphasis: { scale: 1.5, itemStyle: { opacity: 1 } }
				},
				{
					name: 'Write',
					type: 'scatter',
					data: write,
					symbolSize: 7,
					itemStyle: { opacity: 0.5 },
					emphasis: { scale: 1.5, itemStyle: { opacity: 1 } }
				}
			]
		};
	});
</script>

<div class="h-72 w-full">
	{#if points.some((point) => point.readIOPS > 0 || point.writeIOPS > 0)}
		<Chart {init} {options} class="h-full w-full" />
	{:else}
		<div class="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 text-center">
			<div class="bg-muted grid size-11 place-items-center rounded-full">
				<span class="icon-[mdi--chart-scatter-plot] size-5"></span>
			</div>
			<p class="text-sm font-medium">No active I/O in this range</p>
			<p class="max-w-64 text-xs">Latency points appear when the selected scope is serving operations.</p>
		</div>
	{/if}
</div>
