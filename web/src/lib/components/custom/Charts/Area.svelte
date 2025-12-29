<script lang="ts">
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import type { AreaChartElement } from '$lib/types/components/chart';
	import { switchColor } from '$lib/utils/chart';
	import {
		CategoryScale,
		Chart,
		Filler,
		Legend,
		LinearScale,
		LineController,
		LineElement,
		PointElement,
		Title,
		Tooltip
	} from 'chart.js';
	import zoomPlugin from 'chartjs-plugin-zoom';
	import { format } from 'date-fns';
	import humanFormat from 'human-format';
	import { onDestroy, onMount } from 'svelte';

	interface Props {
		title?: string;
		description?: string;
		icon?: string;
		elements: AreaChartElement[];
		formatSize?: boolean;
		containerClass?: string;
		showResetButton?: boolean;
		chart: Chart | null;
		percentage?: boolean;
	}

	let {
		title = '',
		description = '',
		icon = '',
		elements,
		formatSize = false,
		containerClass = 'p-5',
		showResetButton = true,
		chart = $bindable(),
		percentage = false
	}: Props = $props();

	let data = $derived.by(() => {
		if (!elements?.length) return [];

		const THRESH = 60_000;
		const series = elements.map(({ field, data: pts }) => ({
			field,
			points: pts
				.map((p) => ({ t: new Date(p.date).getTime(), v: p.value }))
				.sort((a, b) => a.t - b.t)
		}));

		series.sort((a, b) => a.points.length - b.points.length);
		const [base, ...others] = series;

		const out = [];

		for (const { t: bt, v: bv } of base.points) {
			const rec = { date: new Date(bt), [base.field]: bv };
			let good = true;

			for (const { field, points } of others) {
				let bestDiff = Infinity,
					bestVal = null;
				for (const { t, v } of points) {
					const d = Math.abs(t - bt);
					if (d < bestDiff) {
						bestDiff = d;
						bestVal = v;
					}

					if (t - bt > bestDiff) break;
				}
				if (bestVal === null || bestDiff > THRESH) {
					good = false;
					break;
				}
				rec[field] = bestVal;
			}

			if (good) out.push(rec);
		}

		return out;
	});

	let series = $derived.by(() => {
		if (!elements?.length) return [];
		return elements.map((element) => ({
			key: element.field,
			label: element.label,
			color: element.color
		}));
	});

	const labels = $derived.by(() => {
		return data.map((v) => [
			format(new Date(v.date), 'dd/MM/yyyy'),
			format(new Date(v.date), 'HH:mm')
		]);
	});

	let datasets = $derived.by(() => {
		return series.map((s, i) => ({
			label: s.label,
			data: data.map((d) => Number(d[s.key])),
			borderColor: switchColor(s.color),
			backgroundColor: switchColor(s.color, 0.2),
			fill: i === 0 ? 'origin' : '-1',
			tension: 0.4,
			pointRadius: 0,
			pointHoverRadius: 4,
			order: s.label === 'CPU Usage' ? 2 : 1
		}));
	});

	let canvas: HTMLCanvasElement;
	let zoomEnabled = $state(false);

	Chart.register(
		LineController,
		LineElement,
		PointElement,
		LinearScale,
		CategoryScale,
		Title,
		Tooltip,
		Legend,
		Filler,
		zoomPlugin
	);

	onMount(() => {
		chart = new Chart(canvas, {
			type: 'line',
			data: {
				labels,
				datasets
			},
			options: {
				responsive: true,
				maintainAspectRatio: false,
				transitions: {
					zoom: {
						animation: {
							duration: 1000,
							easing: 'easeOutCubic'
						}
					}
				},
				plugins: {
					legend: {
						position: 'top'
					},
					tooltip: {
						mode: 'index',
						intersect: false,
						callbacks: {
							title: (tooltipItems) => {
								try {
									const dataIndex = tooltipItems[0].dataIndex;
									const date = data[dataIndex]?.date;
									if (!date) return 'Invalid Date';

									const dateObj = date instanceof Date ? date : new Date(date);
									if (isNaN(dateObj.getTime())) return 'Invalid Date';

									return [format(dateObj, 'dd/MM/yyyy'), format(dateObj, 'HH:mm:ss')];
								} catch (e) {
									return 'Invalid Date';
								}
							},
							label: (tooltipItem) => {
								const datasetLabel = tooltipItem.dataset.label || '';
								const value = Number(tooltipItem.raw);

								return `${datasetLabel}: ${
									formatSize ? humanFormat(value) : value.toLocaleString()
								}`;
							}
						}
					},
					zoom: {
						zoom: {
							wheel: { enabled: zoomEnabled },
							pinch: { enabled: zoomEnabled },
							mode: 'xy'
						},
						pan: {
							enabled: zoomEnabled,
							mode: 'xy'
						}
					}
				},
				scales: {
					x: {
						title: { color: '#ccc', display: true, text: 'Date' },
						ticks: {
							callback: function (value, index, ticks) {
								try {
									const date = data[index]?.date;
									if (!date) return '';

									const dateObj = date instanceof Date ? date : new Date(date);
									if (isNaN(dateObj.getTime())) return '';

									return [format(date, 'dd/MM/yyyy'), format(date, 'HH:mm')];
								} catch (e) {
									return '';
								}
							}
						},
						grid: {
							color: '#333' // Optional: X-axis grid line color
						}
					},
					y: {
						min: percentage ? 0 : undefined,
						max: percentage ? 100 : undefined,
						beginAtZero: true,
						title: {
							color: '#ccc',
							display: true,
							text: 'Value'
						},
						ticks: {
							callback: function (value) {
								const numValue = Number(value);
								return formatSize ? humanFormat(numValue) : numValue.toLocaleString();
							}
						},
						grid: {
							color: '#333' // Optional: X-axis grid line color
						}
					}
				}
			}
		});
		setTimeout(() => chart?.resize(), 300);
	});

	$effect(() => {
		if (chart) {
			if (
				chart.options &&
				chart.options.plugins &&
				chart.options.plugins.zoom &&
				chart.options.plugins.zoom.zoom &&
				chart.options.plugins.zoom.zoom.wheel
			) {
				chart.options.plugins.zoom.zoom.wheel.enabled = zoomEnabled;
			}
			if (
				chart.options &&
				chart.options.plugins &&
				chart.options.plugins.zoom &&
				chart.options.plugins.zoom.zoom &&
				chart.options.plugins.zoom.zoom.pinch
			) {
				chart.options.plugins.zoom.zoom.pinch.enabled = zoomEnabled;
			}
			if (
				chart.options &&
				chart.options.plugins &&
				chart.options.plugins.zoom &&
				chart.options.plugins.zoom.pan
			) {
				chart.options.plugins.zoom.pan.enabled = zoomEnabled;
			}
			chart.update('none');
		}
	});

	onDestroy(() => {
		if (chart) {
			chart.destroy();
			chart = null;
		}
	});
</script>

<Card.Root class={containerClass}>
	<Card.Header class="p-0">
		<Card.Title class="flex items-center justify-between gap-4">
			<div class="flex items-center gap-2">
				{#if icon}
					<span class={icon}></span>
				{/if}
				{title}
			</div>
			<div class="flex items-center gap-2">
				<Button
					onclick={() => {
						zoomEnabled = !zoomEnabled;
					}}
					variant={zoomEnabled ? 'default' : 'outline'}
					size="sm"
					class="h-8"
				>
					<span class="icon-[material-symbols--zoom-in] h-4 w-4"></span>
					{zoomEnabled ? 'Disable Zoom' : 'Enable Zoom'}
				</Button>
				{#if showResetButton && zoomEnabled}
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
				{/if}
			</div>
		</Card.Title>
		{#if description}
			<Card.Description>{description}</Card.Description>
		{/if}
	</Card.Header>

	<Card.Content class="h-full min-h-[300px] w-full p-0">
		<canvas bind:this={canvas} style="width: 100%; height: 100%; display: block;"></canvas>
	</Card.Content>
</Card.Root>
