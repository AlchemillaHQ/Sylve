<script lang="ts">
	import { buttonVariants } from '$lib/components/ui/button/index.js';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import type { ClusterJoinStatus } from '$lib/types/cluster/cluster';
	import { cn } from '$lib/utils.js';
	import { getClusterJoinErrorMessage, getClusterJoinPhaseMeta } from '$lib/utils/cluster';

	interface Props {
		status: ClusterJoinStatus;
	}

	let { status }: Props = $props();
	let phase = $derived(getClusterJoinPhaseMeta(status.phase));
	let hasProgress = $derived((status.targetIndex ?? 0) > 0);
	let progress = $derived(
		hasProgress
			? Math.min(
					100,
					Math.max(0, Math.round((status.appliedIndex / (status.targetIndex ?? 1)) * 100))
				)
			: 0
	);
	let friendlyError = $derived(
		status.lastError ? getClusterJoinErrorMessage(status.lastError, status.retrying) : ''
	);
	let triggerClass = $derived(
		cn(
			buttonVariants({ variant: 'outline', size: 'sm' }),
			'h-6 max-w-72 gap-1.5 px-2 text-xs',
			phase.tone === 'warning' &&
				'border-amber-500/50 text-amber-700 hover:bg-amber-500/10 dark:text-amber-400',
			phase.tone === 'error' && 'border-destructive/50 text-destructive hover:bg-destructive/10'
		)
	);
</script>

<Popover.Root>
	<Popover.Trigger class={triggerClass} aria-label="View Cluster Join Progress">
		{#if status.retrying}
			<span class="icon-[mdi--loading] h-3.5 w-3.5 shrink-0 animate-spin"></span>
		{:else}
			<span class="icon-[mdi--alert-circle-outline] h-3.5 w-3.5 shrink-0"></span>
		{/if}
		<span class="truncate" aria-live="polite">Cluster Join: {phase.label}</span>
		{#if hasProgress}
			<span class="text-muted-foreground tabular-nums">{progress}%</span>
		{/if}
		<span class="icon-[mdi--chevron-down] h-3.5 w-3.5 shrink-0"></span>
	</Popover.Trigger>

	<Popover.Content align="start" class="w-80 space-y-3">
		<div class="flex items-start gap-2">
			{#if status.retrying}
				<span class="icon-[mdi--loading] mt-0.5 h-4 w-4 shrink-0 animate-spin"></span>
			{:else}
				<span class="icon-[mdi--alert-circle-outline] mt-0.5 h-4 w-4 shrink-0"></span>
			{/if}
			<div>
				<p class="text-sm font-medium">{phase.label}</p>
				<p class="text-muted-foreground mt-0.5 text-xs">{phase.description}</p>
			</div>
		</div>

		{#if hasProgress}
			<div class="space-y-1.5">
				<div class="flex items-center justify-between text-xs">
					<span class="text-muted-foreground">Cluster State</span>
					<span class="font-medium tabular-nums">{progress}%</span>
				</div>
				<Progress value={progress} class="h-1.5" />
				<p class="text-muted-foreground text-[11px] tabular-nums">
					Synchronized {status.appliedIndex.toLocaleString()} of {status.targetIndex?.toLocaleString()}
					entries
				</p>
			</div>
		{/if}

		{#if friendlyError}
			<div
				class={phase.tone === 'error'
					? 'border-destructive/30 bg-destructive/10 rounded-md border p-2.5'
					: 'rounded-md border border-amber-500/30 bg-amber-500/10 p-2.5'}
			>
				<p class="text-xs font-medium">
					{status.retrying ? 'Retrying Automatically' : 'Action Required'}
				</p>
				<p class="text-muted-foreground mt-1 text-xs">{friendlyError}</p>
			</div>

			<details class="text-muted-foreground text-[11px]">
				<summary class="cursor-pointer select-none">Technical Details</summary>
				<p class="bg-muted mt-1.5 max-h-24 overflow-auto rounded p-2 font-mono break-all">
					{status.lastError}
				</p>
			</details>
		{/if}

		{#if status.attempts > 0}
			<p class="text-muted-foreground text-[11px]">Attempt {status.attempts.toLocaleString()}</p>
		{/if}
	</Popover.Content>
</Popover.Root>
