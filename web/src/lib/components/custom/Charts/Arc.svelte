<script lang="ts">
	import { Chart } from 'svelte-echarts';
	import { init, use } from 'echarts/core';
	import { GaugeChart } from 'echarts/charts';
	import { GraphicComponent } from 'echarts/components';
	import { CanvasRenderer } from 'echarts/renderers';
	import { mode } from 'mode-watcher';
	import type { EChartsOption } from 'echarts';

	use([GaugeChart, GraphicComponent, CanvasRenderer]);

	interface Props {
		title: string;
		subtitle?: string;
		value: number;
	}

	let { title, subtitle, value }: Props = $props();

	const getColor = (v: number) => {
		if (v <= 33) return '#22c55e';
		if (v <= 66) return '#eab308';
		return '#ef4444';
	};

	let options: EChartsOption = $derived({
		graphic: {
			elements: [
				{
					type: 'text',
					left: 'center',
					top: 40,
					style: {
						text: title,
						fontSize: 16,
						fontWeight: 'bold',
						fill: mode.current === 'dark' ? '#ffffff' : '#000000'
					}
				},
				...(subtitle
					? [
							{
								type: 'text',
								left: 'center',
								top: 65,
								style: {
									text: subtitle,
									fontSize: 12,
									fontWeight: 500,
									fill: mode.current === 'dark' ? '#a1a1aa' : '#71717a'
								}
							}
						]
					: []),
				{
					type: 'text',
					left: 'center',
					top: 85,
					style: {
						text: `${Math.round(value)}%`,
						fontSize: 30,
						fontWeight: 'bold',
						fill: mode.current === 'dark' ? '#ffffff' : '#000000'
					}
				}
			]
		},
		series: [
			{
				type: 'gauge',
				startAngle: 210,
				endAngle: -30,
				min: 0,
				max: 100,
				center: ['50%', '50%'],
				radius: '100%',
				pointer: { show: false },
				progress: {
					show: true,
					overlap: false,
					roundCap: true,
					clip: false,
					itemStyle: {
						color: getColor(value),
						opacity: 0.7
					}
				},
				axisLine: {
					lineStyle: {
						width: 15,
						color: [
							[1, mode.current === 'dark' ? 'rgba(255, 255, 255, 0.1)' : 'rgba(0, 0, 0, 0.1)']
						]
					}
				},
				splitLine: { show: false },
				axisTick: { show: false },
				axisLabel: { show: false },
				detail: { show: false },
				title: { show: false },
				data: [{ value: value }]
			}
		]
	});
</script>

<div class="h-37.5 w-50 overflow-hidden rounded-sm">
	<Chart {init} {options} class="h-full w-full" />
</div>
