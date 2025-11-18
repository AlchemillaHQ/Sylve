<script lang="ts">
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { getFileIcon } from '$lib/utils/icons';
	import { format, isThisYear, isToday, isYesterday } from 'date-fns';
	import humanFormat from 'human-format';
	import { Copy, Download, Edit, Folder, FolderOpen, Scissors, Trash2 } from 'lucide-svelte';

	interface Props {
		items: FileNode[];
		onItemClick: (item: FileNode) => void;
		onItemSelect: (item: FileNode, event?: MouseEvent) => void;
		selectedItems: Set<string>;
		onItemDelete?: (item: FileNode) => void;
		onItemDownload?: (item: FileNode) => void;
		isCopying?: boolean;
		onItemCopy?: (item: FileNode, isCut: boolean) => void;
		onItemRename?: (item: FileNode) => void;
	}

	let {
		items,
		onItemClick,
		onItemSelect,
		selectedItems,
		onItemDelete,
		onItemDownload,
		isCopying,
		onItemCopy,
		onItemRename
	}: Props = $props();

	function formatFileSize(bytes?: number): string {
		if (!bytes || bytes === 0) return '-';
		return humanFormat(bytes, {
			separator: ' ',
			scale: 'binary',
			unit: 'B'
		});
	}

	function formatDate(date: Date): string {
		if (isToday(date)) {
			return format(date, 'hh:mm a'); // e.g., "03:45 PM"
		} else if (isYesterday(date)) {
			return 'Yesterday';
		} else if (isThisYear(date)) {
			return format(date, 'MMM d'); // e.g., "Jul 10"
		} else {
			return format(date, 'MMM d, yyyy'); // e.g., "Jul 10, 2023"
		}
	}
</script>

<div class="rounded-md">
	<div class="border-b p-3">
		<div class="text-muted-foreground grid grid-cols-12 gap-4 text-sm font-medium">
			<div class="col-span-6">Name</div>
			<div class="col-span-3">Size</div>
			<div class="col-span-3">Modified</div>
		</div>
	</div>
	<div>
		{#each items as item}
			{@const itemName = item.id.split('/').pop() || item.id}
			{@const FileIcon = getFileIcon(itemName)}
			{@const isSelected = selectedItems.has(item.id)}
			<ContextMenu.Root>
				<ContextMenu.Trigger
					class="hover:bg-muted/50 group flex w-full cursor-pointer items-center justify-between border-b px-3 py-2 {isSelected
						? 'bg-muted'
						: ''}"
					ondblclick={() => onItemClick(item)}
					onclick={(e) => onItemSelect(item, e)}
					oncontextmenu={(e) => {
						if (!selectedItems.has(item.id)) {
							onItemSelect(item, e);
						}
					}}
				>
					<div class="grid w-full grid-cols-12 items-center gap-4">
						<div class="col-span-6 flex items-center gap-3">
							{#if item.type === 'folder'}
								<span
									class="icon-[material-symbols--folder-rounded] mb-2 h-5 w-5 flex-shrink-0 text-blue-400"
								></span>
							{:else}
								<FileIcon class="mb-2 h-5 w-5 flex-shrink-0 text-blue-400" />
							{/if}
							<span class="truncate text-sm">{itemName}</span>
						</div>
						<div class="text-muted-foreground col-span-3 ml-0.5 text-sm">
							{item.type === 'folder' ? '-' : formatFileSize(item.size)}
						</div>
						<div class="text-muted-foreground col-span-3 text-sm">
							{formatDate(item.date)}
						</div>
					</div>
				</ContextMenu.Trigger>
				<ContextMenu.Content>
					{#if item.type === 'folder'}
						<ContextMenu.Item class="gap-2" onclick={() => onItemClick(item)}>
							<FolderOpen class="h-4 w-4" />
							Open
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item class="gap-2" onclick={() => onItemDownload?.(item)}>
							<Download class="h-4 w-4" />
							Download
						</ContextMenu.Item>
					{/if}
					{#if !isCopying}
						<ContextMenu.Item class="gap-2" onclick={() => onItemCopy?.(item, false)}>
							<Copy class="h-4 w-4" />
							Copy
						</ContextMenu.Item>
						<ContextMenu.Item class="gap-2" onclick={() => onItemCopy?.(item, true)}>
							<Scissors class="h-4 w-4" />
							Cut
						</ContextMenu.Item>
					{/if}
					<ContextMenu.Item class="gap-2" onclick={() => onItemRename?.(item)}>
						<Edit class="h-4 w-4" />
						Rename
					</ContextMenu.Item>
					<ContextMenu.Item class=" gap-2" onclick={() => onItemDelete?.(item)}>
						<Trash2 class="h-4 w-4" />
						Delete
					</ContextMenu.Item>
				</ContextMenu.Content>
			</ContextMenu.Root>
		{/each}
	</div>
</div>
