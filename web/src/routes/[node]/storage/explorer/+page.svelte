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
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { generateBreadcrumbItems, sortFileItems, type SortBy } from '$lib/utils/explorer';
	interface Data {
		files: FileNode[];
	}

	let { data }: { data: Data } = $props();

	let viewMode = $state<'grid' | 'list'>('grid');
	let searchQuery = $state('');
	let currentPath = $state(storage.fileExplorerCurrentPath || '/');
	let folderData = $state<{ [path: string]: FileNode[] }>({ '/': data.files });
	let selectedItems = $state<string[]>([]);
	let sortBy = $state<SortBy>('name-asc');

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

	async function handleItemClick(item: any) {
		if (item.type === 'folder') {
			searchQuery = '';
			currentPath = item.id;
			await loadFolderData(item.id);
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

	$effect(() => {
		storage.fileExplorerCurrentPath = currentPath;
		selectedItems = [];
		if (currentPath !== '/' && !folderData[currentPath]) {
			loadFolderData(currentPath);
		}
	});

	function handleBackClick() {
		if (currentPath === '/') return;

		const parts = currentPath.split('/').filter(Boolean);
		if (parts.length > 1) {
			parts.pop();
			currentPath = '/' + parts.join('/');
		} else {
			currentPath = '/';
		}
	}

	async function loadFolderData(folderId: string) {
		try {
			const response = await getFiles(folderId);
			folderData = { ...folderData, [folderId]: response };
		} catch (error) {
			console.error('Error loading folder data:', error);
			folderData = { ...folderData, [folderId]: [] };
		}
	}

	async function createFileOrFolder() {
		let name = modals.create.name;
		let isFolder = modals.create.isFolder;
		const response = await addFileOrFolder(currentPath, name, isFolder);

		delete folderData[currentPath];
		await loadFolderData(currentPath);

		handleAPIResponse(response, {
			success: `${isFolder ? 'Folder' : 'File'} "${name}" created successfully`,
			error: `Failed to create ${isFolder ? 'folder' : 'file'} "${name}"`
		});

		modals.create.name = '';
	}

	async function handleDeleteFileOrFolder(item: FileNode) {
		modals.delete.item = item;
		modals.delete.isOpen = true;
	}

	async function refreshCurrentFolder() {
		delete folderData[currentPath];
		await loadFolderData(currentPath);
		selectedItems = [];
	}

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

		const hash = await getTokenHash();
		const downloadUrl = `/api/system/file-explorer/download?id=${encodeURIComponent(item.id)}&hash=${hash}`;
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

	async function handleCopyFileOrFolder(item: FileNode, isCut: boolean) {
		const itemsToCopy = selectedItems.length > 0 ? selectedItems : [item.id];
		copyFileOrFolder.items = itemsToCopy;
		copyFileOrFolder.isCut = isCut;
	}

	async function pasteFileOrFolder() {
		if (!copyFileOrFolder.items || copyFileOrFolder.items.length === 0) return;

		const requestData: [string, string][] = copyFileOrFolder.items.map((itemId) => [
			itemId,
			currentPath
		]);

		await copyOrMoveFilesOrFolders(requestData, copyFileOrFolder.isCut);

		delete folderData[currentPath];
		await loadFolderData(currentPath);

		copyFileOrFolder.items = [];
		copyFileOrFolder.isCut = false;
	}

	async function handleRenameFileOrFolder(item: FileNode) {
		modals.rename.id = item.id;
		modals.rename.isOpen = true;
		let name = item.id.split('/').pop() || item.id;
		modals.rename.newName = name;
	}

	async function handleBreadcrumbNavigate(path: string) {
		searchQuery = '';
		selectedItems = [];
		currentPath = path;

		if (!folderData[path]) {
			await loadFolderData(path);
		}
	}

	async function rename() {
		if (!modals.rename.id || !modals.rename.newName) return;

		const response = await renameFileOrFolder(modals.rename.id, modals.rename.newName);
		delete folderData[currentPath];
		await loadFolderData(currentPath);
		handleAPIResponse(response, {
			success: 'Renamed successfully',
			error: Array.isArray(response.error)
				? response.error.join(', ')
				: (response.error ?? 'Failed to rename')
		});
		modals.rename.isOpen = false;
		modals.rename.id = '';
		modals.rename.newName = '';
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
			{viewMode}
			onSearchChange={(value) => (searchQuery = value)}
			onSortChange={(sort) => (sortBy = sort)}
			onViewModeChange={(mode) => (viewMode = mode)}
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
				{#if viewMode === 'grid'}
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
					<ContextMenu.Item class="gap-2" onclick={pasteFileOrFolder}>
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

		<div class="bg-muted/30 flex flex-shrink-0 items-center justify-between border-t px-4 py-1">
			<div class="text-muted-foreground flex items-center gap-4 text-sm">
				<span>{sortedItems.length} items</span>
			</div>
			<div class="text-muted-foreground text-sm">
				{sortedItems.filter((item: any) => item.type === 'folder').length} folders,
				{sortedItems.filter((item: any) => item.type === 'file').length} files
			</div>
		</div>
	</div>
</div>

<CreateFileOrFolderModal
	bind:isOpen={modals.create.isOpen}
	bind:isFolder={modals.create.isFolder}
	bind:name={modals.create.name}
	onClose={() => {
		modals.create.isOpen = false;
		modals.create.isFolder = true;
	}}
	onReset={() => {
		modals.create.name = '';
	}}
	onCreate={() => {
		createFileOrFolder();
		modals.create.isOpen = false;
		modals.create.isFolder = true;
	}}
/>

<RenameModal
	bind:isOpen={modals.rename.isOpen}
	bind:newName={modals.rename.newName}
	onClose={() => {
		modals.rename.isOpen = false;
		modals.rename.id = '';
	}}
	onReset={() => {
		modals.rename.newName = modals.rename.id.split('/').pop() || '';
	}}
	onRename={() => {
		rename();
	}}
/>

<AlertDialog
	open={modals.delete.isOpen}
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
		onConfirm: async () => {
			if (modals.delete.item) {
				const response = await deleteFilesOrFolders(selectedItems);
				const single = selectedItems.length === 1 && modals.delete.item;

				handleAPIResponse(response, {
					success: single
						? `${modals.delete.item.type === 'folder' ? 'Folder' : 'File'} "${modals.delete.item.id.split('/').pop() || ''}" was deleted successfully.`
						: `${selectedItems.length} item${selectedItems.length > 1 ? 's' : ''} were deleted successfully.`,
					error: single
						? `Failed to delete ${modals.delete.item.type === 'folder' ? 'folder' : 'file'} "${modals.delete.item.id.split('/').pop() || ''}".`
						: `Failed to delete ${selectedItems.length} item${selectedItems.length > 1 ? 's' : ''}.`
				});
			}
			delete folderData[currentPath];
			await loadFolderData(currentPath);
			modals.delete.isOpen = false;
			modals.delete.item = null;
		},
		onCancel: () => {
			modals.delete.isOpen = false;
			modals.delete.item = null;
		}
	}}
></AlertDialog>

<FilepondModal
	bind:isOpen={modals.filepond.isOpen}
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
