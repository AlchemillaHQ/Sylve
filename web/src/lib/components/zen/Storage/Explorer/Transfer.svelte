<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { copyOrMoveFilesOrFolders, getFiles } from '$lib/api/system/file-explorer';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { handleAPIError, isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		browseErrorMessage,
		compareEntries,
		entryIcon,
		entryName,
		isFolder,
		normalizePath,
		parentPath,
		completedItems,
		partialMessage,
		transferErrorMessage,
		transferProblem
	} from './explorer';

	let {
		hostname,
		mode,
		items,
		start,
		onClose,
		onDone,
		onOpen
	}: {
		hostname: string;
		mode: 'copy' | 'move';
		items: FileNode[];
		start: string;
		onClose: () => void;
		onDone: () => void;
		onOpen: (path: string) => void;
	} = $props();

	const id = $props.id();
	const lifetime = new AbortController();
	const move = untrack(() => mode) === 'move';
	const verb = move ? 'Move' : 'Copy';
	const sources = untrack(() => items.map((item) => item.id));
	const label = sources.length === 1 ? entryName(sources[0]) : `${sources.length} Items`;
	const folderLimit = 300;

	let destination = $state(untrack(() => start));
	let draft = $state(untrack(() => start));
	let draftError = $state('');
	let listing = $state<FileNode[] | null>(null);
	let listingError = $state('');
	let saving = $state(false);
	let saveError = $state('');
	let cancelRequest = $state<(() => void) | null>(null);
	let request = 0;

	let folders = $derived((listing ?? []).filter(isFolder).sort(compareEntries));
	let taken = $derived(new Set((listing ?? []).map((entry) => entryName(entry.id))));
	let problem = $derived(listing ? transferProblem(sources, destination, taken, move) : '');
	let ready = $derived(Boolean(listing) && !problem && !saving);

	async function browse(target: string) {
		const call = ++request;
		destination = target;
		draft = target;
		draftError = '';
		listing = null;
		listingError = '';
		try {
			const result = await getFiles(target, hostname, lifetime.signal);
			if (call !== request) return;
			if (isAPIResponse(result)) {
				listingError = browseErrorMessage(result);
				return;
			}
			listing = result;
		} catch (error) {
			if (call !== request || lifetime.signal.aborted || isRequestCancellation(error)) return;
			listingError = 'This folder could not be loaded.';
		}
	}

	function applyDraft() {
		const next = normalizePath(draft);
		if (!next) {
			draftError = 'Use an absolute path like /zroot/data';
			return;
		}
		if (next !== destination || listingError) void browse(next);
		else draft = next;
	}

	void browse(untrack(() => start));
	onDestroy(() => lifetime.abort());

	async function save() {
		if (!ready) return;
		saving = true;
		saveError = '';
		const target = destination;
		const controller = new AbortController();
		cancelRequest = () => controller.abort();
		try {
			const response = await copyOrMoveFilesOrFolders(
				sources.map((source) => ({ source, destination: target })),
				move,
				hostname,
				AbortSignal.any([controller.signal, lifetime.signal])
			);
			if (response.status !== 'success') {
				handleAPIError(response);
				saveError = partialMessage(
					completedItems(response),
					sources.length,
					move ? 'moved' : 'copied',
					transferErrorMessage(response, move, target)
				);
				onDone();
				return;
			}
			toast.success(`${move ? 'Moved' : 'Copied'} ${label} to ${target}`, {
				position: 'bottom-center',
				action: { label: 'Open', onClick: () => onOpen(target) }
			});
			onDone();
			onClose();
		} catch (error) {
			if (!isRequestCancellation(error)) throw error;
			if (lifetime.signal.aborted) return;
			saveError = `${verb} cancelled. Anything that had already been ${move ? 'moved' : 'copied'} stays in ${target}.`;
			onDone();
		} finally {
			cancelRequest = null;
			saving = false;
		}
	}
</script>

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !saving) onClose();
	}}
