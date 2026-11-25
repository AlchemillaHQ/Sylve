<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { axisTime, niceCeil, readoutTime, runs } from './format';

	interface Point {
		date: number;
		value: number;
	}

	interface Series {
		name: string;
		color: string;
		points: Point[];
	}

	interface Props {
		series: Series[];
		format: (value: number) => string;
		start: number;
		end: number;
		floor?: number;
		limit?: number;
	}

	let { series, format, start, end, floor = 0, limit = Infinity }: Props = $props();

	const height = 104;
	const inset = 16;
	const width = 1000;

	let hovered = $state<number | null>(null);
	let hasSamples = $derived(series.some((item) => item.points.length > 0));

	let top = $derived(
		Math.min(
			limit,
			Math.max(floor, niceCeil(Math.max(0, ...series.flatMap((s) => s.points.map((p) => p.value)))))
		)
	);

	function x(date: number): number {
		return ((date - start) / (end - start)) * width;
	}

	function y(value: number): number {
		return inset + (1 - Math.min(value, top) / top) * (height - inset);
	}

	function line(points: Point[]): string {
		const path = points
			.map(
				({ date, value }, index) =>
					`${index ? 'L' : 'M'}${x(date).toFixed(1)},${y(value).toFixed(1)}`
			)
			.join('');
		return points.length === 1 ? `${path}h0` : path;
	}

	function area(points: Point[]): string {
		return `${line(points)}L${x(points.at(-1)!.date).toFixed(1)},${height}L${x(points[0].date).toFixed(1)},${height}Z`;
	}

	function nearest(points: Point[], date: number): Point | undefined {
		let best: Point | undefined;
		for (const point of points) {
			if (!best || Math.abs(point.date - date) < Math.abs(best.date - date)) best = point;
		}
		return best;
	}

	let shapes = $derived(
		series.map(({ name, color, points }) => ({ name, color, segments: runs(points) }))
	);

	let readout = $derived.by(() => {
		if (hovered === null) return null;
		const anchor = nearest(series[0]?.points ?? [], hovered);
		if (!anchor) return null;
		return {
			date: anchor.date,
			left: x(anchor.date),
			rows: series.map(({ name, color, points }) => ({
				name,
				color,
				value: nearest(points, anchor.date)?.value ?? 0
			}))
		};
	});

	let summary = $derived(
		series.map(({ name, points }) => `${name} ${format(points.at(-1)?.value ?? 0)}`).join(', ')
	);

	function track(event: PointerEvent & { currentTarget: HTMLDivElement }) {
		const bounds = event.currentTarget.getBoundingClientRect();
		const ratio = Math.min(1, Math.max(0, (event.clientX - bounds.left) / bounds.width));
		hovered = start + ratio * (end - start);
	}
</script>

<div>
	<div
		class="relative touch-pan-y"
		style:height="{height}px"
		onpointermove={track}
		onpointerleave={() => (hovered = null)}
		role="img"
		aria-label={summary}
	>
		<span class="text-muted-foreground absolute top-0 left-0 text-[10px] leading-none tabular-nums">
			{format(top)}
		</span>

		<svg
			viewBox="0 0 {width} {height}"
			preserveAspectRatio="none"
			{height}
			class="plot block w-full overflow-visible"
			aria-hidden="true"
		>
			<line
				x1="0"
				x2={width}
				y1={inset}
				y2={inset}
				class="stroke-border"
				stroke-width="1"
				vector-effect="non-scaling-stroke"
			/>
			<line
				x1="0"
				x2={width}
				y1={height}
				y2={height}
				class="stroke-border"
				stroke-width="1"
				vector-effect="non-scaling-stroke"
			/>

			{#each shapes as shape (shape.name)}
				{#each shape.segments as segment (segment[0].date)}
					{#if shapes.length === 1 && segment.length > 1}
						<path d={area(segment)} fill={shape.color} fill-opacity="0.1" />
					{/if}
					<path
						d={line(segment)}
						fill="none"
						stroke={shape.color}
						stroke-width="2"
						stroke-linejoin="round"
						stroke-linecap="round"
						vector-effect="non-scaling-stroke"
					/>
				{/each}
			{/each}

			{#if readout}
				<line
					x1={readout.left}
					x2={readout.left}
					y1={inset}
					y2={height}
					class="stroke-muted-foreground"
					stroke-width="1"
					vector-effect="non-scaling-stroke"
				/>
			{/if}
		</svg>

		{#if readout}
			{#each readout.rows as row (row.name)}
				<span
					class="border-card pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2"
					style:left="{(readout.left / width) * 100}%"
					style:top="{y(row.value)}px"
					style:background={row.color}
					aria-hidden="true"
				></span>
			{/each}
			<div
				class="bg-popover text-popover-foreground pointer-events-none absolute top-0 z-10 rounded-md border px-2.5 py-1.5 text-xs whitespace-nowrap shadow-sm"
				style:left={readout.left < width / 2
					? `calc(${(readout.left / width) * 100}% + 12px)`
					: undefined}
				style:right={readout.left < width / 2
					? undefined
					: `calc(${(1 - readout.left / width) * 100}% + 12px)`}
			>
				<p class="text-muted-foreground">{readoutTime(readout.date)}</p>
				{#each readout.rows as row (row.name)}
					<p class="mt-1 flex items-center gap-2">
						<span class="h-0.5 w-3 rounded-full" style:background={row.color}></span>
						<span class="font-semibold">{format(row.value)}</span>
						<span class="text-muted-foreground">{row.name}</span>
					</p>
				{/each}
			</div>
		{/if}
		{#if !hasSamples}
			<p class="text-muted-foreground absolute inset-0 flex items-center justify-center text-xs">
				No samples in this period
			</p>
		{/if}
	</div>

	<div class="text-muted-foreground mt-1.5 flex justify-between text-[10px] tabular-nums">
		<span>{axisTime(start, end - start)}</span>
		<span>{axisTime(end, end - start)}</span>
	</div>
</div>

<style>
	@media (prefers-reduced-motion: no-preference) {
		.plot {
			animation: reveal 180ms ease-out;
		}
	}

	@keyframes reveal {
		from {
			opacity: 0;
		}
		to {
			opacity: 1;
		}
	}
</style>
