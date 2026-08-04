<script lang="ts" module>
	export type PerformanceMetric = 'bandwidth' | 'operations' | 'latency';
</script>

<script lang="ts">
	import { Chart } from '@alchemilla/svelte-echarts';
	import { LineChart } from 'echarts/charts';
	import { AxisPointerComponent, DataZoomComponent, GridComponent, LegendComponent, TooltipComponent } from 'echarts/components';
	import { init, use } from 'echarts/core';
	import { CanvasRenderer } from 'echarts/renderers';
	import { mode } from 'mode-watcher';
	import type { EChartsOption } from 'echarts';
	import { getZFSChartTheme } from './chartTheme';
	import type { PoolStatPoint } from '$lib/types/zfs/pool';
	import { formatBytesPerSecondBinary } from '$lib/utils/bytes';

	use([
		LineChart,
		AxisPointerComponent,
		DataZoomComponent,
		GridComponent,
		LegendComponent,
		TooltipComponent,
		CanvasRenderer
	]);

	interface Props {
		points: PoolStatPoint[];
		metric: PerformanceMetric;
	}

	let { points, metric }: Props = $props();

	function compactNumber(value: number): string {
		return new Intl.NumberFormat(undefined, {
			notation: 'compact',
			maximumFractionDigits: 1
		}).format(value);
	}

	function formatValue(value: number): string {
		if (metric === 'bandwidth') return formatBytesPerSecondBinary(value);
		if (metric === 'latency') return `${value.toFixed(value >= 10 ? 1 : 2)} ms`;
		return `${compactNumber(value)} IOPS`;
	}

	function metricValue(point: PoolStatPoint, direction: 'read' | 'write'): number {
		if (metric === 'bandwidth') {
			return direction === 'read' ? point.readBytesPerSecond : point.writeBytesPerSecond;
		}
		if (metric === 'latency') {
			return (direction === 'read' ? point.readLatencyNanos : point.writeLatencyNanos) / 1_000_000;
		}
		return direction === 'read' ? point.readIOPS : point.writeIOPS;
	}

	let options: EChartsOption = $derived.by(() => {
		const dark = mode.current === 'dark';
		const theme = getZFSChartTheme(dark);
		const readData = points.map((point) => [point.time, metricValue(point, 'read')]);
		const writeData = points.map((point) => [point.time, metricValue(point, 'write')]);

		return {
			animationDuration: 300,
			color: [theme.read, theme.write],
			grid: {
				left: 10,
				right: 10,
				top: 42,
				bottom: 64,
				outerBoundsMode: 'same',
				outerBoundsContain: 'axisLabel'
			},
			legend: {
				top: 4,
				right: 4,
				itemWidth: 9,
				itemHeight: 9,
				icon: 'circle',
				textStyle: { color: theme.muted, fontSize: 12 }
			},
			tooltip: {
				trigger: 'axis',
				backgroundColor: theme.tooltip,
				borderColor: theme.tooltipBorder,
				textStyle: { color: theme.foreground },
				axisPointer: { type: 'line', lineStyle: { color: theme.muted } },
				formatter: (params) => {
					const entries = Array.isArray(params) ? params : [params];
					const timestamp = Array.isArray(entries[0]?.data) ? Number(entries[0].data[0]) : 0;
					const lines = [`<strong>${new Date(timestamp).toLocaleString()}</strong>`];
					for (const entry of entries) {
						if (!Array.isArray(entry.data)) continue;
						lines.push(`${entry.marker ?? ''}${entry.seriesName}: ${formatValue(Number(entry.data[1]))}`);
					}
					return lines.join('<br/>');
				}
			},
			xAxis: {
				type: 'time',
				boundaryGap: false,
				axisLine: { lineStyle: { color: theme.grid } },
				axisTick: { show: false },
				axisLabel: {
					color: theme.muted,
					fontSize: 11,
					hideOverlap: true,
					formatter: (value: number) => {
						const date = new Date(value);
						return `${date.toLocaleDateString([], { month: 'short', day: 'numeric' })}\n${date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
					}
				},
				splitLine: { show: false }
			},
			yAxis: {
				type: 'value',
				min: 0,
				axisLine: { show: false },
				axisTick: { show: false },
				axisLabel: {
					color: theme.muted,
					fontSize: 11,
					formatter: (value: number) =>
						metric === 'bandwidth'
							? formatBytesPerSecondBinary(value)
							: metric === 'latency'
								? `${value.toFixed(0)} ms`
								: compactNumber(value)
				},
				splitLine: { lineStyle: { color: theme.grid, type: 'dashed' } }
			},
			dataZoom: [
				{
					type: 'slider',
					filterMode: 'none',
					height: 22,
					left: 10,
					right: 10,
					bottom: 8,
					borderColor: 'transparent',
					backgroundColor: theme.grid,
					fillerColor: dark ? 'rgba(255,255,255,0.12)' : 'rgba(24,24,27,0.12)',
					dataBackground: { lineStyle: { color: theme.muted }, areaStyle: { color: theme.grid } },
					selectedDataBackground: { lineStyle: { color: theme.read }, areaStyle: { color: theme.grid } },
					handleSize: '70%',
					showDetail: false
				}
			],
			series: [
				{
					name: 'Read',
					type: 'line',
					data: readData,
					showSymbol: false,
					smooth: false,
					sampling: 'lttb',
					lineStyle: { width: 2 },
					emphasis: { focus: 'series' }
				},
				{
					name: 'Write',
					type: 'line',
					data: writeData,
					showSymbol: false,
					smooth: false,
					sampling: 'lttb',
					lineStyle: { width: 2, type: 'dashed' },
					emphasis: { focus: 'series' }
				}
			]
		};
	});
</script>

<div class="h-80 w-full">
	{#if points.length > 0}
		<Chart {init} {options} class="h-full w-full" />
	{:else}
		<div class="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 text-center" role="status">
			<div class="bg-muted grid size-11 place-items-center rounded-full">
				<span class="icon-[mdi--chart-timeline-variant-shimmer] size-5"></span>
			</div>
			<p class="text-sm font-medium">Performance history is warming up</p>
			<p class="max-w-64 text-xs">Read and write samples will appear here as they are collected.</p>
		</div>
	{/if}
</div>
