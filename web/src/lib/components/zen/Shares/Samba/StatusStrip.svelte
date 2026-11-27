<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import type { SambaStatus } from '$lib/types/samba/shares';
	import { plural } from './samba';

	let {
		status,
		failed,
		loading = false,
		subject,
		onConnections
	}: {
		status: SambaStatus | undefined;
		failed: boolean;
		loading?: boolean;
		subject: string;
		onConnections: () => void;
	} = $props();
</script>

{#snippet divider()}
	<span class="bg-border hidden h-4 w-px shrink-0 sm:block" aria-hidden="true"></span>
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
				Checking Samba…
			</span>
		{:else if !status}
			<span class="text-muted-foreground flex items-center gap-2">
				<span class="icon-[lucide--circle-help] size-4 shrink-0" aria-hidden="true"></span>
				Samba’s status couldn’t be loaded
			</span>
		{:else if !status.serviceEnabled}
			<span class="flex items-center gap-2">
				<span
					class="icon-[lucide--triangle-alert] size-4 shrink-0 text-amber-600 dark:text-amber-400"
					aria-hidden="true"
				></span>
				The Samba service is turned off, so {subject}. Turn it on in Services.
			</span>
		{:else if !status.running}
			<span class="flex items-center gap-2">
				<span
					class="icon-[lucide--triangle-alert] size-4 shrink-0 text-amber-600 dark:text-amber-400"
					aria-hidden="true"
				></span>
				Samba isn’t running, so {subject}.
			</span>
		{:else}
			<span class="flex items-center gap-2 font-medium text-emerald-700 dark:text-emerald-400">
				<span class="size-2 shrink-0 rounded-full bg-emerald-500" aria-hidden="true"></span>
				Running
			</span>
			{#if status.version}
				{@render divider()}
				<span class="flex items-center gap-1.5">
					<span class="text-muted-foreground">Samba</span>
					<span class="tabular-nums">{status.version}</span>
				</span>
			{/if}
			{@render divider()}
			{#if status.error}
				<span class="text-muted-foreground" title={status.error}
					>Connected clients couldn’t be read</span
				>
			{:else}
				<button
					type="button"
					class="group focus-visible:ring-ring flex cursor-pointer items-center gap-1.5 rounded-sm focus-visible:ring-2 focus-visible:outline-none"
					aria-haspopup="dialog"
					onclick={onConnections}
				>
					<span class="underline-offset-4 group-hover:underline">
						{status.sessions.length === 0
							? 'No clients connected'
							: `${plural(status.sessions.length, 'client')} connected`}
					</span>
					<span
						class="icon-[lucide--chevron-right] text-muted-foreground group-hover:text-foreground size-4 shrink-0"
						aria-hidden="true"
					></span>
				</button>
			{/if}
		{/if}
	</div>
{/if}
