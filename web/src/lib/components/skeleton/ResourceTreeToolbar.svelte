<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import {
		DEFAULT_RESOURCE_TREE_PREFERENCES,
		type ResourceTreeDensity,
		type ResourceTreePreferences,
		type ResourceTreeSortKey,
		type ResourceTreeView
	} from '$lib/resource-tree';

	interface Props {
		preferences: ResourceTreePreferences;
		onChange: (preferences: ResourceTreePreferences) => void;
		searchQuery: string;
		onSearchChange: (value: string) => void;
		onExpandAll: () => void;
		onCollapseAll: () => void;
	}

	let { preferences, onChange, searchQuery, onSearchChange, onExpandAll, onCollapseAll }: Props =
		$props();

	let settingsOpen = $state(false);
	let draftSortKey = $state<ResourceTreeSortKey>(DEFAULT_RESOURCE_TREE_PREFERENCES.sortKey);
	let draftGroupTemplates = $state(DEFAULT_RESOURCE_TREE_PREFERENCES.groupTemplates);
	let draftGroupGuestTypes = $state(DEFAULT_RESOURCE_TREE_PREFERENCES.groupGuestTypes);
	let draftDensity = $state<ResourceTreeDensity>(DEFAULT_RESOURCE_TREE_PREFERENCES.density);

	let selectedViewLabel = $derived(preferences.view === 'folder' ? 'Folder View' : 'Server View');

	function changeView(value: string | undefined) {
		if (value !== 'server' && value !== 'folder') return;
		onChange({ ...preferences, view: value as ResourceTreeView });
	}

	function openSettings() {
		draftSortKey = preferences.sortKey;
		draftGroupTemplates = preferences.groupTemplates;
		draftGroupGuestTypes = preferences.groupGuestTypes;
		draftDensity = preferences.density;
		settingsOpen = true;
	}

	function resetDraft() {
		draftSortKey = DEFAULT_RESOURCE_TREE_PREFERENCES.sortKey;
		draftGroupTemplates = DEFAULT_RESOURCE_TREE_PREFERENCES.groupTemplates;
		draftGroupGuestTypes = DEFAULT_RESOURCE_TREE_PREFERENCES.groupGuestTypes;
		draftDensity = DEFAULT_RESOURCE_TREE_PREFERENCES.density;
	}

	function saveSettings() {
		onChange({
			...preferences,
			sortKey: draftSortKey,
			groupTemplates: draftGroupTemplates,
			groupGuestTypes: draftGroupGuestTypes,
			density: draftDensity
		});
		settingsOpen = false;
	}
</script>

