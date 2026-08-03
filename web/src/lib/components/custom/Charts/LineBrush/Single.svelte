<script lang="ts">
	import { Chart } from '@alchemilla/svelte-echarts';
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
	import { watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { Button } from '$lib/components/ui/button/index.js';

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
		titleIconClass?: string;
		points: { date: number; value: number }[];
		percentage: boolean;
		color: 'one' | 'two' | 'three' | 'four';
		containerClass?: string;
		containerContentHeight?: string;
		emptyMessage?: string;
		loading?: boolean;
		error?: boolean;
		onRetry?: () => void;
		animateOnMount?: boolean;
	}

	let {
		title,
		titleIconClass = '',
		points,
		color,
		percentage,
		containerClass = 'p-5',
		containerContentHeight = 'h-[360px]',
		emptyMessage = '',
		loading = false,
		error = false,
		onRetry,
		animateOnMount = false
	}: Props = $props();

	const mountAnimationDuration = 1400;
	let chart: EChartsType | undefined = $state(undefined);
	let optionRafId: number | null = null;
	let restoreRafId: number | null = null;
	let mountAnimatedChart: EChartsType | undefined;
	let mountAnimationRevealTimer: ReturnType<typeof setTimeout> | null = null;
	let mountAnimationSyncTimer: ReturnType<typeof setTimeout> | null = null;
	let mountAnimationReady = !animateOnMount;

	const colors = $derived({
		title: cssVar('--text-blue-600'),
		grid: {
			dark: 'rgba(255,255,255,0.12)',
			light: 'rgba(0,0,0,0.12)'
		},
		tooltip: {
			background: cssVar('--muted'),
			border: cssVar('--border'),
			text: mode.current === 'dark' ? '#ffffff' : '#000000'
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

	// svelte-ignore state_referenced_locally
	// @wc-ignore
	let options: EChartsOption = $state.raw({
		animation: animateOnMount ? true : undefined,
		animationDuration: animateOnMount ? mountAnimationDuration : undefined,
		animationEasing: animateOnMount ? 'cubicInOut' : undefined,
		title: {
			show: false,
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
							tooltipHtml += `<div class="font-semi" style="color:${colors.tooltip.text}">${date.toLocaleString()}: ${parseFloat(value !== undefined ? Number(value).toFixed(2) : '0')}%</div>`;
						} else {
							tooltipHtml += `<div style="color:${colors.tooltip.text}">Invalid date</div>`;
						}
					} else {
						tooltipHtml += `<div style="color:${colors.tooltip.text}">Invalid data</div>`;
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
		grid: {
			left: 10,
			right: 10,
			top: 56,
			bottom: 56,
			outerBoundsMode: 'same',
			outerBoundsContain: 'axisLabel'
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
				showDataShadow: true,
				backgroundColor: 'rgba(0,0,0,0)',
				borderColor: 'rgba(0,0,0,0)',
				dataBackground: {
					lineStyle: { color: colors.moveHandle.color, opacity: 0.3 },
					areaStyle: { color: 'rgba(0,0,0,0)' }
				},
				selectedDataBackground: {
					lineStyle: { color: colors[color].main },
					areaStyle: { color: colors[color].soft }
				},
				fillerColor: 'rgba(0,0,0,0)',

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
		series: animateOnMount ? [] : buildSeries(points),
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

	function buildSeries(currentPoints: Props['points']) {
		const visibleData = currentPoints.map((point) => [point.date, point.value]);
		const previewData = [...visibleData];

		if (visibleData.length > 0) {
			const firstDate = visibleData[0][0];
			const lastDate = visibleData[visibleData.length - 1][0];
			previewData.unshift([firstDate, 100], [firstDate, 0]);
			previewData.push([lastDate, 100], [lastDate, 0]);
		}

		return [
			{
				id: 'zoom-preview',
				type: 'line' as const,
				showSymbol: false,
				silent: true,
				animation: false,
				lineStyle: { opacity: 0 },
				itemStyle: { opacity: 0 },
				tooltip: { show: false },
				data: previewData
			},
			{
				id: 'main',
				type: 'line' as const,
				showSymbol: false,
				smooth: true,
				data: visibleData
			}
		];
	}

	function setSeriesPoints(currentChart: EChartsType, currentPoints = points) {
		currentChart.setOption({
			series: buildSeries(currentPoints)
		});
	}

	function handleRestore() {
		if (restoreRafId !== null) cancelAnimationFrame(restoreRafId);

		restoreRafId = requestAnimationFrame(() => {
			restoreRafId = null;
			if (!chart || chart.isDisposed?.()) return;

			setSeriesPoints(chart);
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

			const revealedPoints = points;
			setSeriesPoints(currentChart, revealedPoints);

			mountAnimationSyncTimer = setTimeout(() => {
				mountAnimationSyncTimer = null;
				if (chart !== currentChart || currentChart.isDisposed?.()) return;

				mountAnimationReady = true;
				if (points !== revealedPoints) setSeriesPoints(currentChart);
			}, mountAnimationDuration);
		}, 100);
	}

	watch([() => chart, () => points, () => mouseIn], ([currentChart, currentPoints, isMouseIn]) => {
		if (!currentChart || !currentPoints) return;
		if (currentChart !== mountAnimatedChart) {
			if (animateOnMount) startMountAnimation(currentChart);
			else mountAnimatedChart = currentChart;
			return;
		}
		if ((animateOnMount && !mountAnimationReady) || isMouseIn) return;

		setSeriesPoints(currentChart, currentPoints);
	});

	watch(
		() => mode.current,
		() => {
			if (!chart) return;

			if (optionRafId !== null) {
				cancelAnimationFrame(optionRafId);
			}

			optionRafId = requestAnimationFrame(() => {
				if (!chart) return;

				const gridColor = mode.current === 'dark' ? colors.grid.dark : colors.grid.light;

				chart.setOption({
					title: {
						show: false
					},
					tooltip: {
						backgroundColor: colors.tooltip.background,
						borderColor: colors.tooltip.border,
						textStyle: {
							color: colors.tooltip.text
						}
					},
					xAxis: {
						axisLine: {
							lineStyle: {
								color: gridColor
							}
						}
					},
					yAxis: {
						max: percentage ? 100 : undefined,
						min: percentage ? 0 : undefined,
						axisLabel: {
							formatter: percentage ? '{value}%' : '{value}'
						},
						splitLine: {
							lineStyle: {
								color: gridColor
							}
						}
					},
					dataZoom: [
						{
							backgroundColor: 'rgba(0,0,0,0)',
							dataBackground: {
								lineStyle: { color: colors.moveHandle.color, opacity: 0.3 },
								areaStyle: { color: 'rgba(0,0,0,0)' }
							},
							selectedDataBackground: {
								lineStyle: { color: colors[color].main },
								areaStyle: { color: colors[color].soft }
							},
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
								}
							}
						}
					],
					toolbox: {
						feature: {
							saveAsImage: {
								backgroundColor: colors.tooltip.background,
								connectedBackgroundColor: colors.tooltip.background
							}
						}
					},
					color: [colors[color].main]
				});

				optionRafId = null;
			});
		},
		{ lazy: animateOnMount }
	);

	onDestroy(() => {
		if (optionRafId !== null) {
			cancelAnimationFrame(optionRafId);
		}
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
			{#if points.length > 0 && error}
				<div
					role="status"
					class="text-muted-foreground pointer-events-none absolute top-1 right-12 z-10 flex items-center gap-1 text-xs"
				>
					<span>Telemetry may be stale</span>
				</div>
			{/if}
			{#if points.length === 0 && (emptyMessage || loading)}
				<div
					role="status"
					class="text-muted-foreground flex h-full w-full flex-col items-center justify-center gap-3 px-6 pt-8 text-center text-sm"
				>
					<div class="flex items-center gap-2">
						{#if loading}
							<span class="icon-[mdi--loading] h-4 w-4 shrink-0 animate-spin"></span>
						{/if}
						<span>{emptyMessage || 'Loading telemetry…'}</span>
					</div>
					{#if error && onRetry}
						<Button size="sm" variant="outline" onclick={onRetry}>Retry</Button>
					{/if}
				</div>
			{:else}
				<Chart {init} {options} bind:chart onrestore={handleRestore} />
			{/if}
		</div>
	</Card.Content>
</Card.Root>
