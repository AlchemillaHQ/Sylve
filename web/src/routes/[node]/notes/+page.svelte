<script lang="ts">
	import { createNote, deleteNote, deleteNotes, getNotes, updateNote } from '$lib/api/info/notes';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ScrollArea from '$lib/components/ui/scroll-area/scroll-area.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Note } from '$lib/types/info/notes';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateTableData, markdownToTailwindHTML } from '$lib/utils/info/notes';
	import { convertDbTime } from '$lib/utils/time';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { resource } from 'runed';
	import { storage } from '$lib';
	import { untrack } from 'svelte';
	import { TrixEditor } from 'svelte-trix';

	interface Data {
		notes: Note[];
	}

	let { data }: { data: Data } = $props();

	const notes = resource(
		[],
		async () => {
			const result = await getNotes();
			updateCache('notes', result);
			return result || ([] as Note[]);
		},
		{
			lazy: true,
			initialValue: data.notes as Note[]
		}
	);

	$effect(() => {
		if (storage.visible) {
			untrack(() => {
				notes.refetch();
			});
		}
	});

	let modalState = $state({
		title: '',
		content: '',
		isOpen: false,
		isEditMode: false,
		isDeleteOpen: false,
		isBulkDeleteOpen: false
	});

	let selectedId: number | null = $state(null);

	function handleNote(note?: Note, editMode: boolean = true, reset: boolean = false) {
		if (reset) {
			modalState.title = '';
			modalState.content = '';
			selectedId = null;
			modalState.isEditMode = false;
			modalState.isOpen = false;
			modalState.isDeleteOpen = false;
			modalState.isBulkDeleteOpen = false;
			activeRow = null;
		} else {
			modalState.title = note?.title || '';
			modalState.content = note?.content || '';
			selectedId = note?.id || null;
			modalState.isEditMode = editMode;
			modalState.isOpen = true;
		}
	}

	async function saveNote() {
		if (!modalState.title.trim() || !modalState.content.trim()) return;
		if (modalState.isEditMode && selectedId !== null) {
			const response = await updateNote(selectedId, modalState.title, modalState.content);
			notes.refetch();
			if (response.status === 'success') {
				toast.success('Note updated', { position: 'bottom-center' });
				handleNote(undefined, false, true);
			} else {
				handleAPIError(response);
				toast.error('Failed to update note', {
					position: 'bottom-center'
				});
			}
		} else {
			const response = await createNote(modalState.title, modalState.content);
			notes.refetch();
			if ((response as Note).id) {
				toast.success('Note created', { position: 'bottom-center' });
				handleNote(undefined, false, true);
			}

			if ((response as APIResponse).error) {
				handleAPIError(response as APIResponse);
				toast.error('Failed to create note', {
					position: 'bottom-center'
				});
			}
		}
	}

	function viewNote(id: number | string | undefined) {
		if (notes.current && Array.isArray(notes.current)) {
			const note = notes.current.find((note) => note.id === id);
			if (note) {
				modalState.title = note.title;
				modalState.content = note.content;
				modalState.isEditMode = false;
				modalState.isOpen = true;
			}
		}
	}

	function handleDelete(id: number | string | undefined) {
		if (notes.current && Array.isArray(notes.current)) {
			const note = notes.current.find((note) => note.id === id);
			if (note) {
				modalState.title = note.title;
				modalState.content = note.content;
				modalState.isEditMode = false;
				modalState.isDeleteOpen = true;
			}
		}
	}

	function handleBulkDelete(ids: number[]) {
		if (notes.current && Array.isArray(notes.current)) {
			const notesToDelete = notes.current.filter((note) => ids.includes(note.id));
			if (notesToDelete.length > 0) {
				modalState.title = `${notesToDelete.length} notes`;
				modalState.isBulkDeleteOpen = true;
			}
		}
	}

	let tableName = 'tt-notes';
	let columns: Column[] = $derived([
		{
			field: 'id',
			title: 'ID',
			visible: false
		},
		{
			field: 'name',
			title: 'Name'
		},
		{
			field: 'createdAt',
			title: 'Created',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return convertDbTime(value);
			}
		},
		{
			field: 'updatedAt',
			title: 'Updated',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return convertDbTime(value);
			}
		}
	]);

	let tableData = $derived(
		generateTableData(columns, notes.current && Array.isArray(notes.current) ? notes.current : [])
	);
	let activeRow: Row[] | null = $state(null);
	let query: string = $state('');
</script>

