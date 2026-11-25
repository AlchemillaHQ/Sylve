<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { createNote, deleteNote, deleteNotes, getNotes, updateNote } from '$lib/api/info/notes';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Note } from '$lib/types/info/notes';
	import {
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		removeCache,
		updateCache
	} from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import {
		createColumnHelper,
		renderSnippet,
		type Row,
		type RowSelectionState
	} from '@tanstack/svelte-table';
	import { resource, watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Data {
		hostname: string;
		notes: Note[] | APIResponse;
		fresh?: boolean;
	}

	let { data }: { data: Data } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const id = $props.id();
	let destroyed = false;
	onDestroy(() => (destroyed = true));

	const notes = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const result = await getNotes({ hostname, signal, preserveErrors: true });
			if (Array.isArray(result)) void updateCache('notes', result, hostname);
			return result;
		},
		{ initialValue: initial.notes, lazy: initial.fresh ?? false }
	);

	watch(
		() => storage.visible,
		(visible) => {
			if (visible) void notes.refetch();
		},
		{ lazy: true }
	);

	let rows = $derived(Array.isArray(notes.current) ? notes.current : []);
	let loadFailed = $derived(isAPIResponse(notes.current));
	let canModify = $derived(allowed('Sys.Modify', nodePath(initial.hostname)));
	let selection = $state<RowSelectionState>({});
	let selectedNotes = $derived(rows.filter((note) => selection[String(note.id)]));

	let dialogOpen = $state(false);
	let dialogMode = $state<'new' | 'edit' | 'view'>('new');
	let currentNote = $state<Note | null>(null);
	let draft = $state({ title: '', content: '' });
	let saving = $state(false);
	let saveError = $state('');
	let deleteOpen = $state(false);
	let deleteTarget = $state<Note[]>([]);
	let deleting = $state(false);
	let deleteError = $state('');
	let busy = $derived(saving || deleting);
	let valid = $derived(draft.title.trim().length >= 3 && draft.content.trim().length >= 3);

	function openNote(note: Note | null = null, mode: 'new' | 'edit' | 'view' = 'new') {
		if (busy) return;
		currentNote = note;
		dialogMode = mode;
		draft = { title: note?.title ?? '', content: note?.content ?? '' };
		saveError = '';
		dialogOpen = true;
	}

	function confirmDelete(target: Note[]) {
		if (busy || !canModify || target.length === 0) return;
		deleteTarget = [...target];
		deleteError = '';
		deleteOpen = true;
	}

	async function saveNote(event: SubmitEvent) {
		event.preventDefault();
		if (busy || !canModify || !valid || dialogMode === 'view') return;
		const editing = dialogMode === 'edit';
		const noteId = currentNote?.id;
		if (editing && !noteId) return;
		saving = true;
		saveError = '';

		try {
			const options = { hostname: initial.hostname, preserveErrors: true };
			const result = editing
				? await updateNote(noteId!, draft.title.trim(), draft.content, options)
				: await createNote(draft.title.trim(), draft.content, options);
			const succeeded = editing
				? isAPIResponse(result) && result.status === 'success'
				: !isAPIResponse(result) && result.id > 0;
			if (!succeeded) {
				if (isAPIResponse(result)) handleAPIError(result);
				saveError = 'Could not save the note. Please try again.';
				return;
			}
			await removeCache('notes', initial.hostname);
			if (destroyed) return;
			dialogOpen = false;
			toast.success(editing ? 'Note updated' : 'Note created', { position: 'bottom-center' });
			await notes.refetch();
		} catch (error) {
			if (!isRequestCancellation(error) && !destroyed) {
				saveError = 'Could not save the note. Please try again.';
			}
		} finally {
			saving = false;
		}
	}

	async function deleteConfirmed() {
		if (busy || !canModify || deleteTarget.length === 0) return;
		deleting = true;
		deleteError = '';
		const ids = deleteTarget.map((note) => note.id);
		try {
			const options = { hostname: initial.hostname, preserveErrors: true };
			const result =
				ids.length === 1 ? await deleteNote(ids[0], options) : await deleteNotes(ids, options);
			if (!isAPIResponse(result) || result.status !== 'success') {
				if (isAPIResponse(result)) handleAPIError(result);
				deleteError = 'Could not delete the notes. Please try again.';
				return;
			}
			await removeCache('notes', initial.hostname);
			if (destroyed) return;
			selection = {};
			deleteOpen = false;
			toast.success(ids.length === 1 ? 'Note deleted' : 'Notes deleted', {
				position: 'bottom-center'
			});
			await notes.refetch();
		} catch (error) {
			if (!isRequestCancellation(error) && !destroyed) {
				deleteError = 'Could not delete the notes. Please try again.';
			}
		} finally {
			deleting = false;
		}
	}

	const columnHelper = createColumnHelper<DataTableFeatures, Note>();
	let columns = $derived(
		columnHelper.columns([
			columnHelper.accessor('title', {
				header: 'Title',
				cell: ({ row }) => renderSnippet(titleCell, row.original)
			}),
			columnHelper.accessor((note) => Date.parse(note.createdAt) || 0, {
				id: 'createdAt',
				header: 'Created',
				cell: ({ row }) => renderSnippet(dateCell, row.original.createdAt),
				enableGlobalFilter: false,
				sortFn: 'basic'
			}),
			columnHelper.accessor((note) => Date.parse(note.updatedAt) || 0, {
				id: 'updatedAt',
				header: 'Updated',
				cell: ({ row }) => renderSnippet(dateCell, row.original.updatedAt),
				enableGlobalFilter: false,
				sortFn: 'basic'
			}),
			columnHelper.display({
				id: 'actions',
				header: () => renderSnippet(actionsHeader, undefined),
				cell: ({ row }) => renderSnippet(rowActions, row.original),
				enableSorting: false,
				enableGlobalFilter: false
			})
		])
	);
