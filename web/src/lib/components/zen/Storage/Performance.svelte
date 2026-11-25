<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { compactNumber, summarizeIO } from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import type {
		PoolStatPoint,
		ZFSDashboardHistory,
		ZFSDashboardPoolSnapshot
	} from '$lib/types/zfs/pool';
	import { formatBytesPerSecondBinary } from '$lib/utils/bytes';
	import Chart from '../Dashboard/Chart.svelte';
	import Panel from '../Dashboard/Panel.svelte';
	import { latency, scopedHistory } from './dashboard';

	let {
		pools,
		history,
		seconds,
		loading,
		failed
	}: {
		pools: ZFSDashboardPoolSnapshot[];
		history: ZFSDashboardHistory | null;
		seconds: number;
		loading: boolean;
		failed: boolean;
	} = $props();

	let io = $derived(summarizeIO(pools));
	let end = $derived(history?.generatedAt || Date.now());
	let start = $derived(end - seconds * 1000);
	let points = $derived(
		history
			? scopedHistory(history, pools).filter((point) => point.time >= start && point.time <= end)
			: []
	);
	let metrics = $derived([
		{
			title: 'Throughput',
			icon: 'icon-[mdi--swap-vertical-bold]',
			read: io.valid ? io.readBytesPerSecond : null,
			write: io.valid ? io.writeBytesPerSecond : null,
			format: formatBytesPerSecondBinary,
			readValue: (point: PoolStatPoint) => point.readBytesPerSecond,
			writeValue: (point: PoolStatPoint) => point.writeBytesPerSecond
		},
		{
			title: 'Operations',
			icon: 'icon-[mdi--chart-timeline-variant]',
			read: io.valid ? io.readIOPS : null,
			write: io.valid ? io.writeIOPS : null,
			format: (value: number) => `${compactNumber(value)} IOPS`,
			readValue: (point: PoolStatPoint) => point.readIOPS,
			writeValue: (point: PoolStatPoint) => point.writeIOPS
		},
		{
			title: 'Latency',
			icon: 'icon-[mdi--timer-outline]',
			read: latency(pools, 'read'),
			write: latency(pools, 'write'),
			format: (value: number) => `${value.toFixed(value >= 10 ? 1 : 2)} ms`,
			readValue: (point: PoolStatPoint) => point.readLatencyNanos / 1_000_000,
			writeValue: (point: PoolStatPoint) => point.writeLatencyNanos / 1_000_000
		}
	]);
</script>

<section aria-label="Storage performance" aria-busy={loading}>
	{#if failed}
		<p class="mb-3 text-xs text-amber-600 dark:text-amber-400" role="status">
			Performance history could not be refreshed.{history
				? ' Showing the last successful history for this range.'
				: ' Use Refresh to try again.'}
		</p>
	{/if}
	<div class="grid gap-4 @2xl:grid-cols-3">
		{#each metrics as metric (metric.title)}
			<Panel title={metric.title} icon={metric.icon}>
				<dl class="space-y-1">
					{#each [{ label: 'Read', value: metric.read, color: 'var(--zen-accent)' }, { label: 'Write', value: metric.write, color: 'var(--zen-contrast)' }] as direction (direction.label)}
						<div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
							<dt class="text-muted-foreground flex items-center gap-1.5 text-xs">
								<span class="h-0.5 w-3 shrink-0 rounded-full" style:background={direction.color}
								></span>{direction.label}
							</dt>
							<dd class="text-lg font-semibold tracking-tight whitespace-nowrap tabular-nums">
								{direction.value === null ? '—' : metric.format(direction.value)}
							</dd>
						</div>
					{/each}
				</dl>
				{#if metric.title === 'Latency' && metric.read === null && metric.write === null}
					<p class="text-muted-foreground mt-1 text-xs">
						No measured I/O latency in the current sample.
					</p>
				{/if}
				<div class="mt-auto pt-4">
					{#if !history}
						<div
							class="text-muted-foreground flex h-[125px] items-center justify-center text-center text-xs"
							role="status"
						>
							{#if failed}
								History Unavailable
							{:else}
								<span
									class="icon-[lucide--loader-circle] text-zen-accent size-6 animate-spin motion-reduce:animate-none"
									aria-hidden="true"
								></span>
								<span class="sr-only">Loading history…</span>
							{/if}
						</div>
					{:else}
						<Chart
							series={[
								{
									name: 'Read',
									color: 'var(--zen-accent)',
									points: points.map((point) => ({
										date: point.time,
										value: metric.readValue(point)
									}))
								},
								{
									name: 'Write',
									color: 'var(--zen-contrast)',
									points: points.map((point) => ({
										date: point.time,
										value: metric.writeValue(point)
									}))
								}
							]}
							format={metric.format}
							{start}
							{end}
						/>
					{/if}
				</div>
			</Panel>
		{/each}
	</div>
</section>
