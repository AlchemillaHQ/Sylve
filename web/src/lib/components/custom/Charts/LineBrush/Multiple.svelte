<script lang="ts">
	import { Chart } from 'svelte-echarts';
	import { init, use } from 'echarts/core';
	import { LineChart } from 'echarts/charts';
	import humanFormat from 'human-format';
	import {
		GridComponent,
		TitleComponent,
		DataZoomComponent,
		ToolboxComponent,
		TooltipComponent,
		LegendComponent,
		AxisPointerComponent
	} from 'echarts/components';
	import { CanvasRenderer } from 'echarts/renderers';
	import * as Card from '$lib/components/ui/card/index.js';
	import { mode } from 'mode-watcher';
	import type { EChartsOption, EChartsType } from 'echarts';
	import { cssVar } from '$lib/utils';
	import { untrack } from 'svelte';

	// register axis pointer too so trigger:'axis' is safe
	use([
		LineChart,
		GridComponent,
		TitleComponent,
		DataZoomComponent,
		ToolboxComponent,
		CanvasRenderer,
		TooltipComponent,
		LegendComponent,
		AxisPointerComponent
	]);

	interface Props {
		title: string;
		points: { date: number; value: number }[]; // primary series
		points2?: { date: number; value: number }[]; // optional extra series
		percentage: boolean;
		data: boolean;
		color: 'one' | 'two';
		color2?: 'one' | 'two'; // optional color for second series
		containerClass?: string;
		containerContentHeight?: string;
	}

	let {
		title,
		points,
		points2 = undefined,
		color,
		color2 = 'two',
		percentage,
		data,
		containerClass = 'p-5',
		containerContentHeight = 'h-[360px]'
	}: Props = $props();

	let chart: EChartsType | undefined = $state(undefined);

	const titleColor = $derived(mode.current === 'dark' ? '#ffffff' : '#000000');
	const colors = {
		grid: {
			dark: 'rgba(255,255,255,0.12)',
			light: 'rgba(0,0,0,0.12)'
		},
		tooltip: {
			background: cssVar('--muted'),
			border: cssVar('--border')
		},
		one: {
			main: 'rgba(230, 131, 47, 1)',
			soft: 'rgba(230, 131, 47, 0.12)',
			softStrong: 'rgba(230, 131, 47, 0.28)'
		},
		two: {
			main: 'rgba(47, 131, 230, 1)',
			soft: 'rgba(47, 131, 230, 0.12)',
			softStrong: 'rgba(47, 131, 230, 0.28)'
		}
	};

	// helper to coerce and filter points safely
	function cleanPoints(src?: { date: any; value: any }[]) {
		if (!Array.isArray(src)) return [];
		return src
			.map((p) => {
				const ts = Number(p?.date);
				const v = Number(p?.value);
				if (!Number.isFinite(ts)) return null;
				// allow null as value to show gaps; but prefer number
				return [ts, Number.isFinite(v) ? v : null] as [number, number | null];
			})
			.filter(Boolean) as [number, number | null][];
	}

	// @wc-ignore
	let options: EChartsOption = $state.raw({
		title: {
			text: title,
			textStyle: {
				// svelte-ignore state_referenced_locally
				color: titleColor,
				fontStyle: 'normal',
				fontSize: 16,
				fontWeight: 'bold',
				fontFamily: 'sans-serif',
				textBorderType: [5, 10],
				textBorderDashOffset: 55
			}
		},
		legend: {},
		tooltip: {
			trigger: 'axis', // kept as you had it
			axisPointer: { type: 'line' },
			formatter: (params) => {
				let tooltipHtml = `<div class="p-2 rounded">`;
				const paramArray = Array.isArray(params) ? params : [params];
				paramArray.forEach((param) => {
					if (Array.isArray(param.data) && param.data.length >= 2) {
						const timestamp = param.data[0];
						const value = param.data[1];
						if (timestamp !== undefined) {
							const date = new Date(timestamp as string | number | Date);
							let formattedValue = '';
							if (value !== undefined && value !== null) {
								if (percentage) {
									formattedValue = `${Number(value).toFixed(2)}%`;
								} else if (data) {
									formattedValue = humanFormat(Number(value));
								} else {
									formattedValue = Number(value).toFixed(2);
								}
							}

							tooltipHtml += `
                                <div class="dark:bg-muted bg-white dark:text-white font-semi">
                                    ${date.toLocaleString()}: ${formattedValue}
                                    </div>`;
						} else {
							tooltipHtml += `<div class="dark:bg-muted bg-white dark:text-white">Invalid date</div>`;
						}
					} else {
						tooltipHtml += `<div class="dark:bg-muted bg-white dark:text-white">Invalid data</div>`;
					}
				});
				tooltipHtml += `</div>`;
				return tooltipHtml;
			},
			backgroundColor: colors.tooltip.background,
			borderColor: colors.tooltip.border,
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
				color: mode.current === 'dark' ? colors.grid.dark : colors.grid.light,
				width: 1
			}
		},
		yAxis: {
			type: 'value',
			max: percentage ? 100 : undefined,
			min: percentage ? 0 : undefined,
			axisLabel: {
				formatter: function (value: number) {
					if (percentage) {
						return `${value}%`;
					} else if (data) {
						return `${humanFormat(value)}`;
					}
				}
			},
			splitLine: {
				show: true,
				lineStyle: {
					color: mode.current === 'dark' ? colors.grid.dark : colors.grid.light,
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
					lineStyle: { color: 'rgba(255,255,255,0.15)' },
					areaStyle: { color: 'rgba(0,0,0,0.35)' }
				},

				// **selected region**
				selectedDataBackground: {
					lineStyle: { color: colors[color].main },
					areaStyle: { color: colors[color].softStrong }
				},

				// main filled rectangle between handles
				fillerColor: colors[color].soft,

				// handles
				handleStyle: {
					color: colors[color].main,
					borderColor: colors[color].main
				},
				moveHandleStyle: {
					color: colors[color].main,
					borderColor: colors[color].main
				}
			}
		],
		series: [
			{
				type: 'line',
				showSymbol: false,
				smooth: true,
				data: cleanPoints(points)
			},
			...(points2
				? [
						{
							name: 'Series 2',
							type: 'line',
							showSymbol: false,
							smooth: true,
							data: cleanPoints(points2)
						}
					]
				: [])
		],
		toolbox: {
			feature: {
				saveAsImage: {
					show: true,
					title: 'Save As Image',
					backgroundColor: colors.tooltip.background,
					connectedBackgroundColor: colors.tooltip.background
				},
				restore: {}
			}
		},
		color: [colors[color].main, colors[color2 ?? 'two'].main]
	});

	let mouseIn = $state(false);

	$effect(() => {
		// only update when points exist and not hovering
		// if (!points || mouseIn) return;

		const mainSeries = {
			name: undefined,
			type: 'line',
			showSymbol: false,
			smooth: true,
			data: cleanPoints(points)
		};

		const secondSeries = points2
			? {
					name: undefined,
					type: 'line',
					showSymbol: false,
					smooth: true,
					data: cleanPoints(points2)
				}
			: undefined;

		// build full option so ECharts never sees a series without axis etc.
		const fullOption: Partial<EChartsOption> = {
			title: options.title,
			legend: options.legend,
			tooltip: options.tooltip,
			grid: options.grid,
			xAxis: options.xAxis,
			yAxis: options.yAxis,
			dataZoom: options.dataZoom,
			toolbox: options.toolbox,
			emphasis: options.emphasis,
			color: [colors[color].main, colors[color2 ?? 'two'].main],
			series: secondSeries ? [mainSeries, secondSeries] : [mainSeries]
		};

		untrack(() => {
			if (!chart || chart.isDisposed?.()) return;
			// schedule to avoid main-process setOption warnings
			requestAnimationFrame(() => {
				if (!chart || chart.isDisposed?.()) return;
				chart.setOption(fullOption, { notMerge: false, lazyUpdate: true });
			});
		});
	});
</script>

<Card.Root class={containerClass}>
	<Card.Content class="{containerContentHeight} w-full overflow-hidden rounded-sm p-0">
		<div
			role="region"
			class="h-full w-full overflow-visible"
			onmouseenter={() => (mouseIn = true)}
			onmouseleave={() => (mouseIn = false)}
		>
			<Chart {init} {options} bind:chart />
		</div>
	</Card.Content>
</Card.Root>