{#snippet button(type: string)}
	{#if activeRow !== null && activeRow.length === 1}
		{#if type === 'view-note'}
			<Button
				onclick={() => activeRow && viewNote(activeRow[0]?.id)}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--eye] mr-1 h-4 w-4"></span>

					<span>View</span>
				</div>
			</Button>
		{/if}

		{#if type === 'delete-note'}
			<Button
				onclick={() => activeRow && handleDelete(activeRow[0]?.id)}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
					<span>Delete</span>
				</div>
			</Button>
		{/if}

		{#if type === 'edit-note'}
			<Button
				onclick={() => {
					if (notes.current && Array.isArray(notes)) {
						const note = notes.find((note) => activeRow && note.id === activeRow[0]?.id);
						handleNote(note, true);
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--note-edit] mr-1 h-4 w-4"></span>
					<span>Edit</span>
				</div>
			</Button>
		{/if}
	{/if}

	{#if activeRow !== null && activeRow.length > 1}
		{#if type === 'bulk-delete-note'}
			<Button
				onclick={() => {
					const ids = activeRow?.map((row) => row.id) || [];
					handleBulkDelete(ids as number[]);
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[material-symbols--delete-sweep] mr-1 h-4 w-4"></span>
					<span>Bulk Delete</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={() => handleNote()} size="sm" class="h-6  ">
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('view-note')}
		{@render button('edit-note')}
		{@render button('delete-note')}
		{@render button('bulk-delete-note')}
	</div>

	<Dialog.Root bind:open={modalState.isOpen}>
		<Dialog.Content class="w-[90%]  overflow-hidden p-5 lg:max-w-2xl">
			<Dialog.Header class="">
				<Dialog.Title class="flex items-center justify-between">
					<div class="flex items-center gap-2">
						<span class={`icon-[${modalState.isEditMode ? 'mdi--note-edit' : 'mdi--note'}] h-5 w-5`}
						></span>
						<span>
							{#if modalState.isEditMode}
								{#if selectedId}
									Edit
								{:else}
									New
								{/if}
							{:else}
								View
							{/if}

							{'Note'}
						</span>
					</div>
					<div class="flex items-center gap-0.5">
						<Button
							size="sm"
							variant="link"
							title={'Reset'}
							class="h-4 "
							onclick={() => {
								modalState.title = '';
								modalState.content = '';
							}}
						>
							<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>

							<span class="sr-only">{'Reset'}</span>
						</Button>
						<Button
							size="sm"
							variant="link"
							class="h-4"
							title={'Close'}
							onclick={() => {
								modalState.isOpen = false;
								modalState.title = '';
								modalState.content = '';
							}}
						>
							<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"
							></span>
							<span class="sr-only">{'Close'}</span>
						</Button>
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<CustomValueInput
				label={'Name'}
				placeholder="Post Upgrade Summary"
				bind:value={modalState.title}
				classes="flex-1 space-y-1"
				disabled={!modalState.isEditMode}
			/>

			<ScrollArea orientation="vertical" class="h-full">
				{#if modalState.isEditMode}
					<div>
						<!-- <CustomValueInput
							label={'Content'}
							placeholder="This is a note"
							bind:value={modalState.content}
							classes="flex-1 space-y-1 "
							type="textarea"
						/> -->
						<TrixEditor bind:value={modalState.content} />
					</div>
				{:else}
					<div class="mt-2">
						<p
							class="mb-2 text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70"
						>
							Content
						</p>
						<article class="prose lg:prose-xl rounded-md border p-3">
							{@html markdownToTailwindHTML(modalState.content)}
						</article>
					</div>
				{/if}
			</ScrollArea>
			<Dialog.Footer class="flex justify-end">
				<div class="flex w-full items-center justify-end gap-2">
					{#if modalState.isEditMode}
						<Button onclick={saveNote} type="submit" size="sm" class="w-full lg:w-28"
							>{'Save'}</Button
						>
					{/if}
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable data={tableData} name={tableName} bind:parentActiveRow={activeRow} bind:query />
	</div>

	<AlertDialog
		open={modalState.isDeleteOpen}
		names={{ parent: 'note', element: modalState?.title || '' }}
		actions={{
			onConfirm: async () => {
				const id = activeRow ? activeRow[0]?.id : null;
				const result = await deleteNote(id as number);
				// notesQuery.refetch();
				notes.refetch();
				if (isAPIResponse(result) && result.status === 'success') {
					handleNote(undefined, false, true);
				} else {
					handleAPIError(result as APIResponse);
					toast.error('Failed to delete note', {
						position: 'bottom-center'
					});
				}
			},
			onCancel: () => {
				modalState.isDeleteOpen = false;
			}
		}}
	></AlertDialog>

	<AlertDialog
		open={modalState.isBulkDeleteOpen}
		names={{ parent: '', element: modalState?.title || '' }}
		actions={{
			onConfirm: async () => {
				const ids = activeRow
					? activeRow.map((row) => (typeof row.id === 'number' ? row.id : parseInt(row.id)))
					: [];
				const result = await deleteNotes(ids);
				notes.refetch();
				if (isAPIResponse(result) && result.status === 'success') {
					handleNote(undefined, false, true);
				} else {
					handleAPIError(result as APIResponse);
					toast.error('Failed to delete notes', {
						position: 'bottom-center'
					});
				}
			},
			onCancel: () => {
				modalState.isBulkDeleteOpen = false;
			}
		}}
	></AlertDialog>

	<!-- bg-white! dark:bg-muted! text-muted! dark:text-white! -->
</div>

<style>
	@reference "#app.css";

	:global(.svelte-trix-content) {
		min-height: 200px;
		max-height: 500px;
		overflow-y: auto;
		resize: vertical;
	}

	:global(.trix-button) {
		@apply bg-white! dark:bg-muted! text-muted! dark:text-white!;
	}
</style>