</script>

{#snippet titleCell(note: Note)}
	<button
		type="button"
		class="max-w-xs text-left font-medium whitespace-normal break-words underline-offset-4 hover:underline focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2"
		onclick={() => openNote(note, 'view')}
	>
		{note.title}
	</button>
{/snippet}

{#snippet dateCell(value: string)}
	{#if value}<time datetime={value} class="text-muted-foreground whitespace-nowrap"
			>{convertDbTime(value)}</time
		>{:else}<span class="text-muted-foreground">—</span>{/if}
{/snippet}

{#snippet actionsHeader(_value: undefined)}
	<span class="sr-only">Actions</span>
{/snippet}

{#snippet rowActions(note: Note)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={busy}
						aria-label="Actions for {note.title}"
						{...props}
					>
						<span class="icon-[lucide--ellipsis] size-5" aria-hidden="true"></span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end" class="w-40">
				<DropdownMenu.Item onclick={() => openNote(note, 'view')}
					><span class="icon-[lucide--eye] size-4" aria-hidden="true"></span>View</DropdownMenu.Item
				>
				{#if canModify}
					<DropdownMenu.Item onclick={() => openNote(note, 'edit')}
						><span class="icon-[lucide--pencil] size-4" aria-hidden="true"
						></span>Edit</DropdownMenu.Item
					>
					<DropdownMenu.Separator />
					<DropdownMenu.Item
						class="text-destructive focus:text-destructive"
						onclick={() => confirmDelete([note])}
						><span class="icon-[lucide--trash-2] size-4" aria-hidden="true"
						></span>Delete</DropdownMenu.Item
					>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, Note>)}
	<div class="flex items-start justify-between gap-3">
		<div class="min-w-0 flex-1">
			{@render titleCell(row.original)}
			<p class="text-muted-foreground mt-1 text-xs">
				Updated {@render dateCell(row.original.updatedAt)}
			</p>
			<p class="text-muted-foreground mt-2 line-clamp-2 text-sm break-words">
				{row.original.content}
			</p>
		</div>
		{@render rowActions(row.original)}
	</div>
{/snippet}

{#snippet toolbar()}
	{#if canModify && selectedNotes.length > 0}
		<Button
			variant="outline"
			size="sm"
			class="text-destructive h-9 gap-2"
			disabled={busy}
			onclick={() => confirmDelete(selectedNotes)}
		>
			<span class="icon-[lucide--trash-2] size-4" aria-hidden="true"></span>
			Delete Selected ({selectedNotes.length})
		</Button>
	{/if}
{/snippet}

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[lucide--sticky-note] size-5"></span></span
			>
			<h1 class="text-2xl font-semibold tracking-tight">Notes</h1>
		</div>
		<div class="flex items-center gap-2">
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				disabled={notes.loading || busy}
				onclick={() => notes.refetch()}
				aria-label="Refresh notes"
			>
				<span
					class="icon-[lucide--refresh-cw] size-4"
					class:animate-spin={notes.loading}
					aria-hidden="true"
				></span>
			</Button>
			{#if canModify}
				<Button class="h-9 gap-2" disabled={busy} onclick={() => openNote()}
					><span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>New Note</Button
				>
			{/if}
		</div>
	</header>

	{#if loadFailed}
		<div
			class="bg-card flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4"
			role="alert"
		>
			<p class="text-sm">Could not load notes.</p>
			<Button variant="outline" size="sm" disabled={notes.loading} onclick={() => notes.refetch()}
				>Retry</Button
			>
		</div>
	{:else}
		<DataTable
			data={rows}
			{columns}
			getRowId={(note) => String(note.id)}
			getRowLabel={(note) => note.title}
			globalFilterFn={(row, _columnId, value) =>
				`${row.original.title}\n${row.original.content}`
					.toLocaleLowerCase()
					.includes(String(value).trim().toLocaleLowerCase())}
			initialSorting={[{ id: 'updatedAt', desc: true }]}
			bind:selection
			selectable={canModify}
			loading={notes.loading}
			searchPlaceholder="Search notes…"
			emptyMessage="No notes yet. Create your first note to get started."
			{toolbar}
			{mobileRow}
		/>
	{/if}
</div>

<Dialog.Root bind:open={dialogOpen}>
	<Dialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl"
		showCloseButton={false}
		escapeKeydownBehavior={saving ? 'ignore' : 'close'}
	>
		<form class="flex min-h-0 flex-1 flex-col" onsubmit={saveNote}>
			<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
				<Dialog.Header class="min-w-0 gap-1 text-left">
					<Dialog.Title
						class="flex min-h-9 items-center gap-2.5 text-base leading-snug break-words"
					>
						<span
							class="text-zen-accent size-5 shrink-0 {dialogMode === 'new'
								? 'icon-[lucide--file-plus]'
								: dialogMode === 'edit'
									? 'icon-[lucide--pencil]'
									: 'icon-[lucide--sticky-note]'}"
							aria-hidden="true"
						></span>
						<span class="min-w-0"
							>{dialogMode === 'new'
								? 'New Note'
								: dialogMode === 'edit'
									? 'Edit Note'
									: currentNote?.title}</span
						>
					</Dialog.Title>
					<Dialog.Description class="sr-only"
						>Read or edit a Markdown note for this node.</Dialog.Description
					>
				</Dialog.Header>
				<Dialog.Close disabled={saving} class="shrink-0">
					{#snippet child({ props })}
						<Button variant="ghost" size="icon" class="size-9" disabled={saving} {...props}
							><span class="icon-[lucide--x] size-5" aria-hidden="true"></span><span class="sr-only"
								>Close</span
							></Button
						>
					{/snippet}
				</Dialog.Close>
			</div>
			<div class="min-h-0 flex-1 overflow-y-auto p-4">
				{#if dialogMode === 'view'}
					<article
						class="prose prose-sm dark:prose-invert max-w-none break-words [&_pre]:overflow-x-auto [&_table]:block [&_table]:overflow-x-auto"
					>
						{#if currentNote?.contentHtml}
							<!-- eslint-disable-next-line svelte/no-at-html-tags -->
							{@html currentNote.contentHtml}
						{:else}
							<p class="whitespace-pre-wrap">{currentNote?.content}</p>
						{/if}
					</article>
				{:else}
					<div class="space-y-4">
						<div class="space-y-2">
							<Label for="{id}-title">Title</Label>
							<Input
								id="{id}-title"
								bind:value={draft.title}
								required
								minlength={3}
								maxlength={128}
								disabled={saving}
								placeholder="Note title"
							/>
						</div>
						<div class="space-y-2">
							<Label for="{id}-content">Content</Label>
							<Textarea
								id="{id}-content"
								bind:value={draft.content}
								class="min-h-56 resize-y font-mono text-sm"
								required
								minlength={3}
								disabled={saving}
								placeholder="Write your note…"
							/>
							<p class="text-muted-foreground text-xs">Markdown is supported.</p>
						</div>
						{#if saveError}<p class="text-destructive text-sm" role="alert">{saveError}</p>{/if}
					</div>
				{/if}
			</div>
			{#if dialogMode !== 'view' || (canModify && currentNote)}
				<div class="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t p-4">
					{#if dialogMode === 'view' && canModify && currentNote}
						<Button type="button" class="h-9" onclick={() => openNote(currentNote, 'edit')}
							>Edit Note</Button
						>
					{:else if dialogMode !== 'view'}
						<Button type="submit" class="h-9 gap-2" disabled={saving || !valid}
							>{#if saving}<span
									class="icon-[lucide--loader-circle] size-4 animate-spin"
									aria-hidden="true"
								></span>{/if}Save Note</Button
						>
					{/if}
				</div>
			{/if}
		</form>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root bind:open={deleteOpen}>
	<AlertDialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		escapeKeydownBehavior={deleting ? 'ignore' : 'close'}
	>
		<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title
					class="flex min-h-9 items-center gap-2.5 text-base leading-snug break-words"
				>
					<span class="icon-[lucide--trash-2] text-destructive size-5 shrink-0" aria-hidden="true"
					></span>
					<span class="min-w-0">{deleteTarget.length === 1 ? 'Delete Note?' : 'Delete Notes?'}</span
					>
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
		<div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-4">
			<AlertDialog.Description class="break-words">
				{#if deleteTarget.length === 1}
					This permanently deletes the note {deleteTarget[0].title}. This cannot be undone.
				{:else}
					This permanently deletes {deleteTarget.length} notes. This cannot be undone.
				{/if}
			</AlertDialog.Description>
			{#if deleteError}<p class="text-destructive text-sm" role="alert">{deleteError}</p>{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button variant="destructive" class="gap-2" disabled={deleting} onclick={deleteConfirmed}
				>{#if deleting}<span
						class="icon-[lucide--loader-circle] size-4 animate-spin"
						aria-hidden="true"
					></span>{/if}Delete {deleteTarget.length === 1 ? 'Note' : 'Notes'}</Button
			>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
