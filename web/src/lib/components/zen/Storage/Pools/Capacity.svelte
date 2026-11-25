<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { capacityTone } from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { toneMeter } from '../dashboard';
	import { poolUsage } from './pools';
	let { pool, summary = false }: { pool: Zpool; summary?: boolean } = $props();
	let percent = $derived(poolUsage(pool));
</script>

<div class="text-xs {summary ? 'space-y-3' : 'min-w-36 space-y-1.5'}">
	{#if !summary}
		<div class="flex flex-wrap justify-between gap-x-3 gap-y-1 tabular-nums">
			<span class="flex flex-wrap gap-x-1">
				<span>{formatBytesBinary(pool.allocated)}</span>
				<span class="text-muted-foreground">of {formatBytesBinary(pool.size)}</span></span
			><span>{percent.toFixed(1)}%</span>
		</div>
	{/if}
	<div
		class="bg-muted overflow-hidden rounded-full {summary ? 'h-2' : 'h-1.5'}"
		role="meter"
		aria-label={`${pool.name} capacity used`}
		aria-valuemin="0"
		aria-valuemax="100"
		aria-valuenow={Math.min(100, Math.max(0, percent))}
	>
		<div
			class="h-full rounded-full {toneMeter[capacityTone(percent)]}"
			style:width="{Math.min(100, Math.max(0, percent))}%"
		></div>
	</div>
	{#if summary}
		<dl class="grid grid-cols-3 gap-2">
			{#each [{ label: 'Used', value: pool.allocated }, { label: 'Free', value: pool.free }, { label: 'Total', value: pool.size }] as stat (stat.label)}
				<div class="min-w-0">
					<dt class="text-muted-foreground">{stat.label}</dt>
					<dd class="mt-1 font-medium break-words tabular-nums">{formatBytesBinary(stat.value)}</dd>
				</div>
			{/each}
		</dl>
	{/if}
</div>
