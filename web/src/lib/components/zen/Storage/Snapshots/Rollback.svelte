<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { rollbackSnapshot } from '$lib/api/zfs/datasets';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { SnapshotSummary } from '$lib/types/zfs/dataset';
	import { APIRequestError, handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { dateToAgo } from '$lib/utils/time';
	import { snapshotRollbackErrorMessage } from '$lib/utils/zfs/snapshot';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { createdTime, newerSnapshots } from './snapshots';

	let {
		hostname,
		target,
		all,
		onClose,
		onDone
	}: {
		hostname: string;
		target: SnapshotSummary;
		all: SnapshotSummary[];
		onClose: () => void;
		onDone: () => void;
	} = $props();

	const id = $props.id();
	const lifetime = new AbortController();
	const snapshot = untrack(() => target);
	const newer = untrack(() => newerSnapshots(target, all));
	const confirmation = newer.length > 0 ? snapshot.name : '';

	let typed = $state('');
	let rolling = $state(false);
	let rollbackError = $state('');
	let canConfirm = $derived(!rolling && (!confirmation || typed.trim() === confirmation));

	onDestroy(() => lifetime.abort());

	async function confirm() {
		if (!canConfirm) return;
		rolling = true;
		rollbackError = '';
		try {
			const response = await rollbackSnapshot(
				snapshot,
				newer.map((entry) => entry.guid),
				{ hostname, signal: lifetime.signal }
			);
			if (response.status !== 'success') {
				handleAPIError(response);
				throw new APIRequestError(response);
			}
			toast.success(`${snapshot.dataset} rolled back to @${snapshot.snapshot}`, {
				position: 'bottom-center'
			});
			onDone();
			onClose();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			rollbackError = snapshotRollbackErrorMessage(
				error instanceof Error ? error.message : 'The file system could not be rolled back.'
			);
			onDone();
		} finally {
			if (!lifetime.signal.aborted) rolling = false;
		}
	}
</script>

<AlertDialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !rolling) onClose();
	}}
>
	<AlertDialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		escapeKeydownBehavior={rolling ? 'ignore' : 'close'}
		aria-busy={rolling}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span class="icon-[lucide--history] text-destructive size-5 shrink-0" aria-hidden="true"
					></span>
					Roll Back to Snapshot?
				</AlertDialog.Title>
			</AlertDialog.Header>
			<AlertDialog.Cancel disabled={rolling}>
				{#snippet child({ props })}
					<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0" disabled={rolling}>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</AlertDialog.Cancel>
		</div>
		<div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
			<AlertDialog.Description class="w-full text-left leading-relaxed text-pretty break-words">
				Rolling back
				<span class="text-foreground text-sm font-semibold break-words">{snapshot.dataset}</span>
				on <span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
				returns it to how it was when
				<span class="text-foreground text-sm font-semibold break-words">@{snapshot.snapshot}</span>
				was taken{#if createdTime(snapshot)}<span title={snapshot.createdAt.toLocaleString()}
						>, {dateToAgo(snapshot.createdAt)}</span
					>{/if}. Every change made since then is lost. This cannot be undone.
			</AlertDialog.Description>
			{#if newer.length > 0}
				<div class="space-y-2">
					<p class="text-sm leading-relaxed">
						This also deletes {newer.length === 1
							? 'the newer snapshot'
							: `the ${newer.length} newer snapshots`} of this file system, and any newer bookmarks:
					</p>
					<ScrollArea
						class="rounded-md border"
						fade
						scrollbarYClasses="hidden"
						viewportProps={{
							class: 'max-h-40 overscroll-contain',
							role: 'region',
							'aria-label': 'Newer snapshots to delete',
							tabindex: 0
						}}
					>
						<ul class="divide-y text-sm">
							{#each newer as entry (entry.name)}
								<li class="flex items-baseline justify-between gap-3 px-3 py-2 text-xs">
									<span class="min-w-0 font-mono break-words">@{entry.snapshot}</span>
									{#if createdTime(entry)}
										<span
											class="text-muted-foreground shrink-0 whitespace-nowrap"
											title={entry.createdAt.toLocaleString()}
										>
											{dateToAgo(entry.createdAt)}
										</span>
									{/if}
								</li>
							{/each}
						</ul>
					</ScrollArea>
				</div>
				<div class="space-y-2">
					<Label for="{id}-confirm" class="block leading-relaxed">
						Type <span class="text-foreground text-sm font-semibold break-words"
							>{confirmation}</span
						> to confirm
					</Label>
					<Input
						id="{id}-confirm"
						bind:value={typed}
						placeholder={confirmation}
						autocomplete="off"
						spellcheck={false}
						disabled={rolling}
						onkeydown={(event) => {
							if (event.key === 'Enter') void confirm();
						}}
					/>
				</div>
			{/if}
			{#if rollbackError}
				<p class="text-destructive text-sm break-words" role="alert">{rollbackError}</p>
			{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button variant="destructive" class="gap-2" disabled={!canConfirm} onclick={confirm}>
				{#if rolling}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
					Rolling Back…
				{:else}
					Roll Back
				{/if}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
