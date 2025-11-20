<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { Input } from '$lib/components/ui/input';
	import type { SortBy } from '$lib/utils/explorer';

	interface Props {
		searchQuery: string;
		sortBy: SortBy;
		viewMode: 'grid' | 'list';
		onSearchChange: (value: string) => void;
		onSortChange: (sort: SortBy) => void;
		onViewModeChange: (mode: 'grid' | 'list') => void;
		onCreateFile: () => void;
		onCreateFolder: () => void;
		onUploadFile: () => void;
	}

	let {
		searchQuery,
		sortBy,
		viewMode,
		onSearchChange,
		onSortChange,
		onViewModeChange,
		onCreateFile,
		onCreateFolder,
		onUploadFile
	}: Props = $props();
</script>

<div class="flex flex-shrink-0 items-center justify-between gap-4 border-b px-4 py-2">
	<div class="flex items-center gap-2">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				<Button size="sm" class="!h-7 gap-2">
					<!-- <Plus class="h-4 w-4" /> -->
					<span class="icon-[lucide--plus] h-4 w-4"></span>
					Add New
				</Button>
			</DropdownMenu.Trigger>
			<DropdownMenu.Content>
				<DropdownMenu.Group>
					<DropdownMenu.Item onclick={onCreateFile}>New File</DropdownMenu.Item>
					<DropdownMenu.Item onclick={onCreateFolder}>New Folder</DropdownMenu.Item>
					<DropdownMenu.Item onclick={onUploadFile}>Upload File</DropdownMenu.Item>
				</DropdownMenu.Group>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>

	<div class="flex items-center gap-2">
		<div class="relative">
			<span class="icon-[lucide--search] text-muted-foreground absolute left-2 top-1.5 h-4 w-4"
			></span>
			<Input
				placeholder="Search files..."
				value={searchQuery}
				oninput={(e) => onSearchChange(e.currentTarget.value)}
				class="!h-7 w-64 pl-8"
			/>
		</div>
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				<Button variant="outline" size="sm" class="h-7 gap-2">
					<span class="icon-[lucide--arrow-up-down] h-4 w-4"></span>
					Sort
				</Button>
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end">
				<DropdownMenu.Group>
					<DropdownMenu.Item
						class={`${sortBy === 'name-asc' ? 'bg-accent' : ''}`}
						onclick={() => onSortChange('name-asc')}
					>
						A - Z
					</DropdownMenu.Item>
					<DropdownMenu.Item
						class={`${sortBy === 'name-desc' ? 'bg-accent' : ''}`}
						onclick={() => onSortChange('name-desc')}
					>
						Z - A
					</DropdownMenu.Item>
					<DropdownMenu.Item
						class={`${sortBy === 'modified-desc' ? 'bg-accent' : ''}`}
						onclick={() => onSortChange('modified-desc')}
					>
						Last Modified
					</DropdownMenu.Item>
					<DropdownMenu.Item
						class={`${sortBy === 'modified-asc' ? 'bg-accent' : ''}`}
						onclick={() => onSortChange('modified-asc')}
					>
						First Modified
					</DropdownMenu.Item>
					<DropdownMenu.Item
						class={`${sortBy === 'size-desc' ? 'bg-accent' : ''}`}
						onclick={() => onSortChange('size-desc')}
					>
						Size
					</DropdownMenu.Item>
					<DropdownMenu.Item
						class={`${sortBy === 'type' ? 'bg-accent' : ''}`}
						onclick={() => onSortChange('type')}
					>
						Type
					</DropdownMenu.Item>
				</DropdownMenu.Group>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
		<div class="flex items-center rounded-md border">
			<Button
				variant={viewMode === 'grid' ? 'default' : 'ghost'}
				size="sm"
				onclick={() => onViewModeChange('grid')}
				class="h-7 rounded-r-none"
			>
				<!-- <Grid3X3 class="h-4 w-4" /> -->
				<span class="icon-[lucide--grid-3x3] h-4 w-4"></span>
			</Button>
			<Button
				variant={viewMode === 'list' ? 'default' : 'ghost'}
				size="sm"
				onclick={() => onViewModeChange('list')}
				class="h-7 rounded-l-none"
			>
				<!-- <List class="h-4 w-4" /> -->
				<span class="icon-[lucide--list] h-4 w-4"></span>
			</Button>
		</div>
	</div>
</div>
