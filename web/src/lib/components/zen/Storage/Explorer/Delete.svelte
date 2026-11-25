<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { deleteFilesOrFolders } from '$lib/api/system/file-explorer';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { handleAPIError, isRequestCancellation } from '$lib/utils/http';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		completedItems,
		entryIcon,
		entryName,
		explorerErrorMessage,
		hasContents,
		isFolder,
		partialMessage
	} from './explorer';

	let {
		hostname,
		targets,
		onClose,
		onDone
	}: {
		hostname: string;
		targets: FileNode[];
		onClose: () => void;
		onDone: () => void;
	} = $props();

	const id = $props.id();
	const chosen = untrack(() => [...targets]);
	const single = chosen.length === 1 ? chosen[0] : undefined;
	const folders = chosen.filter(hasContents).length;
	const needsTyping = !single || hasContents(single);
	const confirmation = single ? entryName(single.id) : 'delete';
	const title = single
		? single.link
			? 'Delete Link?'
			: isFolder(single)
				? 'Delete Folder?'
				: 'Delete File?'
		: `Delete ${chosen.length} Items?`;

	const lifetime = new AbortController();
	onDestroy(() => lifetime.abort());

	let typed = $state('');
	let deleting = $state(false);
	let deleteError = $state('');
	let stopRequest = $state<(() => void) | null>(null);
	let canConfirm = $derived(!deleting && (!needsTyping || typed.trim() === confirmation));

	async function confirm() {
		if (!canConfirm) return;
		deleting = true;
		deleteError = '';
		const controller = new AbortController();
		stopRequest = () => controller.abort();
		try {
			const response = await deleteFilesOrFolders(
				chosen.map((entry) => entry.id),
				hostname,
				AbortSignal.any([controller.signal, lifetime.signal])
			);
			if (response.status !== 'success') {
				handleAPIError(response);
				deleteError = partialMessage(
					completedItems(response),
					chosen.length,
					'deleted',
					explorerErrorMessage(
						response,
						single ? 'It could not be deleted.' : 'The items could not be deleted.'
					)
				);
				onDone();
				return;
			}
			toast.success(single ? `${single.id} deleted` : `${chosen.length} items deleted`, {
				position: 'bottom-center'
			});
			onDone();
			onClose();
		} catch (error) {
			if (!isRequestCancellation(error)) throw error;
			if (lifetime.signal.aborted) return;
			deleteError = 'Deleting stopped. Anything that wasn’t deleted yet is still there.';
			onDone();
		} finally {
			stopRequest = null;
			deleting = false;
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
					{title}
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
				{#if single?.link}
					Deleting the link
					<span class="text-foreground text-sm font-semibold break-all">{single.id}</span>
					on <span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					removes only the link, not what it points to.
				{:else if single}
					Deleting
					<span class="text-foreground text-sm font-semibold break-all">{single.id}</span>
					on <span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					permanently removes it{hasContents(single) ? ' and everything inside it' : ''}. This
					cannot be undone.
				{:else}
					Deleting these items on
					<span class="text-foreground text-sm font-semibold break-words">{hostname}</span>
					permanently removes them{folders > 0
						? `, including everything inside ${folders === 1 ? 'the folder' : `the ${folders} folders`}`
						: ''}. This cannot be undone.
				{/if}
			</AlertDialog.Description>
			{#if !single}
				<ScrollArea
					class="rounded-md border"
					fade
					scrollbarYClasses="hidden"
					viewportProps={{
						class: 'max-h-48 overscroll-contain',
						role: 'region',
						'aria-label': 'Items to delete',
						tabindex: 0
					}}
				>
					<ul class="divide-y text-sm">
						{#each chosen as entry (entry.id)}
							<li class="flex min-w-0 items-center gap-2 px-3 py-2">
								<span
									class="{entryIcon(entry)} text-muted-foreground size-4 shrink-0"
									aria-hidden="true"
								></span>
								<span class="min-w-0 font-mono text-xs break-all">{entry.id}</span>
							</li>
						{/each}
					</ul>
				</ScrollArea>
			{/if}
			{#if needsTyping}
				<div class="space-y-2">
					<Label for="{id}-confirm" class="block leading-relaxed">
						Type <span class="text-foreground text-sm font-semibold break-all">{confirmation}</span>
						to confirm
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
			{#if deleting && folders > 0}
				<p class="text-muted-foreground text-sm" role="status">
					Large folders can take a while. Stop keeps anything not deleted yet.
				</p>
			{/if}
			{#if deleteError}
				<p class="text-destructive text-sm break-words" role="alert">{deleteError}</p>
			{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			{#if stopRequest}
				<Button variant="outline" onclick={() => stopRequest?.()}>Stop</Button>
			{/if}
			<Button variant="destructive" class="gap-2" disabled={!canConfirm} onclick={confirm}>
				{#if deleting}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
					Deleting…
				{:else}
					{single ? title.replace('?', '') : `Delete ${chosen.length} Items`}
				{/if}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
