<script lang="ts">
	import { Chart } from '@alchemilla/svelte-echarts';
	import { init, use } from 'echarts/core';
	import { LineChart } from 'echarts/charts';
	import {
		formatBytesBinary,
		formatBytesPerSecondBinary,
		formatBitsPerSecondDecimal
	} from '$lib/utils/bytes';
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
	import { watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';

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

	type ValueType = 'auto' | 'number' | 'bytes' | 'bytesPerSecond' | 'bitsPerSecond';

	interface Props {
		title: string;
		titleIconClass?: string;
		series: SeriesData[];
		percentage: boolean;
		data: boolean;
		types?: ValueType;
		smooth?: boolean;
		containerClass?: string;
		containerContentHeight?: string;
		animateOnMount?: boolean;
	}

	let {
		title,
		titleIconClass = '',
		series,
		percentage,
		data,
		types = 'auto',
		smooth = true,
		containerClass = 'p-5',
		containerContentHeight = 'h-[360px]',
		animateOnMount = false
	}: Props = $props();

	const mountAnimationDuration = 1400;
	const mountAnimationEnabled = untrack(() => animateOnMount);
	let chart: EChartsType | undefined = $state(undefined);
	let optionRafId: number | null = null;
	let restoreRafId: number | null = null;
	let mountAnimatedChart: EChartsType | undefined;
	let mountAnimationRevealTimer: ReturnType<typeof setTimeout> | null = null;
	let mountAnimationSyncTimer: ReturnType<typeof setTimeout> | null = null;
	let mountAnimationReady = !mountAnimationEnabled;

	const titleColor = $derived(mode.current === 'dark' ? '#ffffff' : '#000000');
	const legendTextColor = $derived(mode.current === 'dark' ? '#ffffff' : '#000000');

	const colors = $derived({
		grid: {
			dark: 'rgba(255,255,255,0.12)',
			light: 'rgba(0,0,0,0.12)'
		},
		tooltip: {
			background: 'var(--muted)',
			border: 'var(--border)',
			text: 'var(--foreground)'
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
						filler: 'rgb(200, 200, 200, 0.1)'
					}
				: {
						color: 'rgb(165, 165, 165)',
						borderColor: 'rgb(165, 165, 165)',
						soft: 'rgb(195, 195, 195, 0.6)',
						filler: 'rgb(195, 195, 195, 0.1)'
					}
	});

	const seriesColors = $derived(series.map((s) => colors[s.color].main));
	const gridColor = $derived(mode.current === 'dark' ? colors.grid.dark : colors.grid.light);

	function cleanPoints(src?: { date: unknown; value: unknown }[]) {
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

	function getEffectiveValueType(): 'percentage' | 'number' | 'human' | Exclude<ValueType, 'auto'> {
		if (types !== 'auto') return types;
		if (percentage) return 'percentage';
		if (data) return 'human';
		return 'number';
	}

	function formatValue(value: number, axis = false): string {
		const type = getEffectiveValueType();

		switch (type) {
			case 'percentage':
				return axis ? `${value}%` : `${Number(value).toFixed(2)}%`;
			case 'human':
				return formatBytesBinary(value);
			case 'bytes':
				return formatBytesBinary(value);
			case 'bytesPerSecond':
				return formatBytesPerSecondBinary(value);
			case 'bitsPerSecond':
				return formatBitsPerSecondDecimal(value);
			default:
				return axis ? value.toString() : Number(value).toFixed(2);
		}
	}

	function buildSeries() {
		const mainSeries = series.map((s, index) => ({
			id: `main-${index}`,
			name: s.name,
			type: 'line' as const,
			xAxisIndex: 0,
			yAxisIndex: 0,
			showSymbol: false,
			smooth,
			lineStyle: {
				color: colors[s.color].main
			},
			data: cleanPoints(s.points)
		}));

		const previewSeries = series.map((s, index) => ({
			id: `preview-${index}`,
			type: 'line' as const,
			xAxisIndex: 1,
			yAxisIndex: 1,
			showSymbol: false,
			smooth: false,
			silent: true,
			animation: false,
			lineStyle: {
				color: colors[s.color].main,
				width: 1
			},
			tooltip: {
				show: false
			},
			data: cleanPoints(s.points)
		}));

		return [...mainSeries, ...previewSeries];
	}

	function getOptions(includeSeries = true): EChartsOption {
		return {
			animation: mountAnimationEnabled ? true : undefined,
			animationDuration: mountAnimationEnabled ? mountAnimationDuration : undefined,
			animationEasing: mountAnimationEnabled ? 'cubicInOut' : undefined,
			title: {
				show: false,
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
							tooltipHtml += `<div class="font-semi mb-1" style="color:${colors.tooltip.text}">${date.toLocaleString()}</div>`;
						}
					}

					paramArray.forEach((param) => {
						if (Array.isArray(param.data) && param.data.length >= 2) {
							const value = param.data[1];
							const seriesName = param.seriesName || 'Unknown';

							let formattedValue = '';
							if (value !== undefined && value !== null) {
								formattedValue = formatValue(Number(value));
							}

							tooltipHtml += `<div class="font-semi" style="color:${colors.tooltip.text}">${seriesName}: ${formattedValue}</div>`;
						}
					});
					tooltipHtml += `</div>`;
					return tooltipHtml;
				},
				backgroundColor: colors.tooltip.background,
				borderColor: colors.tooltip.border,
				textStyle: {
					color: colors.tooltip.text
				},
				borderWidth: 1
			},
			grid: [
				{
					left: 10,
					right: 10,
					top: 70,
					bottom: 64,
					outerBoundsMode: 'same',
					outerBoundsContain: 'axisLabel'
				},
				{
					left: 10,
					right: 10,
					bottom: 8,
					height: 30
				}
			],
			xAxis: [
				{
					type: 'time',
					gridIndex: 0,
					axisLine: {
						lineStyle: {
							color: gridColor,
							width: 1
						}
					}
				},
				{
					type: 'time',
					gridIndex: 1,
					show: false
				}
			],
			yAxis: [
				{
					type: 'value',
					gridIndex: 0,
					max: percentage ? 100 : undefined,
					min: percentage ? 0 : undefined,
					axisLabel: {
						formatter: function (value: number) {
							return formatValue(value, true);
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
				{
					type: 'value',
					gridIndex: 1,
					min: 0,
					show: false
				}
			],
			dataZoom: [
				{
					type: 'slider',
					xAxisIndex: 0,
					showDataShadow: false,
					left: 10,
					right: 10,
					bottom: 8,
					height: 30,
					backgroundColor: 'rgba(0,0,0,0)',
					borderColor: 'rgba(0,0,0,0)',
					fillerColor: 'rgba(0,0,0,0)',
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
			series: includeSeries ? buildSeries() : [],
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

	const mountOptions = mountAnimationEnabled ? getOptions(false) : undefined;
	let mouseIn = $state(false);

	function handleRestore() {
		if (restoreRafId !== null) cancelAnimationFrame(restoreRafId);

		restoreRafId = requestAnimationFrame(() => {
			restoreRafId = null;
			if (!chart || chart.isDisposed?.()) return;

			chart.setOption(getOptions(), { notMerge: true, lazyUpdate: false });
		});
	}

	function startMountAnimation(currentChart: EChartsType) {
		if (mountAnimationRevealTimer !== null) clearTimeout(mountAnimationRevealTimer);
		if (mountAnimationSyncTimer !== null) clearTimeout(mountAnimationSyncTimer);

		mountAnimatedChart = currentChart;
		mountAnimationReady = false;
		mountAnimationRevealTimer = setTimeout(() => {
			mountAnimationRevealTimer = null;
			if (chart !== currentChart || currentChart.isDisposed?.()) return;

			const revealedSeries = series;
			const revealedMode = mode.current;
			currentChart.setOption(getOptions(), { notMerge: true, lazyUpdate: false });

			mountAnimationSyncTimer = setTimeout(() => {
				mountAnimationSyncTimer = null;
				if (chart !== currentChart || currentChart.isDisposed?.()) return;

				mountAnimationReady = true;
				if (series !== revealedSeries || mode.current !== revealedMode) {
					currentChart.setOption(getOptions(), { notMerge: true, lazyUpdate: false });
				}
			}, mountAnimationDuration);
		}, 100);
	}

	watch(
		() => chart,
		(currentChart) => {
			if (!mountAnimationEnabled || !currentChart || currentChart === mountAnimatedChart) return;
			startMountAnimation(currentChart);
		}
	);

	watch(
		[
			() => mode.current,
			() => title,
			() => series,
			() => percentage,
			() => data,
			() => types,
			() => smooth,
			() => titleIconClass
		],
		() => {
			if (!chart || chart.isDisposed?.()) return;
			if (mountAnimationEnabled && !mountAnimationReady) return;

			if (optionRafId !== null) {
				cancelAnimationFrame(optionRafId);
			}

			optionRafId = requestAnimationFrame(() => {
				if (!chart || chart.isDisposed?.()) return;
				chart.setOption(getOptions(), { notMerge: false, lazyUpdate: false });
				optionRafId = null;
			});
		},
		{ lazy: mountAnimationEnabled }
	);

	onDestroy(() => {
		if (optionRafId !== null) cancelAnimationFrame(optionRafId);
		if (restoreRafId !== null) cancelAnimationFrame(restoreRafId);
		if (mountAnimationRevealTimer !== null) clearTimeout(mountAnimationRevealTimer);
		if (mountAnimationSyncTimer !== null) clearTimeout(mountAnimationSyncTimer);
	});
</script>

<Card.Root class={containerClass}>
	<Card.Content class="{containerContentHeight} w-full overflow-hidden rounded-sm p-0">
		<div
			role="region"
			class="relative h-full w-full overflow-visible"
			onmouseenter={() => (mouseIn = true)}
			onmouseleave={() => (mouseIn = false)}
		>
			<div
				class="pointer-events-none absolute top-1 left-2 z-10 flex items-center gap-1 whitespace-nowrap"
			>
				{#if titleIconClass}
					<span
						class={`${titleIconClass} text-blue-600 dark:text-blue-500 inline-block h-5 w-5 shrink-0 align-middle`}
					></span>
				{/if}
				<span class="text-base leading-none font-normal text-blue-600 dark:text-blue-500"
					>{title}</span
				>
			</div>
			{#key mode.current}
				<Chart {init} options={mountOptions ?? getOptions()} bind:chart onrestore={handleRestore} />
			{/key}
		</div>
	</Card.Content>
</Card.Root>
