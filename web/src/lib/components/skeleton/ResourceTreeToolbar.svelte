<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import {
		DEFAULT_RESOURCE_TREE_PREFERENCES,
		type ResourceTreePreferences,
		type ResourceTreeSortKey,
		type ResourceTreeView
	} from '$lib/resource-tree';

	interface Props {
		preferences: ResourceTreePreferences;
		onChange: (preferences: ResourceTreePreferences) => void;
	}

	let { preferences, onChange }: Props = $props();

	let settingsOpen = $state(false);
	let draftSortKey = $state<ResourceTreeSortKey>(DEFAULT_RESOURCE_TREE_PREFERENCES.sortKey);
	let draftGroupTemplates = $state(DEFAULT_RESOURCE_TREE_PREFERENCES.groupTemplates);
	let draftGroupGuestTypes = $state(DEFAULT_RESOURCE_TREE_PREFERENCES.groupGuestTypes);

	let selectedViewLabel = $derived(
		preferences.view === 'folder' ? 'Folder View' : 'Server View'
	);

	function changeView(value: string | undefined) {
		if (value !== 'server' && value !== 'folder') return;
		onChange({ ...preferences, view: value as ResourceTreeView });
	}

	function openSettings() {
		draftSortKey = preferences.sortKey;
		draftGroupTemplates = preferences.groupTemplates;
		draftGroupGuestTypes = preferences.groupGuestTypes;
		settingsOpen = true;
	}

	function resetDraft() {
		draftSortKey = DEFAULT_RESOURCE_TREE_PREFERENCES.sortKey;
		draftGroupTemplates = DEFAULT_RESOURCE_TREE_PREFERENCES.groupTemplates;
		draftGroupGuestTypes = DEFAULT_RESOURCE_TREE_PREFERENCES.groupGuestTypes;
	}

	function saveSettings() {
		onChange({
			...preferences,
			sortKey: draftSortKey,
			groupTemplates: draftGroupTemplates,
			groupGuestTypes: draftGroupGuestTypes
		});
		settingsOpen = false;
	}
</script>

<div class="bg-background/95 shrink-0 border-b px-1.5 py-1.5 backdrop-blur-sm">
	<div class="flex min-w-0 items-center gap-1.5">
		<Select.Root type="single" value={preferences.view} onValueChange={changeView}>
			<Select.Trigger
				size="sm"
				class="h-8 min-w-0 flex-1 bg-background px-2.5 shadow-xs"
				aria-label="Resource tree view"
				title={selectedViewLabel}
			>
				<span class="flex min-w-0 items-center gap-2">
					<span
						class={preferences.view === 'folder'
							? 'icon-[lucide--folders] size-4 shrink-0 text-muted-foreground'
							: 'icon-[lucide--network] size-4 shrink-0 text-muted-foreground'}
					></span>
					<span class="truncate">{selectedViewLabel}</span>
				</span>
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
							<span class="text-muted-foreground text-[11px] font-normal">Group by resource type</span>
						</div>
					</div>
				</Select.Item>
			</Select.Content>
		</Select.Root>

		<Button
			variant="outline"
			size="icon"
			class="size-8 shrink-0 bg-background shadow-xs"
			onclick={openSettings}
			aria-label="Tree settings"
		>
			<span class="icon-[mdi--cog-outline] size-4"></span>
		</Button>
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

			<div class="space-y-2">
				<div class="hover:bg-muted/40 flex items-start gap-3 rounded-lg border p-3 transition-colors">
					<Checkbox
						id="resource-tree-group-templates"
						class="mt-0.5"
						bind:checked={draftGroupTemplates}
					/>
					<div class="min-w-0 space-y-1">
						<Label for="resource-tree-group-templates" class="cursor-pointer">Group templates</Label>
						<p class="text-muted-foreground text-xs leading-relaxed">
							Keep VM and jail templates in a dedicated Templates branch.
						</p>
					</div>
				</div>

				<div class="hover:bg-muted/40 flex items-start gap-3 rounded-lg border p-3 transition-colors">
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
