<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import {
		capacityTone,
		statusTone,
		type Tone
	} from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { ZFSDashboardPoolSnapshot } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import Panel from './Panel.svelte';

	let { pools, class: className = '' }: { pools: ZFSDashboardPoolSnapshot[]; class?: string } =
		$props();

	const columns = '@xl:grid-cols-[1.4fr_1fr_2fr_1fr_0.6fr_0.6fr]';

	const statusStyle: Record<Tone, string> = {
		success: 'icon-[lucide--circle-check] text-green-600 dark:text-green-500',
		warning: 'icon-[lucide--triangle-alert] text-amber-500',
		danger: 'icon-[lucide--circle-x] text-red-600 dark:text-red-500',
		neutral: 'icon-[lucide--info] text-muted-foreground'
	};

	const meterStyle: Record<Tone, string> = {
		success: 'bg-zen-accent',
		neutral: 'bg-zen-accent',
		warning: 'bg-amber-500',
		danger: 'bg-red-600 dark:bg-red-500'
	};

	const trackStyle: Record<Tone, string> = {
		success: 'bg-zen-accent/15',
		neutral: 'bg-zen-accent/15',
		warning: 'bg-amber-500/20',
		danger: 'bg-red-600/20'
	};

	let size = $derived(pools.reduce((sum, pool) => sum + pool.size, 0));
	let allocated = $derived(pools.reduce((sum, pool) => sum + pool.allocated, 0));
	let free = $derived(pools.reduce((sum, pool) => sum + pool.free, 0));
	let rows = $derived(
		pools.map((pool) => {
			const errors = pool.errors.read + pool.errors.write + pool.errors.checksum;
			const used = pool.size > 0 ? (pool.allocated / pool.size) * 100 : 0;
			const scanning = pool.scan?.state.toUpperCase() === 'SCANNING';
			return {
				pool,
				errors,
				used,
				status: statusTone(pool.state, errors, 0),
				capacity: capacityTone(used),
				resilvering: scanning && pool.scan?.function.toLowerCase().includes('resilver'),
				scrubbing: scanning && !pool.scan?.function.toLowerCase().includes('resilver'),
				progress: pool.scan?.progressPercent ?? null
			};
		})
	);
</script>

<Panel title="Storage Pools" icon="icon-[lucide--database]" class="min-h-0 {className}">
	{#snippet aside()}
		{#if pools.length > 0}{formatBytesBinary(free)} Available{/if}
	{/snippet}

	{#if rows.length === 0}
		<p class="text-muted-foreground text-sm">No storage pools yet.</p>
	{:else}
		<p class="mb-4 flex shrink-0 flex-wrap items-baseline gap-x-2 gap-y-1">
			<span class="text-2xl font-semibold tracking-tight tabular-nums"
				>{formatBytesBinary(allocated)}</span
			>
			<span class="text-muted-foreground text-xs">of {formatBytesBinary(size)} used</span>
		</p>
		<div class="@container flex min-h-0 flex-1 flex-col">
			<div
				class="text-muted-foreground hidden shrink-0 gap-x-4 border-b pb-2 text-xs @xl:grid {columns}"
				aria-hidden="true"
			>
				<span>Pool</span>
				<span>Status</span>
				<span>Capacity</span>
				<span class="text-right">Available</span>
				<span class="text-right">Disks</span>
				<span class="text-right">Errors</span>
			</div>

			<ScrollArea
				class="min-h-0 max-h-64 flex-1 @4xl/overview:max-h-none"
				fade
				fadeClasses="from-card"
				scrollbarYClasses="hidden"
				viewportProps={{
					class: 'max-h-64 overscroll-contain @4xl/overview:max-h-none',
					role: 'region',
					'aria-label': 'Storage Pools',
					tabindex: 0
				}}
			>
				<ul class="divide-y">
					{#each rows as row (row.pool.guid)}
						<li
							class="grid grid-cols-6 gap-x-4 gap-y-2.5 py-2.5 text-sm last:pb-0 @xl:items-center {columns}"
						>
							<div class="col-span-4 min-w-0 @xl:col-span-1">
								<p class="truncate font-medium">{row.pool.name}</p>
								{#if row.scrubbing || row.resilvering}
									<p class="text-muted-foreground text-xs">
										{#if row.resilvering}Resilvering{:else}Scrubbing{/if}
										{#if row.progress !== null}{row.progress.toFixed(0)}%{/if}
									</p>
								{/if}
							</div>

							<div
								class="col-span-2 flex items-center gap-1.5 justify-self-end @xl:col-span-1 @xl:justify-self-auto"
							>
								<span class="{statusStyle[row.status]} size-4 shrink-0"></span>
								<span class="capitalize">{row.pool.state.toLowerCase()}</span>
							</div>

							<div class="col-span-6 flex items-center gap-3 @xl:col-span-1">
								<span class="w-9 shrink-0 tabular-nums">{row.used.toFixed(0)}%</span>
								<div
									class="h-1.5 flex-1 overflow-hidden rounded-full {trackStyle[row.capacity]}"
									role="meter"
									aria-valuemin="0"
									aria-valuemax="100"
									aria-valuenow={Math.round(row.used)}
									aria-label={row.pool.name}
								>
									<div
										class="h-full rounded-full {meterStyle[row.capacity]}"
										style:width="{Math.min(100, row.used)}%"
									></div>
								</div>
							</div>

							<div class="col-span-2 @xl:col-span-1 @xl:text-right">
								<p class="text-muted-foreground text-xs @xl:hidden">Available</p>
								<p class="tabular-nums">{formatBytesBinary(row.pool.free)}</p>
							</div>

							<div class="col-span-2 @xl:col-span-1 @xl:text-right">
								<p class="text-muted-foreground text-xs @xl:hidden">Disks</p>
								<p class="tabular-nums">{row.pool.topology.disks}</p>
							</div>

							<div class="col-span-2 @xl:col-span-1 @xl:text-right">
								<p class="text-muted-foreground text-xs @xl:hidden">Errors</p>
								<p class="flex items-center gap-1.5 tabular-nums @xl:justify-end">
									{#if row.errors > 0}
										<span class="icon-[lucide--circle-x] size-4 text-red-600 dark:text-red-500"
										></span>
									{/if}
									{row.errors}
								</p>
							</div>
						</li>
					{/each}
				</ul>
			</ScrollArea>
		</div>
	{/if}
</Panel>
