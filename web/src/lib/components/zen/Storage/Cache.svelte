<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import type { ZFSDashboardARCPoint } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import Panel from '../Dashboard/Panel.svelte';

	let { arc }: { arc: ZFSDashboardARCPoint | null } = $props();
	function ratio(value: number | null): string {
		return value === null ? 'Warming Up' : `${value.toFixed(1)}%`;
	}
	function revealDetails(event: Event & { currentTarget: HTMLDetailsElement }) {
		const element = event.currentTarget;
		if (!element.open) return;
		requestAnimationFrame(() => {
			requestAnimationFrame(() => {
				if (!element.isConnected || !element.open) return;
				const viewport = element.closest('main');
				if (!viewport) return;
				viewport.scrollTo({
					top: viewport.scrollHeight,
					behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches
						? 'instant'
						: 'smooth'
				});
			});
		});
	}
	let details = $derived(
		arc
			? [
					{ label: 'Demand Hit Rate', value: ratio(arc.demandHitRatio) },
					{ label: 'Prefetch Hit Rate', value: ratio(arc.prefetchHitRatio) },
					{ label: 'Data', value: formatBytesBinary(arc.dataSize) },
					{ label: 'Metadata', value: formatBytesBinary(arc.metadataSize) },
					{ label: 'Other', value: formatBytesBinary(arc.otherSize) },
					{ label: 'Headers', value: formatBytesBinary(arc.headerSize) },
					{ label: 'Minimum Size', value: formatBytesBinary(arc.minSize) },
					{
						label: 'Compression',
						value:
							arc.compressedSize > 0
								? `${(arc.uncompressedSize / arc.compressedSize).toFixed(2)}×`
								: 'Unavailable'
					},
					{ label: 'Evictions', value: `${arc.evictionsPerSecond.toFixed(1)}/s` },
					{ label: 'Memory Throttle Events', value: arc.memoryThrottleEvents },
					{ label: 'Reclaim Shortfalls', value: arc.evictNotEnoughEvents },
					...(arc.l2DeviceCount > 0
						? [
								{ label: 'L2ARC Devices', value: arc.l2DeviceCount },
								{ label: 'L2ARC Used', value: formatBytesBinary(arc.l2Allocated) },
								{ label: 'L2ARC Hit Rate', value: ratio(arc.l2HitRatio) }
							]
						: [])
				]
			: []
	);
</script>

<Panel title="ZFS Cache" icon="icon-[mdi--memory]" class="@container">
	{#snippet aside()}Node Wide{/snippet}
	{#if arc}
		<div class="grid gap-4 @2xl:grid-cols-3 @2xl:gap-6">
			<div class="min-w-0">
				<p class="text-[28px] leading-8 font-semibold tracking-tight tabular-nums">
					{formatBytesBinary(arc.size)}
				</p>
				<p class="text-muted-foreground mt-1 text-xs">RAM Used by ZFS (ARC)</p>
				{#if arc.maxSize > 0}
					<div
						class="bg-muted mt-4 h-2 overflow-hidden rounded-full"
						role="meter"
						aria-label="ZFS cache size"
						aria-valuemin="0"
						aria-valuemax={arc.maxSize}
						aria-valuenow={Math.min(arc.maxSize, Math.max(0, arc.size))}
						aria-valuetext={`${formatBytesBinary(arc.size)} of ${formatBytesBinary(arc.maxSize)}`}
					>
						<div
							class="h-full rounded-full bg-teal-600 dark:bg-teal-400"
							style:width="{Math.min(100, Math.max(0, (arc.size / arc.maxSize) * 100))}%"
						></div>
					</div>
				{/if}
			</div>
			<div class="min-w-0 border-t pt-4 @2xl:border-t-0 @2xl:border-l @2xl:pt-0 @2xl:pl-6">
				<p class="text-[28px] leading-8 font-semibold tracking-tight tabular-nums">
					{ratio(arc.hitRatio)}
				</p>
				<p class="text-muted-foreground mt-1 text-xs">Cache Hit Rate</p>
				<p class="text-muted-foreground mt-3 text-xs leading-relaxed">
					Reads served from RAM instead of accessing disks.
				</p>
			</div>
			<div class="min-w-0 border-t pt-4 @2xl:border-t-0 @2xl:border-l @2xl:pt-0 @2xl:pl-6">
				<dl class="space-y-2 text-[13px]">
					<div class="flex flex-wrap justify-between gap-x-3 gap-y-1">
						<dt class="text-muted-foreground">Minimum Size</dt>
						<dd class="font-medium tabular-nums">{formatBytesBinary(arc.minSize)}</dd>
					</div>
					<div class="flex flex-wrap justify-between gap-x-3 gap-y-1">
						<dt class="text-muted-foreground">Adaptive Target</dt>
						<dd class="font-medium tabular-nums">{formatBytesBinary(arc.targetSize)}</dd>
					</div>
					<div class="flex flex-wrap justify-between gap-x-3 gap-y-1">
						<dt class="text-muted-foreground">Maximum Size</dt>
						<dd class="font-medium tabular-nums">{formatBytesBinary(arc.maxSize)}</dd>
					</div>
				</dl>
			</div>
		</div>
		<details class="group mt-4 border-t pt-3 text-xs" ontoggle={revealDetails}>
			<summary
				class="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex w-fit cursor-pointer list-none items-center gap-1 rounded-sm focus-visible:ring-2 focus-visible:outline-none [&::-webkit-details-marker]:hidden"
				><span
					class="icon-[lucide--chevron-right] group-open:icon-[lucide--chevron-down] size-3.5 group-open:size-3.5"
					aria-hidden="true"
				></span>Cache Details</summary
			>
			<p class="text-muted-foreground mt-3 leading-relaxed">
				This cache is shared by all pools on the node, regardless of the pool filter.
			</p>
			<dl class="mt-3 grid gap-x-6 gap-y-3 @xl:grid-cols-2 @4xl:grid-cols-3">
				{#each details as detail (detail.label)}
					<div class="flex flex-wrap justify-between gap-x-3 gap-y-1">
						<dt class="text-muted-foreground">{detail.label}</dt>
						<dd class="font-medium tabular-nums">{detail.value}</dd>
					</div>
				{/each}
			</dl>
		</details>
	{:else}
		<p class="text-muted-foreground text-sm">No cache sample has been reported yet.</p>
	{/if}
</Panel>
