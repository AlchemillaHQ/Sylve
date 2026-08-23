<script lang="ts">
	import { Chart } from '@alchemilla/svelte-echarts';
	import { PieChart } from 'echarts/charts';
	import { TooltipComponent } from 'echarts/components';
	import { init, use } from 'echarts/core';
	import { CanvasRenderer } from 'echarts/renderers';
	import { mode } from 'mode-watcher';
	import type { EChartsOption } from 'echarts';
	import type { PoolStatPoint } from '$lib/types/zfs/pool';
	import { formatBytesPerSecondBinary } from '$lib/utils/bytes';

	use([PieChart, TooltipComponent, CanvasRenderer]);

	interface Props {
		point: PoolStatPoint | null;
	}

	let { point }: Props = $props();
	let read = $derived(point?.readBytesPerSecond ?? 0);
	let write = $derived(point?.writeBytesPerSecond ?? 0);
	let total = $derived(read + write);
	let readShare = $derived(total > 0 ? (read / total) * 100 : 0);
	let writeShare = $derived(total > 0 ? (write / total) * 100 : 0);
	let totalIOPS = $derived((point?.readIOPS ?? 0) + (point?.writeIOPS ?? 0));
	let averageLatency = $derived(
		point && totalIOPS > 0
			? (point.readLatencyNanos * point.readIOPS + point.writeLatencyNanos * point.writeIOPS) /
					totalIOPS /
					1_000_000
			: null
	);

	let options: EChartsOption = $derived.by(() => {
		const dark = mode.current === 'dark';
		return {
			animationDuration: 700,
			color: ['#38bdf8', '#8b5cf6'],
			tooltip: {
				trigger: 'item',
				backgroundColor: dark ? '#343434' : '#fafaf9',
				borderColor: dark ? '#525252' : '#d6d3d1',
				textStyle: { color: dark ? '#f4f4f5' : '#27272a' },
				formatter: (params) => {
					if (Array.isArray(params)) return '';
					return `${params.marker ?? ''}${params.name}: ${formatBytesPerSecondBinary(Number(params.value))}`;
				}
			},
			series: [
				{
					type: 'pie',
					radius: ['66%', '88%'],
					center: ['50%', '50%'],
					avoidLabelOverlap: true,
					itemStyle: {
						borderColor: dark ? '#434343' : '#f6f5f2',
						borderWidth: 4,
						borderRadius: 7
					},
					label: { show: false },
					emphasis: { scaleSize: 4 },
					data:
						total > 0
							? [
									{ value: read, name: 'Read' },
									{ value: write, name: 'Write' }
								]
							: [
									{ value: 1, name: 'Read', itemStyle: { color: dark ? '#52525b' : '#d4d4d8' } },
									{ value: 1, name: 'Write', itemStyle: { color: dark ? '#3f3f46' : '#e4e4e7' } }
								]
				}
			]
		};
	});
</script>

<div class="grid min-h-72 grid-cols-[minmax(130px,0.85fr)_minmax(140px,1.15fr)] items-center gap-4">
	<div class="relative mx-auto size-44 max-w-full">
		<Chart {init} {options} class="h-full w-full" />
		<div
			class="pointer-events-none absolute inset-0 flex flex-col items-center justify-center text-center"
		>
			<span class="text-muted-foreground text-[10px] font-medium tracking-wide uppercase"
				>Total</span
			>
			<span class="mt-1 text-lg font-semibold tracking-tight tabular-nums"
				>{formatBytesPerSecondBinary(total)}</span
			>
		</div>
	</div>

	<div class="space-y-4">
		<div>
			<div class="flex items-center justify-between gap-3 text-xs">
				<span class="flex items-center gap-2 font-medium"
					><span class="size-2 rounded-full bg-sky-400"></span>Read</span
				>
				<span class="tabular-nums">{formatBytesPerSecondBinary(read)}</span>
			</div>
			<div class="bg-muted mt-2 h-1.5 overflow-hidden rounded-full">
				<div class="h-full rounded-full bg-sky-400" style:width={`${readShare}%`}></div>
			</div>
		</div>
		<div>
			<div class="flex items-center justify-between gap-3 text-xs">
				<span class="flex items-center gap-2 font-medium"
					><span class="size-2 rounded-full bg-violet-500"></span>Write</span
				>
				<span class="tabular-nums">{formatBytesPerSecondBinary(write)}</span>
			</div>
			<div class="bg-muted mt-2 h-1.5 overflow-hidden rounded-full">
				<div class="h-full rounded-full bg-violet-500" style:width={`${writeShare}%`}></div>
			</div>
		</div>

		<div class="grid grid-cols-2 gap-2 border-t pt-4">
			<div class="bg-muted/45 rounded-lg p-2.5">
				<p class="text-muted-foreground text-[10px] uppercase">IOPS</p>
				<p class="mt-1 text-sm font-semibold tabular-nums">{totalIOPS.toLocaleString()}</p>
			</div>
			<div class="bg-muted/45 rounded-lg p-2.5">
				<p class="text-muted-foreground text-[10px] uppercase">Avg latency</p>
				<p class="mt-1 text-sm font-semibold tabular-nums">
					{averageLatency === null ? '—' : `${averageLatency.toFixed(1)} ms`}
				</p>
			</div>
		</div>
	</div>
</div>