>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-lg"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
		interactOutsideBehavior={saving ? 'ignore' : 'close'}
		aria-busy={saving}
	>
		<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
			<Dialog.Header class="min-w-0 text-left">
				<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="{move
							? 'icon-[lucide--folder-input]'
							: 'icon-[lucide--copy]'} text-zen-highlight-foreground size-5 shrink-0"
						aria-hidden="true"
					></span>
					<span class="min-w-0 break-all">{verb} {label}</span>
				</Dialog.Title>
				<Dialog.Description class="sr-only">
					Choose the folder to {verb.toLowerCase()}
					{label} into.
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Close disabled={saving}>
				{#snippet child({ props })}
					<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0" disabled={saving}>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</Dialog.Close>
		</div>
		<div class="min-h-0 space-y-3 overflow-y-auto p-4">
			<div class="space-y-2">
				<div class="flex min-h-5 flex-wrap items-baseline gap-x-1">
					<Label for="{id}-destination">Destination</Label>
					{#if draftError}
						<span id="{id}-destination-error" class="text-destructive text-xs">({draftError})</span>
					{/if}
				</div>
				<div class="flex gap-2">
					<Button
						variant="outline"
						size="icon"
						class="size-9 shrink-0"
						disabled={saving || destination === '/'}
						onclick={() => void browse(parentPath(destination))}
						aria-label="Go to the parent folder"
						title="Go to the parent folder"
					>
						<span class="icon-[lucide--arrow-up] size-4" aria-hidden="true"></span>
					</Button>
					<Input
						id="{id}-destination"
						bind:value={draft}
						class="min-w-0 flex-1 font-mono text-sm"
						autocomplete="off"
						spellcheck={false}
						disabled={saving}
						aria-invalid={draftError ? true : undefined}
						aria-describedby={draftError ? `${id}-destination-error` : undefined}
						onkeydown={(event) => {
							if (event.key === 'Enter') {
								event.preventDefault();
								applyDraft();
							}
						}}
						onblur={applyDraft}
					/>
				</div>
			</div>
			<ScrollArea
				class="h-64 rounded-md border"
				fade
				scrollbarYClasses="hidden"
				viewportProps={{
					class: 'overscroll-contain',
					role: 'region',
					'aria-label': `Folders in ${destination}`,
					tabindex: 0
				}}
			>
				{#if listingError}
					<div class="flex flex-col items-center gap-3 px-4 py-10 text-center text-sm">
						<p>{listingError}</p>
						<Button variant="outline" size="sm" onclick={() => void browse(destination)}>
							Try Again
						</Button>
					</div>
				{:else if !listing}
					<p class="text-muted-foreground flex items-center justify-center gap-2 py-10 text-sm">
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
						Loading…
					</p>
				{:else if folders.length === 0}
					<p class="text-muted-foreground py-10 text-center text-sm">No folders inside.</p>
				{:else}
					<ul class="p-1">
						{#each folders.slice(0, folderLimit) as folder (folder.id)}
							<li>
								<button
									type="button"
									class="hover:bg-accent focus-visible:ring-ring flex w-full min-w-0 cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm focus-visible:ring-2 focus-visible:outline-none"
									disabled={saving}
									onclick={() => void browse(folder.id)}
								>
									<span
										class="{entryIcon(folder)} text-zen-highlight-foreground size-4 shrink-0"
										aria-hidden="true"
									></span>
									<span class="min-w-0 truncate">{entryName(folder.id)}</span>
									<span
										class="icon-[lucide--chevron-right] text-muted-foreground ml-auto size-4 shrink-0"
										aria-hidden="true"
									></span>
								</button>
							</li>
						{/each}
					</ul>
					{#if folders.length > folderLimit}
						<p class="text-muted-foreground px-3 pb-3 text-xs">
							{(folders.length - folderLimit).toLocaleString()} more folders aren’t shown. Type a path
							to go to one of them.
						</p>
					{/if}
				{/if}
			</ScrollArea>
			{#if problem}
				<p class="text-muted-foreground flex items-start gap-2 text-sm" role="status">
					<span class="icon-[lucide--info] mt-0.5 size-4 shrink-0" aria-hidden="true"></span>
					<span>{problem}. Choose another folder.</span>
				</p>
			{/if}
			{#if saving}
				<p class="text-muted-foreground text-sm" role="status">
					{move ? 'Moving' : 'Copying'}… Large folders can take a while. Cancel stops it and removes
					the partly copied item.
				</p>
			{/if}
			{#if saveError}
				<p class="text-destructive text-sm break-words" role="alert">{saveError}</p>
			{/if}
		</div>
		<div class="flex shrink-0 items-center justify-between gap-3 border-t p-4">
			<p class="text-muted-foreground min-w-0 truncate font-mono text-xs" title={destination}>
				{destination}
			</p>
			{#if cancelRequest}
				<Button variant="outline" class="ml-auto h-9 shrink-0" onclick={() => cancelRequest?.()}>
					Cancel
				</Button>
			{/if}
			<Button class="h-9 shrink-0 gap-2" disabled={!ready} onclick={save}>
				{#if saving}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
					{move ? 'Moving…' : 'Copying…'}
				{:else}
					{verb} Here
				{/if}
			</Button>
		</div>
	</Dialog.Content>
</Dialog.Root>
