<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import {
		isPoolScrubActive,
		type PoolScrubRun,
		type PoolScrubSchedule,
		type ZpoolStatusPool
	} from '$lib/types/zfs/pool';
	import {
		describeScan,
		scanBytes,
		scanDate,
		scanDuration,
		scanIconColor,
		scanTime,
		scrubLabel
	} from './scan';

	let {
		status,
		activeRun,
		activeSchedule,
		queued,
		requested = false,
		loading = false,
		stale = false
	}: {
		status: ZpoolStatusPool | null;
		activeRun: PoolScrubRun | null;
		activeSchedule: PoolScrubSchedule | null;
		queued: boolean;
		requested?: boolean;
		loading?: boolean;
		stale?: boolean;
	} = $props();
	let scan = $derived(describeScan(status?.scan_stats));
	let tracked = $derived(
		activeRun?.status ||
			(activeSchedule && isPoolScrubActive(activeSchedule.lastStatus)
				? activeSchedule.lastStatus
				: activeSchedule?.active
					? 'starting'
					: '')
	);
	let pending = $derived(!scan.active && (Boolean(tracked) || requested));
	let trackedProgress = $derived(
		activeRun?.progressKnown
			? Math.min(100, Math.max(0, activeRun.progressPct))
			: activeSchedule?.progressKnown && isPoolScrubActive(activeSchedule.lastStatus)
				? Math.min(100, Math.max(0, activeSchedule.progressPct))
				: null
	);
	let progress = $derived(
		scan.active
			? (scan.progress ?? (scan.kind === 'Scrub' ? trackedProgress : null))
			: trackedProgress
	);
	let heading = $derived.by(() => {
		if (scan.active) return `${scan.kind} ${scan.paused ? 'paused' : 'in progress'}`;
		if (pending) return tracked ? `Scrub ${tracked}` : 'Scrub start requested';
		if (queued) return 'Scheduled scrub queued';
		if (scan.known) return `Last ${scan.kind.toLowerCase()}`;
		return status ? 'No scan has been reported' : 'Scan activity unavailable';
	});
	let phase = $derived(
		scan.active
			? scan.phase
			: pending
				? scrubLabel(tracked || 'starting')
				: queued
					? 'Queued'
					: scan.phase
	);
	let iconColor = $derived(
		scan.active || (!pending && !queued)
			? scanIconColor[scan.tone]
			: tracked === 'paused'
				? scanIconColor.warning
				: scanIconColor.neutral
	);
	let icon = $derived.by(() => {
		if (scan.paused || (pending && tracked === 'paused')) return 'icon-[lucide--pause]';
		if (scan.active || pending) return 'icon-[lucide--refresh-cw]';
		if (queued) return 'icon-[lucide--clock]';
		if (scan.tone === 'danger') return 'icon-[lucide--circle-alert]';
		if (scan.finished) return 'icon-[lucide--circle-check]';
		return 'icon-[lucide--scan-line]';
	});
	let metrics = $derived([
		{ label: 'Started', value: scanDate(status?.scan_stats?.start_time) },
		{ label: 'Finished', value: scan.active ? '—' : scanDate(status?.scan_stats?.end_time) },
		{
			label: scan.active ? 'Elapsed' : 'Duration',
			value: scanDuration(
				scan.start,
				scan.active
					? scan.paused
						? scanTime(status?.scan_stats?.scrub_pause)
						: Date.now()
					: scan.end
			)
		},
		{
			label: 'Scanned',
			value: `${scanBytes(status?.scan_stats?.examined)} / ${scanBytes(status?.scan_stats?.to_examine)}`
		},
		{ label: 'Issued', value: scanBytes(status?.scan_stats?.issued) },
		{
			label: scan.kind === 'Resilver' ? 'Resilvered' : 'Repaired',
			value: scanBytes(status?.scan_stats?.processed)
		},
		{ label: 'Errors', value: scan.errors === null ? '—' : scan.errors.toLocaleString() },
		...(scan.active
			? [
					{ label: 'Average Rate', value: scan.rate === null ? '—' : `${scanBytes(scan.rate)}/s` },
					{
						label: 'Est. Remaining',
						value: scan.remaining === null ? '—' : `~${scanDuration(0, scan.remaining * 1000)}`
					}
				]
			: [])
	]);
