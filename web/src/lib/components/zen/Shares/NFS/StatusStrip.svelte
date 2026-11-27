<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import type { NFSStatus } from '$lib/types/nfs';
	import { plural } from '../shared';

	let {
		status,
		failed,
		loading = false,
		subject,
		onApply
	}: {
		status: NFSStatus | undefined;
		failed: boolean;
		loading?: boolean;
		subject: string;
		onApply?: () => void;
	} = $props();
</script>

{#snippet divider()}
	<span class="bg-border hidden h-4 w-px shrink-0 sm:block" aria-hidden="true"></span>
{/snippet}

{#snippet warning(text: string)}
	<span class="flex min-w-0 items-center gap-2">
		<span
			class="icon-[lucide--triangle-alert] size-4 shrink-0 text-amber-600 dark:text-amber-400"
			aria-hidden="true"
		></span>
		{text}
	</span>
{/snippet}

{#if status || failed || loading}
	<div
		class="bg-card mb-4 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border px-4 py-2.5 text-sm"
		role="status"
	>
		{#if !status && !failed}
			<span class="text-muted-foreground flex items-center gap-2">
				<span class="icon-[lucide--loader-circle] size-4 shrink-0 animate-spin" aria-hidden="true"
				></span>
				Checking NFS…
			</span>
		{:else if !status}
			<span class="text-muted-foreground flex items-center gap-2">
				<span class="icon-[lucide--circle-help] size-4 shrink-0" aria-hidden="true"></span>
				NFS’s status couldn’t be loaded
			</span>
		{:else if !status.runtimeAvailable}
			{@render warning(`NFS needs Sylve to run as root, so ${subject}.`)}
		{:else if !status.enabled}
			{@render warning(`The NFS service is turned off, so ${subject}. Turn it on in Services.`)}
		{:else if status.applyStatus === 'applying' || status.applyStatus === 'starting'}
			<span class="flex items-center gap-2 font-medium">
				<span class="icon-[lucide--loader-circle] size-4 shrink-0 animate-spin" aria-hidden="true"
				></span>
				Applying changes…
			</span>
		{:else if status.applyStatus === 'pending'}
			<span class="flex min-w-0 flex-1 items-center gap-2">
				<span
					class="icon-[lucide--triangle-alert] size-4 shrink-0 text-amber-600 dark:text-amber-400"
					aria-hidden="true"
				></span>
				<span class="shrink-0 font-medium">Changes aren’t applied</span>
				{#if status.lastError}
					{@const detail = status.lastError.split('\n')[0]}
					<Tooltip.Root delayDuration={500}>
						<Tooltip.Trigger
							type="button"
							class="text-muted-foreground focus-visible:ring-ring min-w-0 cursor-help truncate rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none"
						>
							{detail}
						</Tooltip.Trigger>
						<Tooltip.Content
							side="bottom"
							align="start"
							sideOffset={6}
							class="max-w-96 text-left break-words whitespace-pre-line"
						>
							{status.lastError}
						</Tooltip.Content>
					</Tooltip.Root>
				{/if}
			</span>
			{#if onApply}
				<Button variant="outline" size="sm" class="ml-auto h-8 gap-2" onclick={onApply}>
					<span class="icon-[lucide--rotate-ccw] size-4" aria-hidden="true"></span>
					Re-apply
				</Button>
			{/if}
		{:else if status.applyStatus === 'idle'}
			<span class="text-muted-foreground flex items-center gap-2 font-medium">
				<span class="bg-muted-foreground/50 size-2 shrink-0 rounded-full" aria-hidden="true"></span>
				Idle
			</span>
			{@render divider()}
			<span class="text-muted-foreground">No enabled shares, so nothing is exported</span>
		{:else}
			<span class="flex items-center gap-2 font-medium text-emerald-700 dark:text-emerald-400">
				<span class="size-2 shrink-0 rounded-full bg-emerald-500" aria-hidden="true"></span>
				Running
			</span>
			{@render divider()}
			<span>
				Exporting {plural(status.appliedExports, 'file system')}
			</span>
		{/if}
	</div>
{/if}
