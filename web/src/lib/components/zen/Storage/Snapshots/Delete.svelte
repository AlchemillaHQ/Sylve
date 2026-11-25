<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { bulkDelete, getSnapshotSummariesResult } from '$lib/api/zfs/datasets';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { SnapshotSummary } from '$lib/types/zfs/dataset';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import {
		APIRequestError,
		handleAPIError,
		isAPIResponse,
		isRequestCancellation
	} from '$lib/utils/http';
	import { onDestroy, onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { snapshotDeleteErrorMessage } from '$lib/utils/zfs/snapshot';
	import {
		sameNamedInsideAll,
		snapshotDeleteBlock,
		snapshotSylveUse,
		snapshotZFSBlock
	} from './snapshots';

	let {
		hostname,
		targets,
		all,
		protectSylve = true,
		onClose,
		onDone
	}: {
		hostname: string;
		targets: SnapshotSummary[];
		all?: SnapshotSummary[];
		protectSylve?: boolean;
		onClose: () => void;
		onDone: () => void;
	} = $props();

	const id = $props.id();
	const lifetime = new AbortController();
	const guarded = untrack(() => protectSylve);
	const blockOf = (snapshot: SnapshotSummary) =>
		guarded ? snapshotDeleteBlock(snapshot) : snapshotZFSBlock(snapshot);
	const warningOf = (snapshot: SnapshotSummary) => (guarded ? '' : snapshotSylveUse(snapshot));
	const chosen = untrack(() => [...targets]);
	const single = chosen.length === 1 ? chosen[0] : undefined;
	const deletable = chosen.filter((snapshot) => !blockOf(snapshot));
	const skipped = chosen.length - deletable.length;

	let summaries = $state<SnapshotSummary[] | null>(untrack(() => all ?? null));
	let summariesFailed = $state(false);
	let inside = $derived(summaries ? sameNamedInsideAll(deletable, summaries) : []);
	let insideDeletable = $derived(inside.filter((snapshot) => !blockOf(snapshot)));
	let insideKept = $derived(inside.length - insideDeletable.length);

	let includeInside = $state(false);
	let typed = $state('');
	let deleting = $state(false);
	let deleteError = $state('');
	let finalTargets = $derived(includeInside ? [...deletable, ...insideDeletable] : deletable);
	let warned = $derived(finalTargets.filter((snapshot) => warningOf(snapshot)));
	let freed = $derived(finalTargets.reduce((total, snapshot) => total + snapshot.used, 0));
	let buttonCount = $derived(finalTargets.length || chosen.length);
	let confirmation = $derived(
		finalTargets.length > 1
			? single
				? single.name
				: 'delete'
			: warned.length > 0
				? finalTargets[0].name
				: ''
	);
	let canConfirm = $derived(
		!deleting && finalTargets.length > 0 && (!confirmation || typed.trim() === confirmation)
	);

	onMount(() => {
		if (!summaries && deletable.length > 0) void loadSummaries();
	});

	onDestroy(() => lifetime.abort());

	async function loadSummaries() {
		try {
			const result = await getSnapshotSummariesResult(hostname, lifetime.signal);
			if (lifetime.signal.aborted) return;
			if (isAPIResponse(result)) summariesFailed = true;
			else summaries = result;
		} catch {
			if (!lifetime.signal.aborted) summariesFailed = true;
		}
	}

	async function confirm() {
		if (!canConfirm) return;
		deleting = true;
		deleteError = '';
		const deleted = finalTargets;
		try {
			const response = await bulkDelete(
				deleted.map((snapshot) => ({ name: snapshot.name, guid: snapshot.guid })),
				{ hostname, signal: lifetime.signal }
			);
			if (response.status !== 'success') {
				handleAPIError(response);
				throw new APIRequestError(response);
			}
			toast.success(
				deleted.length === 1
					? `Snapshot ${deleted[0].name} deleted`
					: `${deleted.length} snapshots deleted`,
				{ position: 'bottom-center' }
			);
			onDone();
			onClose();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			deleteError = snapshotDeleteErrorMessage(
				error instanceof Error ? error.message : 'The snapshots could not be deleted.'
			);
			onDone();
		} finally {
			if (!lifetime.signal.aborted) deleting = false;
		}
	}
</script>

{#snippet warningNote(text: string)}
	<p class="flex items-start gap-2 text-sm leading-relaxed">
		<span
			class="icon-[lucide--triangle-alert] mt-1 size-4 shrink-0 text-amber-500"
			aria-hidden="true"
		></span>
		<span>{text}</span>
	</p>
{/snippet}

{#snippet nameList(snapshots: SnapshotSummary[], label: string)}
	<ScrollArea
		class="rounded-md border"
		fade
		scrollbarYClasses="hidden"
		viewportProps={{
			class: 'max-h-40 overscroll-contain',
			role: 'region',
			'aria-label': label,
			tabindex: 0
		}}
	>
		<ul class="divide-y text-sm">
			{#each snapshots as snapshot (snapshot.name)}
				{@const block = blockOf(snapshot)}
				{@const warning = block ? '' : warningOf(snapshot)}
				<li class="space-y-1 px-3 py-2 text-xs">
					<p class={['font-mono break-words', block && 'text-muted-foreground']}>{snapshot.name}</p>
					{#if block}
						<p class="text-muted-foreground leading-5">Skipped. {block}</p>
					{:else if warning}
						<p class="flex items-start gap-1.5 leading-5">
							<span
								class="icon-[lucide--triangle-alert] mt-0.5 size-3.5 shrink-0 text-amber-500"
								aria-hidden="true"
							></span>
							<span>{warning}</span>
						</p>
					{/if}
				</li>
			{/each}
		</ul>
	</ScrollArea>
{/snippet}

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
					{single ? 'Delete Snapshot?' : `Delete ${chosen.length} Snapshots?`}
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
				{#if single && skipped > 0}
					<span class="text-foreground text-sm font-semibold break-words">{single.name}</span>
					can’t be deleted. {blockOf(single)}
				{:else if single}
					Deleting
					<span class="text-foreground text-sm font-semibold break-words">{single.name}</span>
					on <span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					removes this point-in-time copy. The file system itself isn’t changed. This cannot be undone.
				{:else}
					Deleting these snapshots on
					<span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					removes these point-in-time copies. Their file systems aren’t changed. This cannot be undone.
				{/if}
			</AlertDialog.Description>
			{#if single && skipped === 0 && warningOf(single)}
				{@render warningNote(warningOf(single))}
			{/if}
			{#if !single}
				{#if skipped > 0}
					<p class="text-muted-foreground text-sm">
						{skipped === chosen.length
							? 'None of these can be deleted.'
							: `${skipped} of these can’t be deleted and will be skipped.`}
					</p>
				{/if}
				{@render nameList(chosen, 'Snapshots to delete')}
			{/if}
			{#if !summaries && !summariesFailed && deletable.length > 0}
				<p class="text-muted-foreground flex items-center gap-2 text-sm">
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
					Looking for same-named snapshots inside…
				</p>
			{/if}
			{#if inside.length > 0}
				<div class="space-y-2 rounded-md border p-3 text-sm">
					{#if insideDeletable.length > 0}
						<div class="flex items-start gap-2">
							<Checkbox
								id="{id}-inside"
								class="mt-0.5"
								bind:checked={includeInside}
								disabled={deleting}
							/>
							<Label for="{id}-inside" class="block leading-snug font-normal">
								{#if single}
									Also delete the {insideDeletable.length}
									{insideDeletable.length === 1 ? 'snapshot' : 'snapshots'} named
									<span class="font-semibold break-words">@{single.snapshot}</span> inside
									<span class="font-semibold break-words">{single.dataset}</span>
								{:else}
									Also delete the {insideDeletable.length} same-named
									{insideDeletable.length === 1 ? 'snapshot' : 'snapshots'} inside these file systems
								{/if}
							</Label>
						</div>
						{#if includeInside}
							{@render nameList(insideDeletable, 'Same-named snapshots to delete')}
						{/if}
					{/if}
					{#if insideKept > 0}
						<p class="text-muted-foreground text-xs leading-5">
							{insideKept} same-named {insideKept === 1 ? 'snapshot' : 'snapshots'} inside
							{#if guarded}
								{insideKept === 1 ? 'is' : 'are'} used by Sylve or can’t be deleted, so
							{:else}
								can’t be deleted, so
							{/if}
							{insideKept === 1 ? 'it stays' : 'they stay'}.
						</p>
					{/if}
				</div>
			{/if}
			{#if !single && warned.length > 0}
				{@render warningNote(
					`Sylve uses ${warned.length === 1 ? '1 of these snapshots' : `${warned.length} of these snapshots`}. Deleting ${warned.length === 1 ? 'it' : 'them'} can break backups, replication or guest snapshots.`
				)}
			{/if}
			{#if finalTargets.length > 0}
				<p class="text-muted-foreground text-sm">
					{#if freed > 0}
						Frees at least {formatBytesBinary(freed)}.
					{:else}
						Frees little or no space, because {finalTargets.length === 1 ? 'its' : 'their'} data is still
						shared with the file system or other snapshots.
					{/if}
				</p>
			{/if}
			{#if confirmation}
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
						disabled={deleting}
						onkeydown={(event) => {
							if (event.key === 'Enter') void confirm();
						}}
					/>
				</div>
			{/if}
			{#if deleteError}
				<p class="text-destructive text-sm break-words" role="alert">{deleteError}</p>
			{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button variant="destructive" class="gap-2" disabled={!canConfirm} onclick={confirm}>
				{#if deleting}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
					Deleting…
				{:else}
					{buttonCount === 1 ? 'Delete Snapshot' : `Delete ${buttonCount} Snapshots`}
				{/if}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