</script>

<div class="min-w-0 space-y-4">
	{#if !status && loading && !pending && !queued}
		<p class="text-muted-foreground text-sm" role="status">Loading scan activity…</p>
	{:else}
		<div class="flex min-w-0 items-start gap-3">
			<span
				class="bg-muted flex size-9 shrink-0 items-center justify-center rounded-lg {iconColor}"
				aria-hidden="true"
			>
				<span class="size-5 {icon}"></span>
			</span>
			<div class="min-w-0 flex-1">
				<div class="flex flex-wrap items-center gap-x-3 gap-y-1">
					<h3 class="text-sm font-semibold">{heading}</h3>
					<span class="text-muted-foreground rounded-md border px-2 py-0.5 text-xs">{phase}</span>
				</div>
				<p class="text-muted-foreground mt-1 text-[13px] leading-5">
					{#if scan.active}
						{activeRun && scan.kind === 'Scrub'
							? `${scrubLabel(activeRun.source)} run`
							: 'External / untracked scan'} · The pool remains available; scan activity may affect I/O
						performance.
					{:else if pending}
						{tracked === 'running' || tracked === 'paused'
							? 'Sylve is tracking this scrub; live ZFS scan details have not arrived.'
							: 'Waiting for ZFS to report the scan.'} Closing this page does not cancel the request.
					{:else if queued}
						A schedule is waiting to run. Disable or delete it to cancel the queued occurrence
						before starting manually.
					{:else if scan.known}
						{stale ? 'Last observed ZFS scan.' : 'Last scan reported by ZFS.'} Times are shown in your
						browser’s local time.
					{:else}
						{status
							? 'A previous scrub or resilver could not be confirmed from ZFS.'
							: 'Detailed ZFS status is unavailable.'} A scrub verifies stored data and repairs damage
						where redundancy permits.
					{/if}
				</p>
			</div>
		</div>
		{#if scan.active || pending}
			<div class="space-y-2">
				<div class="text-muted-foreground flex justify-between gap-3 text-xs">
					<span>{pending ? 'Scrub' : scan.kind} progress</span>
					<span class="tabular-nums"
						>{progress === null ? 'Not Reported' : `${progress.toFixed(1)}%`}</span
					>
				</div>
				<div
					class="bg-muted h-2 overflow-hidden rounded-full"
					role="progressbar"
					aria-label={`${pending ? 'Scrub' : scan.kind} progress`}
					aria-valuemin="0"
					aria-valuemax="100"
					aria-valuenow={progress ?? undefined}
					aria-valuetext={progress === null ? 'Progress not reported' : undefined}
				>
					{#if progress !== null}
						<div
							class="bg-zen-accent h-full rounded-full transition-[width] motion-reduce:transition-none"
							style:width="{progress}%"
						></div>
					{:else}
						<div class="bg-zen-accent/50 h-full w-1/3 rounded-full motion-safe:animate-pulse"></div>
					{/if}
				</div>
			</div>
		{/if}
		{#if scan.known && !pending}
			<div class="border-t pt-4">
				{#if queued && !scan.active}
					<h4 class="mb-3 text-xs font-semibold">Last {scan.kind.toLowerCase()}</h4>
				{/if}
				<dl class="grid grid-cols-2 gap-x-6 gap-y-4 text-[13px] @xl:grid-cols-4">
					{#each metrics as metric (metric.label)}
						<div class="min-w-0">
							<dt class="text-muted-foreground text-xs">{metric.label}</dt>
							<dd
								class="mt-1 break-words tabular-nums"
								class:text-destructive={metric.label === 'Errors' &&
									scan.errors !== null &&
									scan.errors > 0}
							>
								{metric.value}
							</dd>
						</div>
					{/each}
				</dl>
				{#if scan.active && !scan.paused}
					<p class="text-muted-foreground mt-4 text-xs leading-5">
						Rate and remaining time are wall-clock estimates, not instantaneous throughput.
					</p>
				{/if}
			</div>
		{/if}
	{/if}
</div>
