<script lang="ts">
	import { getTokenHash } from '$lib/api/auth';
	import { handleAPIResponse } from '$lib/api/common';
	import {
		addFileOrFolder,
		copyOrMoveFilesOrFolders,
		deleteFilesOrFolders,
		getFiles,
		renameFileOrFolder
	} from '$lib/api/system/file-explorer';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Breadcrumb from '$lib/components/custom/FileExplorer/Breadcrumb.svelte';
	import CreateFileOrFolderModal from '$lib/components/custom/FileExplorer/CreateFileOrFolderModal.svelte';
	import FilepondModal from '$lib/components/custom/FileExplorer/FilepondModal.svelte';
	import GridView from '$lib/components/custom/FileExplorer/GridView.svelte';
	import ListView from '$lib/components/custom/FileExplorer/ListView.svelte';
	import RenameModal from '$lib/components/custom/FileExplorer/RenameModal.svelte';
	import Toolbar from '$lib/components/custom/FileExplorer/Toolbar.svelte';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import { storage } from '$lib';
	import type { APIResponse } from '$lib/types/common';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { generateBreadcrumbItems, sortFileItems, type SortBy } from '$lib/utils/explorer';
	import { isAPIResponse } from '$lib/utils/http';
	import { toHex } from '$lib/utils/string';
	import { PersistedState, watch } from 'runed';
	import { onMount } from 'svelte';

	interface Data {
		node: string;
		files: FileNode[];
		filesError: APIResponse | null;
	}

	let { data }: { data: Data } = $props();

	const viewMode = new PersistedState<'grid' | 'list'>('file-explorer-view-mode', 'grid');
	const initialPath = storage.fileExplorerCurrentPath || '/';

	let searchQuery = $state('');
	let currentPath = $state('/');

	// svelte-ignore state_referenced_locally
	let folderData = $state<{ [path: string]: FileNode[] }>({ '/': data.files });

	let selectedItems = $state<string[]>([]);
	let sortBy = $state<SortBy>('name-asc');
	let createLoading = $state(false);
	let renameLoading = $state(false);
	let transferLoading = $state(false);
	let navigationRequest = 0;
	let createRequest = 0;
	let renameRequest = 0;
	let transferRequest = 0;

	let copyFileOrFolder = $state({
		items: [] as string[],
		isCut: false
	});

	let modals = $state({
		create: {
			isOpen: false,
			isFolder: true,
			name: ''
		},
		delete: {
			isOpen: false,
			item: null as FileNode | null
		},
		rename: {
			isOpen: false,
			id: '',
			newName: ''
		},
		filepond: {
			isOpen: false
		}
	});

	function findItemsInPath(path: string) {
		return folderData[path] || [];
	}

	let currentItems = $derived(findItemsInPath(currentPath));

	let filteredItems = $derived(
		currentItems.filter((item) => {
			const itemName = item.id.split('/').pop() || item.id;
			return itemName.toLowerCase().includes(searchQuery.toLowerCase());
		})
	);

	let sortedItems = $derived(sortFileItems(filteredItems, sortBy));

	let breadcrumbItems = $derived(generateBreadcrumbItems(currentPath));

	async function handleItemClick(item: FileNode) {
		if (item.type === 'folder') {
			await navigateToPath(item.id);
		}
	}

	function handleItemSelect(item: FileNode, event?: MouseEvent) {
		const isSelected = selectedItems.includes(item.id);

		if (event?.ctrlKey || event?.metaKey) {
			selectedItems = isSelected
				? selectedItems.filter((id) => id !== item.id)
				: [...selectedItems, item.id];
		} else if (event?.shiftKey && selectedItems.length > 0) {
			const currentIndex = sortedItems.findIndex((i) => i.id === item.id);
			const lastSelectedIndex = sortedItems.findIndex(
				(i) => i.id === selectedItems[selectedItems.length - 1]
			);

			if (lastSelectedIndex !== -1) {
				const start = Math.min(currentIndex, lastSelectedIndex);
				const end = Math.max(currentIndex, lastSelectedIndex);
				const rangeIds = sortedItems.slice(start, end + 1).map((i) => i.id);
				selectedItems = [...new Set([...selectedItems, ...rangeIds])];
			}
		} else {
			selectedItems = isSelected && selectedItems.length === 1 ? [] : [item.id];
		}
	}

	function handleBackClick() {
		if (currentPath === '/') return;

		const parts = currentPath.split('/').filter(Boolean);
		parts.pop();
		void navigateToPath(parts.length > 0 ? `/${parts.join('/')}` : '/');
	}

	function showBrowseError(response: APIResponse, folderId: string) {
		handleAPIResponse(response, {
			error: `Failed to load folder "${folderId}"`
		});
	}

	async function loadFolderData(folderId: string, node = data.node): Promise<boolean> {
		try {
			const response = await getFiles(folderId, node);
			if (node !== data.node) return false;
			if (isAPIResponse(response)) {
				showBrowseError(response, folderId);
				return false;
			}
			folderData = { ...folderData, [folderId]: response };
			return true;
		} catch (error) {
			console.error('Error loading folder data:', error);
			return false;
		}
	}

	async function navigateToPath(path: string) {
		const request = ++navigationRequest;
		const node = data.node;

		if (!folderData[path] && !(await loadFolderData(path, node))) return;
		if (request !== navigationRequest || node !== data.node) return;

		currentPath = path;
		storage.fileExplorerCurrentPath = path;
		selectedItems = [];
		searchQuery = '';
	}

	async function createFileOrFolder() {
		if (createLoading || !modals.create.name.trim()) return;

		const request = ++createRequest;
		const node = data.node;
		const path = currentPath;
		const name = modals.create.name;
		const isFolder = modals.create.isFolder;
		createLoading = true;

		try {
			const response = await addFileOrFolder(path, name, isFolder, node);
			if (request !== createRequest || node !== data.node) return;

			handleAPIResponse(response, {
				success: `${isFolder ? 'Folder' : 'File'} "${name}" created successfully`,
				error: `Failed to create ${isFolder ? 'folder' : 'file'} "${name}"`
			});
			if (response.status !== 'success') return;

			modals.create.isOpen = false;
			modals.create.isFolder = true;
			modals.create.name = '';
			await refreshFolder(path, node);
		} finally {
			if (request === createRequest) createLoading = false;
		}
	}

	function handleDeleteFileOrFolder(item: FileNode) {
		modals.delete.item = item;
		modals.delete.isOpen = true;
	}

	async function refreshFolder(path: string, node = data.node): Promise<boolean> {
		const loaded = await loadFolderData(path, node);
		if (loaded && node === data.node && path === currentPath) selectedItems = [];
		return loaded;
	}

	async function refreshCurrentFolder() {
		await refreshFolder(currentPath, data.node);
	}

	onMount(() => {
		if (data.filesError) showBrowseError(data.filesError, '/');
		if (initialPath !== '/') void navigateToPath(initialPath);
	});

	watch(
		() => data.node,
		(node, previousNode) => {
			if (!previousNode || node === previousNode) return;

			navigationRequest++;
			createRequest++;
			renameRequest++;
			transferRequest++;
			currentPath = '/';
			storage.fileExplorerCurrentPath = '/';
			folderData = { '/': data.files };
			selectedItems = [];
			searchQuery = '';
			copyFileOrFolder = { items: [], isCut: false };
			createLoading = false;
			renameLoading = false;
			transferLoading = false;
			modals.create = { isOpen: false, isFolder: true, name: '' };
			modals.delete = { isOpen: false, item: null };
			modals.rename = { isOpen: false, id: '', newName: '' };
			modals.filepond.isOpen = false;

			if (data.filesError) showBrowseError(data.filesError, '/');
		}
	);

	function handleEmptySpaceInteraction(e: MouseEvent) {
		const target = e.target as HTMLElement;

		const hasFileItemClasses =
			target.classList.contains('group') ||
			target.classList.contains('cursor-pointer') ||
			target.closest('.group.cursor-pointer') ||
			target.closest('[title]');

		const isContainerElement =
			target.classList.contains('grid-container') ||
			target.classList.contains('list-container') ||
			target.classList.contains('file-explorer-container') ||
			target.classList.contains('grid');

		if (!hasFileItemClasses && (isContainerElement || target === e.currentTarget)) {
			selectedItems = [];
		}
	}

	async function downloadFile(item: FileNode) {
		if (item.type !== 'file') return;

		const node = data.node;
		if (!node) return;
		const hash = await getTokenHash();
		if (!hash || node !== data.node) return;

		const auth = toHex(
			JSON.stringify({
				hostname: node
			})
		);
		const query = new URLSearchParams({ id: item.id, hash, auth });
		const downloadUrl = `/api/system/file-explorer/download?${query.toString()}`;
		const filename = item.id.split('/').pop() || 'download';

		try {
			const link = Object.assign(document.createElement('a'), {
				href: downloadUrl,
				download: filename,
				style: 'display:none'
			});
			document.body.appendChild(link);
			link.click();
			link.remove();
		} catch (error) {
			console.error('Download failed:', error);
			window.open(downloadUrl, '_blank');
		}
	}

	function handleCopyFileOrFolder(item: FileNode, isCut: boolean) {
		if (transferLoading) return;

		const itemsToCopy = selectedItems.length > 0 ? selectedItems : [item.id];
		copyFileOrFolder.items = itemsToCopy;
		copyFileOrFolder.isCut = isCut;
	}

	function parentPath(path: string): string {
		const separator = path.lastIndexOf('/');
		return separator <= 0 ? '/' : path.slice(0, separator);
	}

	async function pasteFileOrFolder() {
		if (transferLoading || copyFileOrFolder.items.length === 0) return;

		const request = ++transferRequest;
		const node = data.node;
		const destination = currentPath;
		const sources = [...copyFileOrFolder.items];
		const move = copyFileOrFolder.isCut;
		const items = sources.map((source) => ({ source, destination }));
		let transferSucceeded = false;
		transferLoading = true;

		try {
			const response = await copyOrMoveFilesOrFolders(items, move, node);
			if (request !== transferRequest || node !== data.node) return;

			handleAPIResponse(response, {
				success: `${sources.length} ${sources.length === 1 ? 'item' : 'items'} ${move ? 'moved' : 'copied'} successfully`,
				error: `Failed to ${move ? 'move' : 'copy'} ${sources.length === 1 ? 'item' : `${sources.length} items`}`
			});
			if (response.status !== 'success') return;
			transferSucceeded = true;

			const affectedFolders = [destination];
			if (move) {
				for (const source of sources) {
					const sourceParent = parentPath(source);
					if (!affectedFolders.includes(sourceParent)) affectedFolders.push(sourceParent);
				}
			}

			const nextFolderData = { ...folderData };
			for (const path of affectedFolders) delete nextFolderData[path];
			folderData = nextFolderData;
			if (affectedFolders.includes(currentPath)) selectedItems = [];

			await loadFolderData(destination, node);
			if (move && currentPath !== destination && affectedFolders.includes(currentPath)) {
				await loadFolderData(currentPath, node);
			}
		} finally {
			if (request === transferRequest) {
				if (transferSucceeded) copyFileOrFolder = { items: [], isCut: false };
				transferLoading = false;
			}
		}
	}

	function handleRenameFileOrFolder(item: FileNode) {
		modals.rename.id = item.id;
		modals.rename.isOpen = true;
		modals.rename.newName = item.id.split('/').pop() || item.id;
	}

	async function handleBreadcrumbNavigate(path: string) {
		await navigateToPath(path);
	}

	async function rename() {
		if (renameLoading || !modals.rename.id || !modals.rename.newName.trim()) return;

		const request = ++renameRequest;
		const node = data.node;
		const path = currentPath;
		const id = modals.rename.id;
		const newName = modals.rename.newName;
		renameLoading = true;

		try {
			const response = await renameFileOrFolder(id, newName, node);
			if (request !== renameRequest || node !== data.node) return;

			handleAPIResponse(response, {
				success: 'Renamed successfully',
				error: `Failed to rename "${id.split('/').pop() || id}"`
			});
			if (response.status !== 'success') return;

			modals.rename.isOpen = false;
			modals.rename.id = '';
			modals.rename.newName = '';
			await refreshFolder(path, node);
		} finally {
			if (request === renameRequest) renameLoading = false;
		}
	}

	async function deleteSelectedItems() {
		const item = modals.delete.item;
		const paths = [...selectedItems];
		if (!item || paths.length === 0) return;

		const node = data.node;
		const path = currentPath;
		const response = await deleteFilesOrFolders(paths, node);
		if (node !== data.node) return;

		const single = paths.length === 1;
		handleAPIResponse(response, {
			success: single
				? `${item.type === 'folder' ? 'Folder' : 'File'} "${item.id.split('/').pop() || ''}" was deleted successfully.`
				: `${paths.length} items were deleted successfully.`,
			error: single
				? `Failed to delete ${item.type === 'folder' ? 'folder' : 'file'} "${item.id.split('/').pop() || ''}".`
				: `Failed to delete ${paths.length} items.`
		});
		if (response.status !== 'success') return;

		modals.delete.isOpen = false;
		modals.delete.item = null;
		await refreshFolder(path, node);
	}

	function cancelDelete() {
		modals.delete.isOpen = false;
		modals.delete.item = null;
	}

	let isDragOver = $state(false);
	let dragCounter = $state(0);
	let droppedFiles = $state<File[]>([]);

	function handleDragEnter(e: DragEvent) {
		e.preventDefault();
		e.stopPropagation();

		dragCounter++;
		if (e.dataTransfer?.types.includes('Files')) {
			isDragOver = true;
		}
	}

	function handleDragLeave(e: DragEvent) {
		e.preventDefault();
		e.stopPropagation();

		dragCounter--;
		if (dragCounter === 0) {
			isDragOver = false;
		}
	}

	function handleDragOver(e: DragEvent) {
		e.preventDefault();
		e.stopPropagation();

		if (e.dataTransfer) {
			e.dataTransfer.dropEffect = 'copy';
		}
	}

	function handleDrop(e: DragEvent) {
		e.preventDefault();
		e.stopPropagation();

		isDragOver = false;
		dragCounter = 0;

		const files = e.dataTransfer?.files;
		if (files && files.length > 0) {
			droppedFiles = Array.from(files);
			modals.filepond.isOpen = true;
		}
	}