<div class="bg-background/95 shrink-0 border-b px-1.5 py-1.5 backdrop-blur-sm">
	<div class="flex min-w-0 items-center gap-1.5">
		<Select.Root type="single" value={preferences.view} onValueChange={changeView}>
			<Select.Trigger
				size="sm"
				class="h-7! min-w-0 flex-1 justify-center gap-1 bg-background px-1 shadow-xs"
				aria-label="Resource tree view"
				title={selectedViewLabel}
			>
				<span
					class={preferences.view === 'folder'
						? 'icon-[lucide--folders] size-4 shrink-0 text-muted-foreground'
						: 'icon-[lucide--network] size-4 shrink-0 text-muted-foreground'}
					aria-hidden="true"
				></span>
			</Select.Trigger>
			<Select.Content align="start" class="min-w-48">
				<Select.Item value="server" label="Server View">
					<div class="flex items-center gap-2">
						<span class="icon-[lucide--network] size-4 text-muted-foreground"></span>
						<div class="flex flex-col">
							<span>Server View</span>
							<span class="text-muted-foreground text-[11px] font-normal">Group by node</span>
						</div>
					</div>
				</Select.Item>
				<Select.Item value="folder" label="Folder View">
					<div class="flex items-center gap-2">
						<span class="icon-[lucide--folders] size-4 text-muted-foreground"></span>
						<div class="flex flex-col">
							<span>Folder View</span>
							<span class="text-muted-foreground text-[11px] font-normal"
								>Group by resource type</span
							>
						</div>
					</div>
				</Select.Item>
			</Select.Content>
		</Select.Root>

		<Button
			variant="outline"
			size="icon"
			class="h-7! min-w-0 flex-1 bg-background shadow-xs"
			onclick={onExpandAll}
			aria-label="Expand all"
			title="Expand all"
		>
			<span class="icon-[lucide--unfold-vertical] size-4"></span>
		</Button>

		<Button
			variant="outline"
			size="icon"
			class="h-7! min-w-0 flex-1 bg-background shadow-xs"
			onclick={onCollapseAll}
			aria-label="Collapse all"
			title="Collapse all"
		>
			<span class="icon-[lucide--fold-vertical] size-4"></span>
		</Button>

		<Button
			variant="outline"
			size="icon"
			class="h-7! min-w-0 flex-1 bg-background shadow-xs"
			onclick={openSettings}
			aria-label="Tree settings"
			title="Tree settings"
		>
			<span class="icon-[mdi--cog-outline] size-4"></span>
		</Button>
	</div>

	<div class="relative mt-1.5">
		<span
			class="icon-[lucide--search] text-muted-foreground pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2"
		></span>
		<Input
			value={searchQuery}
			oninput={(e) => onSearchChange(e.currentTarget.value)}
			placeholder="Filter resources..."
			class="h-7 bg-background pl-7 pr-7 text-xs shadow-xs"
			aria-label="Filter resources"
		/>
		{#if searchQuery}
			<button
				type="button"
				class="text-muted-foreground hover:text-foreground absolute right-1.5 top-1/2 flex size-4 -translate-y-1/2 items-center justify-center"
				onclick={() => onSearchChange('')}
				aria-label="Clear filter"
			>
				<span class="icon-[lucide--x] size-3.5"></span>
			</button>
		{/if}
	</div>
</div>

<Dialog.Root bind:open={settingsOpen}>
	<Dialog.Content
		class="w-[90%] overflow-hidden sm:max-w-[460px]"
		showCloseButton={true}
		showResetButton={true}
		onReset={resetDraft}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--file-tree-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Tree Settings"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-5">
			<div class="grid items-center gap-2 sm:grid-cols-[1fr_180px] sm:gap-4">
				<div class="space-y-0.5">
					<Label for="resource-tree-sort">Sort guests by</Label>
					<p class="text-muted-foreground text-xs">Applied within each resource group.</p>
				</div>
				<Select.Root type="single" bind:value={draftSortKey}>
					<Select.Trigger id="resource-tree-sort" size="sm" class="h-8 w-full">
						{draftSortKey === 'name' ? 'Name' : 'Guest ID'}
					</Select.Trigger>
					<Select.Content>
						<Select.Item value="id" label="Guest ID">Guest ID</Select.Item>
						<Select.Item value="name" label="Name">Name</Select.Item>
					</Select.Content>
				</Select.Root>
			</div>

			<div class="grid items-center gap-2 sm:grid-cols-[1fr_180px] sm:gap-4">
				<div class="space-y-0.5">
					<Label for="resource-tree-density">Row density</Label>
					<p class="text-muted-foreground text-xs">Compact fits more rows per screen.</p>
				</div>
				<Select.Root type="single" bind:value={draftDensity}>
					<Select.Trigger id="resource-tree-density" size="sm" class="h-8 w-full">
						{draftDensity === 'compact' ? 'Compact' : 'Comfortable'}
					</Select.Trigger>
					<Select.Content>
						<Select.Item value="comfortable" label="Comfortable">Comfortable</Select.Item>
						<Select.Item value="compact" label="Compact">Compact</Select.Item>
					</Select.Content>
				</Select.Root>
			</div>

			<div class="space-y-2">
				<div
					class="hover:bg-muted/40 flex items-start gap-3 rounded-lg border p-3 transition-colors"
				>
					<Checkbox
						id="resource-tree-group-templates"
						class="mt-0.5"
						bind:checked={draftGroupTemplates}
					/>
					<div class="min-w-0 space-y-1">
						<Label for="resource-tree-group-templates" class="cursor-pointer">Group templates</Label
						>
						<p class="text-muted-foreground text-xs leading-relaxed">
							Keep VM and jail templates in a dedicated Templates branch.
						</p>
					</div>
				</div>

				<div
					class="hover:bg-muted/40 flex items-start gap-3 rounded-lg border p-3 transition-colors"
				>
					<Checkbox
						id="resource-tree-group-guests"
						class="mt-0.5"
						bind:checked={draftGroupGuestTypes}
					/>
					<div class="min-w-0 space-y-1">
						<Label for="resource-tree-group-guests" class="cursor-pointer">Group guest types</Label>
						<p class="text-muted-foreground text-xs leading-relaxed">
							Create separate VM and Jail branches.
						</p>
					</div>
				</div>
			</div>
		</div>

		<Dialog.Footer class="flex justify-end">
			<Button size="sm" onclick={saveSettings}>Save settings</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
