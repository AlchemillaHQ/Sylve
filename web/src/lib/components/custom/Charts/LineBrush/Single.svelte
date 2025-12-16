<script lang="ts">
	import { Chart } from 'svelte-echarts';
	import { init, use } from 'echarts/core';
	import { LineChart } from 'echarts/charts';
	import {
		GridComponent,
		TitleComponent,
		DataZoomComponent,
		ToolboxComponent,
		TooltipComponent,
		LegendComponent
	} from 'echarts/components';
	import { CanvasRenderer } from 'echarts/renderers';
	import * as Card from '$lib/components/ui/card/index.js';
	import { mode } from 'mode-watcher';
	import type { EChartsOption, EChartsType } from 'echarts';
	import { cssVar } from '$lib/utils';
	import { untrack } from 'svelte';

	use([
		LineChart,
		GridComponent,
		TitleComponent,
		DataZoomComponent,
		ToolboxComponent,
		CanvasRenderer,
		TooltipComponent,
		LegendComponent
	]);

	interface Props {
		title: string;
		points: { date: number; value: number }[];
		percentage: boolean;
		color: 'one' | 'two' | 'three' | 'four';
		containerClass?: string;
		containerContentHeight?: string;
	}

	let {
		title,
		points,
		color,
		percentage,
		containerClass = 'p-5',
		containerContentHeight = 'h-[360px]'
	}: Props = $props();

	let chart: EChartsType | undefined = $state(undefined);

	const colors = $derived({
		title: mode.current === 'dark' ? '#ffffff' : '#000000',
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
		},
		three: {
			main: 'rgba(34, 197, 94, 1)',
			soft: 'rgba(34, 197, 94, 0.12)',
			softStrong: 'rgba(34, 197, 94, 0.28)'
		},
		four: {
			main: 'rgba(168, 85, 247, 1)',
			soft: 'rgba(168, 85, 247, 0.12)',
			softStrong: 'rgba(168, 85, 247, 0.28)'
		},
		moveHandle:
			mode.current === 'dark'
				? {
						color: 'rgb(170, 170, 170)',
						borderColor: 'rgb(170, 170, 170)',
						soft: 'rgb(200, 200, 200, 0.6)',
						filler: 'rgb(200, 200, 200, 0.01)'
					}
				: {
						color: 'rgb(0, 0, 0)',
						borderColor: 'rgb(0, 0, 0)',
						soft: 'rgb(0, 0, 0, 0.6)',
						filler: 'rgb(0, 0, 0, 0.01)'
					}
	});

	// @wc-ignore
	let options: EChartsOption = $state.raw({
		title: {
			text: title,
			textStyle: {
				color: colors.title,
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
							tooltipHtml += `<div class="dark:bg-muted bg-white dark:text-white font-semi">${date.toLocaleString()}: ${parseFloat(value !== undefined ? Number(value).toFixed(2) : '0')}%</div>`;
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
			axisLine: {
				lineStyle: {
					color: mode.current === 'dark' ? colors.grid.dark : colors.grid.light,
					width: 1
				}
			}
		},
		yAxis: {
			type: 'value',
			max: percentage ? 100 : undefined,
			min: percentage ? 0 : undefined,
			axisLabel: {
				formatter: percentage ? '{value}%' : '{value}'
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
					lineStyle: { color: 'rgba(255,255,255,0.15)' }, // neutral, not blue, why wont this work?
					areaStyle: { color: 'rgba(0,0,0,0.35)' }
				},

				// **selected region** – this is the bar that was blue
				selectedDataBackground: {
					lineStyle: { color: colors.moveHandle.color },
					areaStyle: { color: colors.moveHandle.soft }
				},

				// filler between handles
				fillerColor: colors.moveHandle.filler,

				// the two handles
				handleStyle: {
					color: colors.moveHandle.color,
					borderColor: colors.moveHandle.color
				},

				// the larger handle when you hover over it
				moveHandleStyle: {
					color: colors.moveHandle.color,
					borderColor: colors.moveHandle.color
				},

				// on hover
				emphasis: {
					handleStyle: {
						color: colors.moveHandle.color,
						borderColor: colors.moveHandle.color
					},
					moveHandleStyle: {
						color: colors.moveHandle.color,
						borderColor: colors.moveHandle.color
					},
					handleLabel: {
						show: false
					}
				}
			}
		],
		series: [
			{
				type: 'line',
				showSymbol: false,
				smooth: true,
				data: points.map((p) => [p.date, p.value])
			}
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
		color: [colors[color].main],
		emphasis: {
			focus: 'none',
			lineStyle: {
				color: colors[color].main,
				width: 2
			},
			handleStyle: {
				color: colors[color].main,
				borderColor: colors[color].main
			},
			moveHandleStyle: {
				color: colors[color].main,
				borderColor: colors[color].main
			}
		}
	});

	let mouseIn = $state(false);

	$effect(() => {
		if (points && !mouseIn) {
			untrack(() => {
				if (chart) {
					chart.setOption({
						series: [
							{
								data: points.map((p) => [p.date, p.value])
							}
						]
					});
				}
			});
		}
	});
</script>

<Card.Root class={containerClass}>
	<Card.Content class="{containerContentHeight} w-full overflow-hidden rounded-sm p-0">
		<div
			class="h-full w-full overflow-visible"
			onmouseenter={() => (mouseIn = true)}
			onmouseleave={() => (mouseIn = false)}
		>
			<Chart {init} {options} bind:chart />
		</div>
	</Card.Content>
</Card.Root>
