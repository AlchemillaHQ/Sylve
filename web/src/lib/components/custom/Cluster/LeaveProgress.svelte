<script lang="ts">
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import type { ClusterLeaveStatus } from '$lib/types/cluster/cluster';
	import { cn } from '$lib/utils.js';
	import { getClusterLeaveErrorMessage, getClusterLeavePhaseMeta } from '$lib/utils/cluster';

	interface Props {
		status: ClusterLeaveStatus;
		onRetry: () => void;
		onForceReset: () => void;
	}

	let { status, onRetry, onForceReset }: Props = $props();
	let phase = $derived(getClusterLeavePhaseMeta(status.phase));
	let friendlyError = $derived(
		status.lastError ? getClusterLeaveErrorMessage(status.lastError) : ''
	);
	let triggerClass = $derived(
		cn(
			buttonVariants({ variant: 'outline', size: 'sm' }),
			'h-6 max-w-72 gap-1.5 px-2 text-xs',
			friendlyError &&
				'border-amber-500/50 text-amber-700 hover:bg-amber-500/10 dark:text-amber-400'
		)
	);
</script>

<Popover.Root>
	<Popover.Trigger class={triggerClass} aria-label="View Cluster Leave Progress">
		<span class="icon-[mdi--shield-lock-outline] h-3.5 w-3.5 shrink-0"></span>
		<span class="truncate" aria-live="polite">Cluster Leave: {phase.label}</span>
		<span class="icon-[mdi--chevron-down] h-3.5 w-3.5 shrink-0"></span>
	</Popover.Trigger>

	<Popover.Content align="start" class="w-80 space-y-3">
		<div class="flex items-start gap-2">
			<span class="icon-[mdi--shield-lock-outline] mt-0.5 h-4 w-4 shrink-0"></span>
			<div>
				<p class="text-sm font-medium">{phase.label}</p>
				<p class="mt-0.5 text-xs text-muted-foreground">{phase.description}</p>
			</div>
		</div>

		{#if friendlyError}
			<div class="rounded-md border border-amber-500/30 bg-amber-500/10 p-2.5">
				<p class="text-xs font-medium">Attention Required</p>
				<p class="mt-1 text-xs text-muted-foreground">{friendlyError}</p>
			</div>

			<details class="text-[11px] text-muted-foreground">
				<summary class="cursor-pointer select-none">Technical Details</summary>
				<p class="mt-1.5 max-h-24 overflow-auto rounded bg-muted p-2 font-mono break-all">
					{status.lastError}
				</p>
			</details>
		{/if}

		{#if friendlyError}
			<div class="flex justify-end gap-2">
				<Button size="sm" variant="secondary" onclick={onRetry}>Retry Leave</Button>
				<Button size="sm" variant="destructive" onclick={onForceReset}>Force Local Reset…</Button>
			</div>
		{/if}
	</Popover.Content>
</Popover.Root>
