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
	import { c } from '../../../../../locales/.wuchale/main.main.en.compiled';

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

	interface SeriesData {
		name: string;
		points: { date: number; value: number }[];
		color: 'one' | 'two' | 'three' | 'four';
	}

	interface Props {
		title: string;
		series: SeriesData[];
		percentage: boolean;
		data: boolean;
		containerClass?: string;
		containerContentHeight?: string;
	}

	let {
		title,
		series,
		percentage,
		data,
		containerClass = 'p-5',
		containerContentHeight = 'h-[360px]'
	}: Props = $props();

	let chart: EChartsType | undefined = $state(undefined);

	const titleColor = $derived(mode.current === 'dark' ? '#ffffff' : '#000000');
	const legendTextColor = $derived(mode.current === 'dark' ? '#ffffff' : '#000000');

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
	};

	const primaryColor = $derived(series.length > 0 ? series[0].color : 'one');
	const seriesColors = $derived(series.map((s) => colors[s.color].main));
	const gridColor = $derived(mode.current === 'dark' ? colors.grid.dark : colors.grid.light);

	function cleanPoints(src?: { date: any; value: any }[]) {
		if (!Array.isArray(src)) return [];
		return src
			.map((p) => {
				const ts = Number(p?.date);
				const v = Number(p?.value);
				if (!Number.isFinite(ts)) return null;
				return [ts, Number.isFinite(v) ? v : null] as [number, number | null];
			})
			.filter(Boolean) as [number, number | null][];
	}

	// Create a function to generate options based on current state
	function getOptions(): EChartsOption {
		return {
			title: {
				text: title,
				textStyle: {
					color: titleColor,
					fontStyle: 'normal',
					fontSize: 16,
					fontWeight: 'bold',
					fontFamily: 'sans-serif',
					textBorderType: [5, 10],
					textBorderDashOffset: 55
				}
			},
			legend: {
				show: true,
				top: 5,
				textStyle: {
					color: legendTextColor
				}
			},
			tooltip: {
				trigger: 'axis',
				axisPointer: {
					type: 'line'
				},
				formatter: (params) => {
					let tooltipHtml = `<div class="p-2 rounded">`;
					const paramArray = Array.isArray(params) ? params : [params];

					if (paramArray.length > 0 && Array.isArray(paramArray[0].data)) {
						const timestamp = paramArray[0].data[0];
						if (timestamp !== undefined) {
							const date = new Date(timestamp as string | number | Date);
							tooltipHtml += `<div class="font-semibold mb-1">${date.toLocaleString()}</div>`;
						}
					}

					paramArray.forEach((param) => {
						if (Array.isArray(param.data) && param.data.length >= 2) {
							const value = param.data[1];
							const seriesName = param.seriesName || 'Unknown';

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

							tooltipHtml += `<div class="flex items-center gap-2">
								<span style="display:inline-block;width:10px;height:10px;background-color:${param.color};border-radius:50%;"></span>
								<span>${seriesName}: ${formattedValue}</span>
							</div>`;
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
				top: 70,
				bottom: 56,
				containLabel: true
			},
			xAxis: {
				type: 'time',
				axisLine: {
					lineStyle: {
						color: gridColor,
						width: 1
					}
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
						return value.toString();
					}
				},
				splitLine: {
					show: true,
					lineStyle: {
						color: gridColor,
						width: 1
					}
				}
			},
			dataZoom: [
				{
					type: 'slider',
					xAxisIndex: 0,
					backgroundColor: 'rgba(0,0,0,0)',
					borderColor: 'rgba(0,0,0,0)',
					dataBackground: {
						lineStyle: { color: 'rgba(255,255,255,0.15)' },
						areaStyle: { color: 'rgba(0,0,0,0.35)' }
					},
					selectedDataBackground: {
						lineStyle: { color: colors.moveHandle.color },
						areaStyle: { color: colors.moveHandle.soft }
					},
					fillerColor: colors.moveHandle.filler,
					handleStyle: {
						color: colors.moveHandle.color,
						borderColor: colors.moveHandle.color
					},
					moveHandleStyle: {
						color: colors.moveHandle.color,
						borderColor: colors.moveHandle.color
					},
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
			series: series.map((s) => ({
				name: s.name,
				type: 'line',
				showSymbol: false,
				smooth: true,
				data: cleanPoints(s.points)
			})),
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
			color: seriesColors
		};
	}

	let mouseIn = $state(false);

	$effect(() => {
		if (!chart || chart.isDisposed?.()) return;

		untrack(() => {
			requestAnimationFrame(() => {
				if (!chart || chart.isDisposed?.()) return;
				chart.setOption(getOptions(), { notMerge: false, lazyUpdate: true });
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
			<Chart {init} options={getOptions()} bind:chart />
		</div>
	</Card.Content>
</Card.Root>
