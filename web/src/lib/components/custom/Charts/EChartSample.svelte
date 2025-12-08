<script lang="ts">
	import { Chart } from 'svelte-echarts';
	import { init, use } from 'echarts/core';
	import { LineChart } from 'echarts/charts';
	import {
		GridComponent,
		TitleComponent,
		DataZoomComponent,
		ToolboxComponent,
		TooltipComponent
	} from 'echarts/components';
	import { CanvasRenderer } from 'echarts/renderers';
	import * as Card from '$lib/components/ui/card/index.js';
	import { mode } from 'mode-watcher';

	// Register components (tree-shaking)
	use([
		LineChart,
		GridComponent,
		TitleComponent,
		DataZoomComponent,
		ToolboxComponent,
		CanvasRenderer,
		TooltipComponent
	]);

	import type { EChartsOption } from 'echarts';
	import { cssVar } from '$lib/utils';

	// gfs_sample.ts

	const MINUTE = 60 * 1000;
	const HOUR = 60 * MINUTE;
	const DAY = 24 * HOUR;
	const WEEK = 7 * DAY;
	const YEAR = 365 * DAY;

	// fixed "now" for reproducibility, or just use Date.now()
	const NOW = Date.UTC(2025, 0, 7, 12, 0, 0); // 2025-01-07T12:00:00Z

	// simple deterministic pseudo-random-ish function
	function valueFor(ts: number): number {
		const x = Math.sin(ts / (10 * MINUTE)) + Math.cos(ts / (3 * HOUR));
		return Math.round(50 + 30 * x); // around 50 ± 30
	}

	/**
	 * Generate a time series matching the GFS retention shape.
	 * Returns: [timestampMs, value][]
	 */
	export function generateGFSSeries(): [number, number][] {
		const points: [number, number][] = [];

		// 0–1h: every 1 minute
		for (let ago = 0; ago <= HOUR; ago += MINUTE) {
			const t = NOW - ago;
			points.push([t, valueFor(t)]);
		}

		// 1–24h: every 30 minutes (start at 1.5h to avoid overlap)
		for (let ago = HOUR + 30 * MINUTE; ago <= DAY; ago += 30 * MINUTE) {
			const t = NOW - ago;
			points.push([t, valueFor(t)]);
		}

		// 1–7d: every 3 hours (start just past 1d)
		for (let ago = DAY + 3 * HOUR; ago <= 7 * DAY; ago += 3 * HOUR) {
			const t = NOW - ago;
			points.push([t, valueFor(t)]);
		}

		// 7–30d: every 12 hours
		for (let ago = 7 * DAY + 12 * HOUR; ago <= 30 * DAY; ago += 12 * HOUR) {
			const t = NOW - ago;
			points.push([t, valueFor(t)]);
		}

		// 30–365d: every 1 week
		for (let ago = 30 * DAY + WEEK; ago <= YEAR; ago += WEEK) {
			const t = NOW - ago;
			points.push([t, valueFor(t)]);
		}

		// sort ascending by time (oldest → newest) for chart libs
		points.sort((a, b) => a[0] - b[0]);

		return points;
	}

	const data: [number, number][] = generateGFSSeries();

	const GRID_LINE = 'rgba(255,255,255,0.12)'; // for dark mode-ish
	const GRID_LINE_LIGHT = 'rgba(0,0,0,0.12)'; // for light mode-ish

	const CHART1 = 'rgba(230, 131, 47, 1)'; // main stroke
	const CHART1_SOFT = 'rgba(230, 131, 47, 0.12)'; // selection fill
	const CHART1_SOFT_STRONG = 'rgba(230, 131, 47, 0.28)'; // top bar / stronger

	let options: EChartsOption = $derived({
		title: {
			text: 'Memory Usage',
			textStyle: {
				color: mode.current === 'dark' ? '#ffffff' : '#000000',
				fontStyle: 'normal',
				fontSize: 16,
				fontWeight: 'bold',
				fontFamily: 'sans-serif',
				textBorderType: [5, 10],
				textBorderDashOffset: 55
			}
		},
		legend: {
			show: true
		},
		tooltip: {
			trigger: 'axis',
			formatter: (params) => {
				let tooltipHtml = `<div class="p-2 rounded">`;
				const paramArray = Array.isArray(params) ? params : [params];
				paramArray.forEach((param) => {
					if (Array.isArray(param.data) && param.data.length >= 2) {
						const timestamp = param.data[0];
						const value = param.data[1];
						if (timestamp !== undefined) {
							const date = new Date(timestamp as string | number | Date);
							tooltipHtml += `<div class="dark:bg-muted dark:text-white font-semi">${date.toLocaleString()}: ${value}%</div>`;
						} else {
							tooltipHtml += `<div class="dark:bg-muted dark:text-white">Invalid date</div>`;
						}
					} else {
						tooltipHtml += `<div class="dark:bg-muted dark:text-white">Invalid data</div>`;
					}
				});
				tooltipHtml += `</div>`;
				return tooltipHtml;
			},
			backgroundColor: mode.current === 'dark' ? cssVar('--muted') : cssVar('--muted'),
			borderColor: mode.current === 'dark' ? cssVar('--border') : cssVar('--border'),
			borderWidth: 1
		},
		grid: {
			left: 10,
			right: 10,
			top: 56,
			bottom: 56,
			containLabel: true
		},
		xAxis: {
			type: 'time',
			lineStyle: {
				color: mode.current === 'dark' ? GRID_LINE : GRID_LINE_LIGHT,
				width: 1
			}
		},
		yAxis: {
			type: 'value',
			max: 100,
			min: 0,
			axisLabel: {
				formatter: '{value}%'
			},
			splitLine: {
				show: true,
				lineStyle: {
					color: mode.current === 'dark' ? GRID_LINE : GRID_LINE_LIGHT,
					width: 1
				}
			}
		},

		dataZoom: [
			{
				type: 'slider',
				xAxisIndex: 0,

				// track
				backgroundColor: 'rgba(0,0,0,0)',
				borderColor: 'rgba(0,0,0,0)',

				// mini preview (behind the orange line)
				dataBackground: {
					lineStyle: { color: 'rgba(255,255,255,0.15)' }, // neutral, not blue
					areaStyle: { color: 'rgba(0,0,0,0.35)' }
				},

				// **selected region** – this is the bar that was blue
				selectedDataBackground: {
					lineStyle: { color: CHART1 }, // thin top bar color
					areaStyle: { color: CHART1_SOFT_STRONG } // darker band under it
				},

				// main filled rectangle between handles
				fillerColor: CHART1_SOFT,

				// handles
				handleStyle: {
					color: CHART1,
					borderColor: CHART1
				},
				moveHandleStyle: {
					color: CHART1,
					borderColor: CHART1
				}
			}
		],
		series: [
			{
				type: 'line',
				showSymbol: false,
				smooth: true,
				data
			}
		],
		toolbox: {
			feature: {
				saveAsImage: {
					show: true,
					title: 'Save As Image',
					backgroundColor: mode.current === 'dark' ? cssVar('--muted') : cssVar('--muted'),
					connectedBackgroundColor: mode.current === 'dark' ? cssVar('--muted') : cssVar('--muted')
				},
				restore: {}
			}
		},
		color: [CHART1],
		emphasis: {
			focus: 'none',
			lineStyle: {
				color: CHART1, // same as normal color
				width: 2
			},
			handleStyle: {
				color: CHART1,
				borderColor: CHART1
			},
			moveHandleStyle: {
				color: CHART1,
				borderColor: CHART1
			}
		}
	});

	let containerClass = 'p-5';
	let label = 'Usage';
	let icon = '';
	let description = '';
</script>

<Card.Root class={containerClass}>
	<Card.Content class="h-[360px] w-full overflow-hidden rounded-sm p-0">
		<Chart {init} {options} class="h-full w-full" />
	</Card.Content>
</Card.Root>
