<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { capacityTone, statusTone } from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import type { ZFSDashboardPoolSnapshot } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import Panel from '../Dashboard/Panel.svelte';
	import { poolErrors, toneMeter, toneText, usedPercent, verificationSummary } from './dashboard';

	let { pools }: { pools: ZFSDashboardPoolSnapshot[] } = $props();

	function details(pool: ZFSDashboardPoolSnapshot) {
		return [
			{ label: 'Data VDEVs', value: pool.topology.dataVdevs },
			{ label: 'Fragmentation', value: `${pool.fragmentation.toFixed(0)}%` },
			{ label: 'Dedup Ratio', value: `${pool.dedupRatio.toFixed(2)}×` },
			{ label: 'Read Errors', value: pool.statusAvailable ? pool.errors.read : 'Unavailable' },
			{ label: 'Write Errors', value: pool.statusAvailable ? pool.errors.write : 'Unavailable' },
			{
				label: 'Checksum Errors',
				value: pool.statusAvailable ? pool.errors.checksum : 'Unavailable'
			},
			{ label: 'Scan Errors', value: pool.statusAvailable ? pool.errors.scan : 'Unavailable' },
			...[
				{ label: 'Log Devices', value: pool.topology.logs },
				{ label: 'Cache Devices', value: pool.topology.cache },
				{ label: 'Spare Devices', value: pool.topology.spares },
				{ label: 'Special Devices', value: pool.topology.special },
				{ label: 'Dedup Devices', value: pool.topology.dedup }
			].filter((device) => device.value > 0)
		];
	}
</script>

<Panel title="Storage Pools" icon="icon-[bi--hdd-stack-fill]" class="@container">
	{#snippet aside()}{pools.length} {pools.length === 1 ? 'Pool' : 'Pools'}{/snippet}
	{#if pools.length === 0}
		<div
			class="text-muted-foreground flex min-h-48 flex-col items-center justify-center gap-2 text-center"
			role="status"
		>
			<span class="icon-[bi--hdd-stack-fill] size-8" aria-hidden="true"></span>
			<p class="text-sm font-medium">No Imported ZFS Pools</p>
			<p class="max-w-sm text-xs">
				Imported pools will appear here with their capacity, health and verification status.
			</p>
		</div>
	{:else}
		<ul class="divide-y">
			{#each pools as pool (pool.guid)}
				{@const errors = poolErrors(pool)}
				{@const used = usedPercent(pool)}
				{@const health = statusTone(pool.state, errors, pool.statusAvailable ? 0 : 1)}
				{@const verification = verificationSummary([pool])}
				<li
					class="grid min-w-0 gap-x-6 gap-y-3 py-4 first:pt-0 last:pb-0 @2xl:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_minmax(0,1.5fr)]"
				>
					<div
						class="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1 @2xl:flex-col @2xl:items-start @2xl:justify-start"
					>
						<h3 class="min-w-0 font-semibold break-all">{pool.name}</h3>
						<p class="flex items-center gap-1.5 text-xs {toneText[health]}">
							<span
								class="size-4 {health === 'success'
									? 'icon-[lucide--circle-check]'
									: health === 'danger'
										? 'icon-[lucide--circle-x]'
										: 'icon-[lucide--triangle-alert]'}"
								aria-hidden="true"
							></span>
							<span class="capitalize">{pool.state.toLowerCase()}</span>
						</p>
					</div>
					<div class="min-w-0">
						<div
							class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 text-xs tabular-nums"
						>
							<p class="flex flex-wrap gap-x-1">
								<span class="font-medium">{formatBytesBinary(pool.allocated)}</span><span
									class="text-muted-foreground"
								>
									of {formatBytesBinary(pool.size)} Used</span
								>
							</p>
							<span class="font-medium">{used.toFixed(1)}%</span>
						</div>
						<div
							class="bg-muted mt-2 h-2 overflow-hidden rounded-full"
							role="meter"
							aria-label={`${pool.name} capacity used`}
							aria-valuemin="0"
							aria-valuemax="100"
							aria-valuenow={Math.min(100, Math.max(0, used))}
						>
							<div
								class="h-full rounded-full {toneMeter[capacityTone(used)]}"
								style:width="{Math.min(100, Math.max(0, used))}%"
							></div>
						</div>
						<div class="text-muted-foreground mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
							<span>{formatBytesBinary(pool.free)} Free</span>
							<span>{pool.topology.disks} {pool.topology.disks === 1 ? 'Disk' : 'Disks'}</span>
							<span class={errors > 0 ? toneText.danger : ''}
								>{errors > 0
									? `${errors} Errors`
									: pool.statusAvailable
										? 'No Known Errors'
										: 'Device Status Unavailable'}</span
							>
						</div>
					</div>
					<div class="min-w-0">
						<div class="flex items-start gap-2 text-xs">
							<span
								class="icon-[lets-icons--check-fill] mt-0.5 size-4 shrink-0 {toneText[
									verification.tone
								]}"
								aria-hidden="true"
							></span>
							<div class="min-w-0">
								<p class="font-medium {toneText[verification.tone]}">{verification.value}</p>
								<p class="text-muted-foreground mt-1">{verification.detail}</p>
							</div>
						</div>
						{#if verification.progress !== null}
							<div
								class="bg-muted mt-2 h-1.5 overflow-hidden rounded-full"
								role="progressbar"
								aria-label={`${pool.name} verification progress`}
								aria-valuemin="0"
								aria-valuemax="100"
								aria-valuenow={Math.min(100, Math.max(0, verification.progress))}
							>
								<div
									class="bg-zen-accent h-full rounded-full"
									style:width="{Math.min(100, Math.max(0, verification.progress))}%"
								></div>
							</div>
						{/if}
					</div>
					{#if !pool.statusAvailable || health === 'danger' || health === 'warning'}
						{#if pool.status}<p class="text-muted-foreground text-xs break-words @2xl:col-span-3">
								{pool.status}
							</p>{/if}
						{#if pool.action}<p class="text-xs break-words @2xl:col-span-3">{pool.action}</p>{/if}
					{/if}
					<details class="group text-xs @2xl:col-span-3">
						<summary
							class="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex w-fit cursor-pointer list-none items-center gap-1 rounded-sm focus-visible:ring-2 focus-visible:outline-none [&::-webkit-details-marker]:hidden"
						>
							<span
								class="icon-[lucide--chevron-right] group-open:icon-[lucide--chevron-down] size-3.5 group-open:size-3.5"
								aria-hidden="true"
							></span>Pool Details
						</summary>
						<dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 rounded-md border p-3 @xl:grid-cols-3">
							{#each details(pool) as detail (detail.label)}
								<div class="min-w-0">
									<dt class="text-muted-foreground">{detail.label}</dt>
									<dd class="mt-1 font-medium break-words tabular-nums">{detail.value}</dd>
								</div>
							{/each}
						</dl>
					</details>
				</li>
			{/each}
		</ul>
	{/if}
</Panel>
