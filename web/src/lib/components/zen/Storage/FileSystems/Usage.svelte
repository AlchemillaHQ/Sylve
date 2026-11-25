<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { capacityTone } from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { cn } from '$lib/utils.js';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { toneMeter } from '../dashboard';
	import { quotaBytes } from './filesystems';

	let { dataset, class: className }: { dataset: Dataset; class?: string } = $props();
	let quota = $derived(quotaBytes(dataset));
	let percent = $derived(quota ? Math.min(100, (dataset.used / quota) * 100) : 0);
</script>

<div class={cn('w-full space-y-1', className)}>
	<p class="whitespace-nowrap tabular-nums">
		{formatBytesBinary(dataset.used)}
		{#if quota}
			<span class="text-muted-foreground text-xs">of {formatBytesBinary(quota)}</span>
		{/if}
	</p>
	{#if quota}
		<div
			class="bg-muted h-1 overflow-hidden rounded-full"
			role="meter"
			aria-label="{dataset.name} quota used"
			aria-valuemin="0"
			aria-valuemax="100"
			aria-valuenow={Math.round(percent)}
		>
			<div
				class="h-full rounded-full {toneMeter[capacityTone(percent)]}"
				style:width="{percent}%"
			></div>
		</div>
	{/if}
</div>
