<script lang="ts">
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import type { FileNode } from '$lib/types/system/file-explorer';
	import { getFileIcon } from '$lib/utils/icons';
	import { Copy, Download, Edit, FolderOpen, Scissors, Trash2 } from 'lucide-svelte';

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
</script>

<div
	class="grid grid-cols-2 gap-4 p-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 xl:grid-cols-8 2xl:grid-cols-10"
>
	{#each items as item}
		{@const itemName = item.id.split('/').pop() || item.id}
		{@const FileIcon = getFileIcon(itemName)}
		{@const isSelected = selectedItems.has(item.id)}

		<ContextMenu.Root>
			<ContextMenu.Trigger
				title={itemName}
				class="group relative flex w-full cursor-pointer flex-col items-center rounded-lg p-3 {isSelected
					? 'bg-muted border-secondary'
					: 'hover:bg-muted/50'}"
				ondblclick={() => onItemClick(item)}
				onclick={(e) => onItemSelect(item, e)}
				oncontextmenu={(e) => {
					if (!selectedItems.has(item.id)) {
						onItemSelect(item, e);
					}
				}}
			>
				{#if item.type === 'folder'}
					<span
						class="icon-[material-symbols--folder-rounded] mb-2 h-12 w-12 flex-shrink-0 text-blue-400"
					></span>
				{:else}
					<FileIcon class="mb-2 h-12 w-12 flex-shrink-0 text-blue-400" />
				{/if}
				<span
					class="line-clamp-2 w-full px-1 text-center text-xs leading-tight font-medium break-words"
					>{itemName}</span
				>
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
				<ContextMenu.Item
					class=" gap-2"
					onclick={() => {
						if (onItemDelete) {
							onItemDelete(item);
						}
					}}
				>
					<Trash2 class="h-4 w-4" />
					Delete
				</ContextMenu.Item>
			</ContextMenu.Content>
		</ContextMenu.Root>
	{/each}
</div>
