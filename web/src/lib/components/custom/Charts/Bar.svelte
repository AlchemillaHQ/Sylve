<script lang="ts">
	import Button from '$lib/components/ui/button/button.svelte';
	import type { SeriesDataWithBaseline } from '$lib/types/common';
	import { switchColor } from '$lib/utils/chart';
	import {
		BarController,
		BarElement,
		CategoryScale,
		Chart,
		Legend,
		LinearScale,
		Title,
		Tooltip,
		type ChartConfiguration
	} from 'chart.js';
	import zoomPlugin from 'chartjs-plugin-zoom';
	import { format } from 'date-fns';
	import humanFormat from 'human-format';
	import { onDestroy, onMount } from 'svelte';

	Chart.register(
		CategoryScale,
		LinearScale,
		BarController,
		BarElement,
		Title,
		Tooltip,
		Legend,
		zoomPlugin
	);

	type Colors = {
		baseline: string;
		value: string;
	};

	interface Props {
		data: SeriesDataWithBaseline[];
		colors: Colors;
		formatter?: 'size-formatter' | 'default';
		icon?: string;
		title?: string;
		showResetButton?: boolean;
		chart?: Chart;
	}

	let {
		title,
		icon,
		data,
		colors,
		formatter = 'default',
		showResetButton = true,
		chart = $bindable()
	}: Props = $props();

	let canvas: HTMLCanvasElement;

	const chartConfig: ChartConfiguration<'bar'> = {
		type: 'bar',
		data: {
			labels: data.map((d) => d.name),
			datasets: [
				{
					label: 'Baseline',
					data: data.map((d) => d.baseline),
					backgroundColor: switchColor(colors.baseline, 0.6),
					borderColor: switchColor(colors.baseline, 1),
					borderWidth: 1
				},
				{
					label: 'Value',
					data: data.map((d) => d.value),
					backgroundColor: switchColor(colors.value, 0.6),
					borderColor: switchColor(colors.value, 1),
					borderWidth: 1
				}
			]
		},
		options: {
			responsive: true,
			maintainAspectRatio: false,
			plugins: {
				legend: {
					position: 'top'
				},
				tooltip: {
					callbacks: {
						label: function (context) {
							const label = context.label || '';
							const value = context.raw as number;
							const displayValue = formatter === 'size-formatter' ? humanFormat(value) : value;
							return `${label}: ${displayValue}`;
						}
					}
				},
				zoom: {
					pan: { enabled: true, mode: 'xy' },
					zoom: {
						wheel: { enabled: true },
						pinch: { enabled: true },
						mode: 'xy'
					}
				}
			},
			scales: {
				x: {
					title: { color: '#ccc', display: true, text: 'Date' },

					grid: {
						color: '#333'
					}
				},
				y: {
					beginAtZero: true,
					title: {
						color: '#ccc',
						display: true,
						text: 'Value'
					},
					ticks: {
						callback: function (value) {
							const numValue = Number(value);
							return formatter == 'size-formatter'
								? humanFormat(numValue)
								: numValue.toLocaleString();
						}
					},
					grid: {
						color: '#333'
					}
				}
			},
			interaction: {
				mode: 'index',
				intersect: false
			}
		}
	};

	onMount(() => {
		if (canvas) {
			chart = new Chart(canvas, chartConfig);
		}
	});

	$effect(() => {
		if (chart && data && data.length > 0) {
			chart.data.labels = data.map((d) => d.name);

			chart.data.datasets[0].data = data.map((d) => d.baseline);
			chart.data.datasets[0].backgroundColor = switchColor(colors.baseline, 0.6);
			chart.data.datasets[0].borderColor = switchColor(colors.baseline, 1);

			chart.data.datasets[1].data = data.map((d) => d.value);
			chart.data.datasets[1].backgroundColor = switchColor(colors.value, 0.6);
			chart.data.datasets[1].borderColor = switchColor(colors.value, 1);

			chart.update();
		}
	});

	onDestroy(() => {
		chart?.destroy();
	});
</script>

<div class="relative min-h-[300px] w-full">
	<div class="flex items-center justify-between gap-4">
		<div class="flex items-center gap-2">
			{#if icon}
				<span class="icon-[{icon}] h-5 w-5"></span>
			{/if}
			{title}
		</div>
		{#if showResetButton}
			<div>
				<Button
					onclick={() => {
						chart?.resetZoom();
					}}
					variant="outline"
					size="sm"
					class="h-8"
				>
					<span class="icon-[carbon--reset] h-4 w-4"></span>
					Reset zoom
				</Button>
			</div>
		{/if}
	</div>

	<div class="h-full min-h-[300px] w-full">
		<canvas bind:this={canvas} class="h-full w-full"></canvas>
	</div>
</div>