</script>

<div class="flex h-full">
	<div class="flex flex-1 flex-col">
		<Breadcrumb
			onBackClick={handleBackClick}
			{currentPath}
			items={breadcrumbItems}
			onNavigate={handleBreadcrumbNavigate}
		/>

		<Toolbar
			{searchQuery}
			{sortBy}
			viewMode={viewMode.current}
			onSearchChange={(value) => (searchQuery = value)}
			onSortChange={(sort) => (sortBy = sort)}
			onViewModeChange={(mode) => (viewMode.current = mode)}
			onCreateFile={() => {
				modals.create.isFolder = false;
				modals.create.isOpen = true;
			}}
			onCreateFolder={() => {
				modals.create.isFolder = true;
				modals.create.isOpen = true;
			}}
			onUploadFile={() => {
				modals.filepond.isOpen = true;
			}}
		/>

		<ContextMenu.Root>
			<ContextMenu.Trigger
				class="file-explorer-container relative flex-1 overflow-y-auto {isDragOver
					? 'drag-over'
					: ''}"
				onclick={handleEmptySpaceInteraction}
				oncontextmenu={handleEmptySpaceInteraction}
				ondragenter={handleDragEnter}
				ondragleave={handleDragLeave}
				ondragover={handleDragOver}
				ondrop={handleDrop}
			>
				{#if isDragOver}
					<div
						class="drag-over-overlay bg-background border-muted absolute inset-0 z-50 flex items-center justify-center backdrop-blur-sm"
					>
						<div class="bg-background rounded-xl border p-8 text-center shadow-xl">
							<span class="icon-[lucide--upload] mx-auto mb-4 h-16 w-16"></span>
							<p class="mb-2 text-xl font-semibold">Drop files here to upload</p>
							<p class="text-sm">Files will be uploaded to the current folder</p>
						</div>
					</div>
				{/if}
				{#if viewMode.current === 'grid'}
					<div class="grid-container h-full w-full">
						<GridView
							items={sortedItems}
							onItemClick={handleItemClick}
							onItemSelect={handleItemSelect}
							selectedItems={new Set(selectedItems)}
							onItemDelete={handleDeleteFileOrFolder}
							onItemDownload={downloadFile}
							onItemCopy={handleCopyFileOrFolder}
							onItemRename={handleRenameFileOrFolder}
							isCopying={copyFileOrFolder.items.length > 0}
						/>
					</div>
				{:else}
					<div class="list-container h-full w-full">
						<ListView
							items={sortedItems}
							onItemClick={handleItemClick}
							onItemSelect={handleItemSelect}
							selectedItems={new Set(selectedItems)}
							onItemDelete={handleDeleteFileOrFolder}
							onItemDownload={downloadFile}
							onItemCopy={handleCopyFileOrFolder}
							onItemRename={handleRenameFileOrFolder}
							isCopying={copyFileOrFolder.items.length > 0}
						/>
					</div>
				{/if}
			</ContextMenu.Trigger>
			<ContextMenu.Content>
				<ContextMenu.Item class="gap-2" onclick={refreshCurrentFolder}>
					<span class="icon-[lucide--rotate-ccw] h-4 w-4"></span>
					Refresh</ContextMenu.Item
				>
				{#if copyFileOrFolder.items.length > 0}
					<ContextMenu.Item class="gap-2" disabled={transferLoading} onclick={pasteFileOrFolder}>
						<span class="icon-[lucide--clipboard] h-4 w-4"></span>
						Paste
					</ContextMenu.Item>
				{/if}
				<ContextMenu.Item
					class="gap-2"
					onclick={() => {
						modals.create.isFolder = false;
						modals.create.isOpen = true;
					}}
				>
					<span class="icon-[lucide--file-text] h-4 w-4"></span>
					New File
				</ContextMenu.Item>
				<ContextMenu.Item
					class="gap-2"
					onclick={() => {
						modals.create.isFolder = true;
						modals.create.isOpen = true;
					}}
				>
					<span class="icon-[lucide--folder] h-4 w-4"></span>
					New Folder
				</ContextMenu.Item>
				<ContextMenu.Item
					class="gap-2"
					onclick={() => {
						modals.filepond.isOpen = true;
					}}
				>
					<span class="icon-[lucide--upload] h-4 w-4"></span>
					Upload File</ContextMenu.Item
				>
			</ContextMenu.Content>
		</ContextMenu.Root>

		<div class="bg-muted/30 flex shrink-0 items-center justify-between border-t px-4 py-1">
			<div class="text-muted-foreground flex items-center gap-4 text-sm">
				{#if transferLoading}
					<span class="flex items-center gap-2">
						<span class="icon-[lucide--loader-circle] h-3.5 w-3.5 animate-spin"></span>
						{copyFileOrFolder.isCut ? 'Moving' : 'Copying'}
						{copyFileOrFolder.items.length}
						{copyFileOrFolder.items.length === 1 ? 'item' : 'items'}…
					</span>
				{:else}
					<span>{sortedItems.length} items</span>
				{/if}
			</div>
			<div class="text-muted-foreground text-sm">
				{sortedItems.filter((item: FileNode) => item.type === 'folder').length} folders,
				{sortedItems.filter((item: FileNode) => item.type === 'file').length} files
			</div>
		</div>
	</div>
</div>

<CreateFileOrFolderModal
	bind:isOpen={modals.create.isOpen}
	bind:isFolder={modals.create.isFolder}
	bind:name={modals.create.name}
	loading={createLoading}
	onClose={() => {
		modals.create.isOpen = false;
		modals.create.isFolder = true;
	}}
	onReset={() => {
		modals.create.name = '';
	}}
	onCreate={createFileOrFolder}
/>

<RenameModal
	bind:isOpen={modals.rename.isOpen}
	bind:newName={modals.rename.newName}
	loading={renameLoading}
	onClose={() => {
		modals.rename.isOpen = false;
		modals.rename.id = '';
	}}
	onReset={() => {
		modals.rename.newName = modals.rename.id.split('/').pop() || '';
	}}
	onRename={rename}
/>

<AlertDialog
	bind:open={modals.delete.isOpen}
	keepOpenOnConfirm={true}
	names={selectedItems.length === 1 && modals.delete.item
		? {
				parent: modals.delete.item.type === 'folder' ? 'folder' : 'file',
				element: modals.delete.item.id.split('/').pop() || ''
			}
		: {
				parent: `${selectedItems.length}`,
				element: selectedItems.length === 1 ? 'item' : 'items'
			}}
	actions={{
		onConfirm: deleteSelectedItems,
		onCancel: cancelDelete
	}}
></AlertDialog>

<FilepondModal
	bind:isOpen={modals.filepond.isOpen}
	hostname={data.node}
	onClose={() => {
		modals.filepond.isOpen = false;
		droppedFiles = [];
	}}
	onUploadComplete={() => {
		refreshCurrentFolder();
	}}
	{currentPath}
	{droppedFiles}
/>
