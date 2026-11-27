<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { deleteSambaShare } from '$lib/api/samba/share';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { SambaShare } from '$lib/types/samba/shares';
	import { isRequestCancellation } from '$lib/utils/http';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { errorCode, shareDeleteError } from './samba';

	let {
		hostname,
		targets,
		connectionCount,
		onClose,
		onDone
	}: {
		hostname: string;
		targets: SambaShare[];
		connectionCount: (share: SambaShare) => number | undefined;
		onClose: () => void;
		onDone: (deleted: number[]) => void;
	} = $props();

	const lifetime = new AbortController();
	const chosen = untrack(() => targets.map((share) => ({ ...share })));
	const single = chosen.length === 1 ? chosen[0] : undefined;

	let deleting = $state(false);
	let failures = $state<{ name: string; reason: string }[]>([]);
	let remaining = $state(chosen.map((share) => share.id));
	let connected = $derived(
		chosen
			.filter((share) => remaining.includes(share.id))
			.reduce((total, share) => total + (connectionCount(share) ?? 0), 0)
	);

	onDestroy(() => lifetime.abort());

	async function confirm() {
		if (deleting) return;
		deleting = true;
		failures = [];
		const deleted: number[] = [];
		const failed: { name: string; reason: string }[] = [];
		let applyFailed = false;
		for (const share of chosen.filter((entry) => remaining.includes(entry.id))) {
			try {
				const response = await deleteSambaShare(share.id, {
					hostname,
					signal: lifetime.signal
				});
				if (lifetime.signal.aborted) return;
				if (response.status === 'success' || response.message === 'samba_apply_failed') {
					deleted.push(share.id);
					applyFailed ||= response.message === 'samba_apply_failed';
				} else if (errorCode(response).startsWith('share_not_found')) {
					deleted.push(share.id);
				} else {
					failed.push({ name: share.name, reason: shareDeleteError(response) });
				}
			} catch (error) {
				if (lifetime.signal.aborted || isRequestCancellation(error)) return;
				failed.push({
					name: share.name,
					reason: error instanceof Error ? error.message : 'It couldn’t be deleted.'
				});
			}
		}
		remaining = remaining.filter((id) => !deleted.includes(id));
		failures = failed;
		deleting = false;
		if (deleted.length) onDone(deleted);
		if (applyFailed) {
			toast.warning(
				'Deleted, but Samba couldn’t reload its configuration. Check that Samba is running.',
				{
					position: 'bottom-center'
				}
			);
		} else if (deleted.length) {
			toast.success(single ? `Share ${single.name} deleted` : `${deleted.length} shares deleted`, {
				position: 'bottom-center'
			});
		}
		if (failed.length === 0) onClose();
	}
</script>

<AlertDialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !deleting) onClose();
	}}
>
	<AlertDialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		escapeKeydownBehavior={deleting ? 'ignore' : 'close'}
		aria-busy={deleting}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span class="icon-[lucide--trash-2] text-destructive size-5 shrink-0" aria-hidden="true"
					></span>
					{single ? 'Delete Share?' : `Delete ${chosen.length} Shares?`}
				</AlertDialog.Title>
			</AlertDialog.Header>
			<AlertDialog.Cancel disabled={deleting}>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={deleting}
					>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</AlertDialog.Cancel>
		</div>
		<div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
			<AlertDialog.Description class="w-full text-left leading-relaxed text-pretty break-words">
				{#if single}
					<span class="text-foreground text-sm font-semibold break-words">{single.name}</span>
					will no longer be shared on
					<span class="text-foreground text-sm font-semibold break-words">{hostname}</span>.
					{#if single.datasetName}
						The dataset
						<span class="text-foreground text-sm font-semibold break-words"
							>{single.datasetName}</span
						> and its files are kept.
					{:else}
						Its files are kept.
					{/if}
				{:else}
					These shares will no longer be shared on
					<span class="text-foreground text-sm font-semibold break-words">{hostname}</span>. Their
					datasets and files are kept.
				{/if}
			</AlertDialog.Description>
			{#if !single}
				<ScrollArea
					class="rounded-md border"
					fade
					scrollbarYClasses="hidden"
					viewportProps={{
						class: 'max-h-40 overscroll-contain',
						role: 'region',
						'aria-label': 'Shares to delete',
						tabindex: 0
					}}
				>
					<ul class="divide-y text-sm">
						{#each chosen as share (share.id)}
							<li class="flex min-w-0 items-center justify-between gap-3 px-3 py-2">
								<span class="truncate font-medium">{share.name}</span>
								{#if share.datasetName}
									<span class="text-muted-foreground truncate font-mono text-xs"
										>{share.datasetName}</span
									>
								{/if}
							</li>
						{/each}
					</ul>
				</ScrollArea>
			{/if}
			{#if connected > 0}
				<p class="flex items-start gap-2 text-sm text-amber-600 dark:text-amber-400">
					<span class="icon-[lucide--triangle-alert] mt-0.5 size-4 shrink-0" aria-hidden="true"
					></span>
					{connected === 1 ? '1 client is' : `${connected} clients are`} connected and will lose access.
				</p>
			{/if}
			{#if failures.length}
				<div class="text-destructive space-y-1 text-sm" role="alert">
					<p class="font-medium">
						{failures.length === 1
							? `${failures[0].name} couldn’t be deleted.`
							: `${failures.length} shares couldn’t be deleted.`}
					</p>
					<ul class="list-disc space-y-0.5 pl-5 break-words">
						{#each failures as failure (failure.name)}
							<li>
								{failures.length === 1 ? failure.reason : `${failure.name}: ${failure.reason}`}
							</li>
						{/each}
					</ul>
				</div>
			{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button variant="destructive" class="gap-2" disabled={deleting} onclick={confirm}>
				{#if deleting}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
					Deleting…
				{:else if failures.length}
					Try Again
				{:else}
					{single ? 'Delete Share' : `Delete ${chosen.length} Shares`}
				{/if}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
