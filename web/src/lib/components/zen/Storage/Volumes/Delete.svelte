<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { bulkDelete, getDatasets } from '$lib/api/zfs/datasets';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { GZFSDatasetTypeSchema, type DetailedDataset } from '$lib/types/zfs/dataset';
	import { APIRequestError, handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { deletionImpact, impactLines, type DeletionImpact } from '../FileSystems/actions';
	import { volumeDeleteErrorMessage } from './volumes';

	let {
		hostname,
		targets,
		onClose,
		onDone
	}: {
		hostname: string;
		targets: DetailedDataset[];
		onClose: () => void;
		onDone: () => void;
	} = $props();

	const id = $props.id();
	const lifetime = new AbortController();
	const chosen = untrack(() => targets.map(({ name, guid }) => ({ name, guid })));
	const single = chosen.length === 1 ? chosen[0] : undefined;
	const confirmation = single ? single.name : 'delete';

	let impact = $state<DeletionImpact | null>(null);
	let impactFailed = $state(false);
	let typed = $state('');
	let deleting = $state(false);
	let deleteError = $state('');
	let lines = $derived(impact ? impactLines(impact, Boolean(single)) : []);
	let checking = $derived(!impact && !impactFailed);
	let canConfirm = $derived(!deleting && !checking && typed.trim() === confirmation);

	async function loadImpact() {
		try {
			const snapshots = await getDatasets(GZFSDatasetTypeSchema.enum.SNAPSHOT, {
				hostname,
				signal: lifetime.signal
			});
			impact = deletionImpact(
				chosen.map((volume) => volume.name),
				snapshots
			);
		} catch (error) {
			if (!lifetime.signal.aborted && !isRequestCancellation(error)) impactFailed = true;
		}
	}

	void loadImpact();
	onDestroy(() => lifetime.abort());

	async function confirm() {
		if (!canConfirm) return;
		deleting = true;
		deleteError = '';
		try {
			const response = await bulkDelete(chosen, { hostname, signal: lifetime.signal });
			if (response.status !== 'success') {
				handleAPIError(response);
				throw new APIRequestError(response);
			}
			toast.success(single ? `Volume ${single.name} deleted` : `${chosen.length} volumes deleted`, {
				position: 'bottom-center'
			});
			onDone();
			onClose();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			deleteError = volumeDeleteErrorMessage(
				error instanceof Error ? error.message : 'The volumes could not be deleted.',
				Boolean(single)
			);
			onDone();
		} finally {
			if (!lifetime.signal.aborted) deleting = false;
		}
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
					{single ? 'Delete Volume?' : `Delete ${chosen.length} Volumes?`}
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
					Deleting
					<span class="text-foreground text-sm font-semibold break-words">{single.name}</span>
					on <span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					permanently erases everything stored on it. This cannot be undone.
				{:else}
					Deleting these volumes on
					<span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					permanently erases everything stored on them. This cannot be undone.
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
						'aria-label': 'Volumes to delete',
						tabindex: 0
					}}
				>
					<ul class="divide-y text-sm">
						{#each chosen as volume (volume.name)}
							<li class="px-3 py-2 font-mono text-xs break-words">{volume.name}</li>
						{/each}
					</ul>
				</ScrollArea>
			{/if}
			{#if checking || impactFailed || lines.length > 0}
				<div class="text-sm" role="status">
					{#if checking}
						<p class="text-muted-foreground flex items-center gap-2">
							<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
							></span>
							Checking for snapshots…
						</p>
					{:else if impactFailed}
						<p class="text-amber-600 dark:text-amber-400">
							{single ? 'Its' : 'Their'} snapshots couldn’t be checked. Any snapshots will be deleted
							too.
						</p>
					{:else}
						<p class="font-medium">Also deleted:</p>
						<ul class="text-muted-foreground mt-1 list-disc space-y-0.5 pl-5">
							{#each lines as line (line)}<li>{line}</li>{/each}
						</ul>
					{/if}
				</div>
			{/if}
			{#if !checking}
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
				{/if}
				{#if deleting}
					Deleting…
				{:else}
					{single ? 'Delete Volume' : `Delete ${chosen.length} Volumes`}
				{/if}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
