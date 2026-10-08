<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import type { LifecycleTask } from '$lib/types/task/lifecycle';
	import { isLifecycleTaskActive } from '$lib/types/task/lifecycle';

	let { task }: { task: LifecycleTask } = $props();
	const phases = [
		'Queued',
		'Preparing',
		'Provisioning / Copying',
		'Verifying',
		'Configuring',
		'Finalizing'
	];
	const phaseIndices: Record<string, number> = {
		queued: 0,
		running: 1,
		preparing: 1,
		snapshotting: 2,
		provisioning: 2,
		copying: 2,
		verifying: 3,
		publishing: 3,
		configuring: 4,
		ready: 5,
		creation_completion_pending: 5
	};
	let activeIndex = $derived(phaseIndices[task.message || 'queued'] ?? 0);
	let name = $derived.by(() => {
		try {
			return String(JSON.parse(task.payload || '{}').name || `Jail ${task.guestId}`);
		} catch {
			return `Jail ${task.guestId}`;
		}
	});
</script>

<div class="space-y-4" aria-live="polite">
	<div class="flex items-start justify-between gap-3 text-sm">
		<p class="min-w-0 break-words font-medium">{name} - CTID {task.guestId}</p>
		<p class="text-muted-foreground shrink-0 text-xs">Task #{task.id}</p>
	</div>
	{#if isLifecycleTaskActive(task)}
		{#if task.error}
			<Alert
				><AlertTitle>Finalization pending</AlertTitle><AlertDescription
					class="break-words whitespace-pre-wrap">{task.error}</AlertDescription
				></Alert
			>
		{/if}
		<div class="space-y-1.5 rounded-md border p-4">
			{#each phases as label, index (label)}
				<div class="flex items-center gap-2 text-sm">
					{#if index < activeIndex}
						<span class="icon-[mdi--check-circle] h-4 w-4 shrink-0 text-green-500"></span>
						<span class="text-muted-foreground">{label}</span>
					{:else if index === activeIndex}
						<span class="icon-[mdi--loading] text-primary h-4 w-4 shrink-0 animate-spin"></span>
						<span class="font-medium">{label}</span>
					{:else}
						<span class="icon-[mdi--circle-outline] text-muted-foreground h-4 w-4 shrink-0"></span>
						<span class="text-muted-foreground/60">{label}</span>
					{/if}
				</div>
			{/each}
		</div>
	{:else if task.status === 'success'}
		<Alert
			><AlertTitle>Jail created</AlertTitle><AlertDescription
				>The filesystem copy and configuration are complete. The jail was created stopped.</AlertDescription
			></Alert
		>
	{:else if task.status === 'failed'}
		<Alert variant="destructive"
			><AlertTitle>Creation failed</AlertTitle><AlertDescription
				class="break-words whitespace-pre-wrap"
				>{task.error || 'Creation could not be completed.'}</AlertDescription
			></Alert
		>
		<p class="text-muted-foreground text-xs">
			A retry is a new attempt. Retained resources or pending cleanup must be resolved before
			reusing the same ID.
		</p>
	{/if}
</div>
