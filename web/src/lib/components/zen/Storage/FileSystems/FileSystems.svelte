<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { getDetailedDatasetsResult } from '$lib/api/zfs/datasets';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import {
		GZFSDatasetTypeSchema,
		type Dataset,
		type DetailedDataset
	} from '$lib/types/zfs/dataset';
	import { cn } from '$lib/utils.js';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { APIRequestError, isAPIResponse, updateCache } from '$lib/utils/http';
	import {
		createColumnHelper,
		renderSnippet,
		type Row,
		type RowSelectionState,
		type SortingState
	} from '@tanstack/svelte-table';
	import { PersistedState, resource, useInterval, watch } from 'runed';
	import { onDestroy } from 'svelte';
	import MenuAction from '../Disks/MenuAction.svelte';
	import { deleteBlock, systemDeletionReasons } from './actions';
	import Delete from './Delete.svelte';
	import Details from './Details.svelte';
	import Editor from './Editor.svelte';
	import Snapshot from './Snapshot.svelte';
	import {
		fileSystemRows,
		isEncrypted,
		isExpandedByDefault,
		isKeyUnavailable,
		isMounted,
		isPoolRoot,
		isSylveDataset,
		mountLabel,
		visibleRows,
		type FileSystemRow,
		type FileSystemsData
	} from './filesystems';
	import Usage from './Usage.svelte';

	let { data }: { data: FileSystemsData } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const lifetime = new AbortController();
	let initialFailure = $state(isAPIResponse(initial.datasets));
	const datasets = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getDetailedDatasetsResult(
				GZFSDatasetTypeSchema.enum.FILESYSTEM,
				hostname,
				requestSignal
			);
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialFailure = false;
			void updateCache('zfs-filesystems', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.datasets) ? initial.datasets : [],
			lazy: initial.fresh ?? false
		}
	);

	let failed = $derived(initialFailure || Boolean(datasets.error));
	let rows = $derived(
		fileSystemRows(datasets.current.filter((dataset) => !isSylveDataset(dataset.name)))
	);
	let pools = $derived([...new Set(datasets.current.map((dataset) => dataset.pool))].sort());
	let selectedId = $state('');
	let selected = $derived(selectedId ? rows.find((row) => rowId(row) === selectedId) : undefined);
	let canModify = $derived(allowed('Sys.Modify', nodePath(initial.hostname)));
	let names = $derived(new Set(datasets.current.map((dataset) => dataset.name)));
	let editor = $state<{ session: number; id?: string; parent?: string } | null>(null);
	let editing = $derived(editor?.id ? rows.find((row) => rowId(row) === editor?.id) : undefined);
	let editorSession = 0;
	let systemReasons = $derived(systemDeletionReasons(datasets.current));
	let selection = $state<RowSelectionState>({});
	let selectedRows = $derived(rows.filter((row) => selection[rowId(row)]));
	let deleteTargets = $state<FileSystemRow[] | null>(null);
	let snapshotId = $state('');
	let snapshotRow = $derived(
		snapshotId ? rows.find((row) => rowId(row) === snapshotId) : undefined
	);

	function openEditor(target: { id?: string; parent?: string }) {
		selectedId = '';
		editor = { session: ++editorSession, ...target };
	}

	function deleteReason(row: FileSystemRow): string {
		return deleteBlock(row, systemReasons);
	}

	function newChildBlock(row: FileSystemRow): string {
		return isKeyUnavailable(row.dataset) ? 'Its encryption key is not loaded' : '';
	}

	function revealPath(name: string) {
		const parts = name.split('/');
		const ancestors = parts.slice(0, -1).map((_, index) => parts.slice(0, index + 1).join('/'));
		expanded.current = {
			...expanded.current,
			...Object.fromEntries(ancestors.map((ancestor) => [ancestor, true]))
		};
	}

	function defaultParent(): string {
		if (selectedPool !== 'all') return selectedPool;
		return pools.length === 1 ? pools[0] : '';
	}
	const pool = new PersistedState(`sylve-zen-filesystems-pool:${initial.hostname}`, 'all');
	const expanded = new PersistedState<Record<string, boolean>>(
		`sylve-zen-filesystems-expanded:${initial.hostname}`,
		{}
	);
	let selectedPool = $derived(
		pool.current === 'all' || pools.includes(pool.current) ? pool.current : 'all'
	);
	let poolRows = $derived(
		selectedPool === 'all' ? rows : rows.filter((row) => row.dataset.pool === selectedPool)
	);
	let sorting = $state<SortingState>([]);
	let search = $state('');
	let tree = $derived(
		!search.trim() && (sorting.length === 0 || (sorting[0].id === 'name' && !sorting[0].desc))
	);
	let treeReason = $derived(
		search.trim()
			? 'Clear the search to use the tree'
			: tree
				? null
				: 'Sort by Name to use the tree'
	);
	let shown = $derived(tree ? visibleRows(poolRows, isExpanded) : poolRows);
	let pageOffset = $state(0);
	let pageStart = $derived(tree ? shown[pageOffset]?.dataset.name : undefined);

	function rowId(row: FileSystemRow): string {
		return row.dataset.guid || row.dataset.name;
	}

	function isExpanded(row: FileSystemRow): boolean {
		return expanded.current[row.dataset.name] ?? isExpandedByDefault(row);
	}

	function toggle(row: FileSystemRow) {
		expanded.current = { ...expanded.current, [row.dataset.name]: !isExpanded(row) };
	}

	function setAll(open: boolean) {
		const names = new Set(rows.map((row) => row.dataset.name));
		const next = Object.fromEntries(
			Object.entries(expanded.current).filter(([name]) => names.has(name))
		);
		for (const row of poolRows) if (row.children > 0) next[row.dataset.name] = open;
		expanded.current = next;
	}

	const interval = useInterval(() => (storage.idle ? 60_000 : 30_000), {
		callback: () => {
			if (storage.visible && !datasets.loading) void datasets.refetch();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle && !datasets.loading) void datasets.refetch();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	const helper = createColumnHelper<DataTableFeatures, FileSystemRow>();
	const columns = helper.columns([
		helper.accessor('order', {
			id: 'name',
			header: 'Name',
			sortFn: 'basic',
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		}),
		helper.accessor((row) => row.dataset.used, {
			id: 'used',
			header: 'Used',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(usedCell, row.original.dataset)
		}),
		helper.accessor((row) => row.dataset.available, {
			id: 'available',
			header: 'Available',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(sizeCell, row.original.dataset.available)
		}),
		helper.accessor((row) => row.dataset.referenced, {
			id: 'referenced',
			header: 'Referenced',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(sizeCell, row.original.dataset.referenced)
		}),
		helper.accessor((row) => mountLabel(row.dataset), {
			id: 'mountpoint',
			header: 'Mount Point',
			cell: ({ row }) => renderSnippet(mountCell, row.original.dataset)
		}),
		helper.display({
			id: 'actions',
			header: () => renderSnippet(actionsHeader, undefined),
			cell: ({ row }) => renderSnippet(rowActions, row.original),
			enableSorting: false,
			enableGlobalFilter: false
		})
	]);
</script>

{#snippet actionsHeader(_value: undefined)}
	<span class="sr-only">Actions</span>
{/snippet}

{#snippet rowActions(row: FileSystemRow)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						aria-label="Actions for {row.dataset.name}"
						{...props}
					>
						<span class="icon-[lucide--ellipsis] size-5" aria-hidden="true"></span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content
				align="end"
				class="w-56"
				onCloseAutoFocus={(event) => {
					if (selectedId || editor || deleteTargets || snapshotId) event.preventDefault();
				}}
			>
				<DropdownMenu.Item class="cursor-pointer" onSelect={() => (selectedId = rowId(row))}>
					<span class="icon-[lucide--eye] size-4" aria-hidden="true"></span>View Details
				</DropdownMenu.Item>
				{#if canModify}
					<DropdownMenu.Separator />
					<MenuAction
						label="Edit"
						icon="icon-[lucide--pencil]"
						onSelect={() => openEditor({ id: rowId(row) })}
					/>
					<MenuAction
						label="Take Snapshot"
						icon="icon-[lucide--camera]"
						onSelect={() => (snapshotId = rowId(row))}
					/>
					<MenuAction
						label="New Child File System"
						icon="icon-[lucide--folder-plus]"
						blocked={newChildBlock(row)}
						onSelect={() => openEditor({ parent: row.dataset.name })}
					/>
					<DropdownMenu.Separator />
					<MenuAction
						label="Delete"
						icon="icon-[lucide--trash-2]"
						blocked={deleteReason(row)}
						destructive
						onSelect={() => (deleteTargets = [row])}
					/>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet nameText(row: FileSystemRow, indented: boolean)}
	<button
		type="button"
		class="focus-visible:ring-ring min-w-0 cursor-pointer truncate rounded-sm text-left underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
		title={row.dataset.name}
		aria-label="View details for {row.dataset.name}"
		aria-haspopup="dialog"
		onclick={() => (selectedId = rowId(row))}
	>
		{#if indented || !row.parent}
			<span class="text-zen-highlight-foreground font-medium"
				>{indented ? row.leaf : row.dataset.name}</span
			>
		{:else}
			<span class="text-muted-foreground">{row.parent}/</span><span
				class="text-zen-highlight-foreground font-medium">{row.leaf}</span
			>
		{/if}
	</button>
{/snippet}

{#snippet markers(dataset: DetailedDataset)}
	{#if isEncrypted(dataset)}
		{@const locked = isKeyUnavailable(dataset)}
		{@const label = locked ? 'Encrypted, key not loaded' : 'Encrypted'}
		<span
			class="icon-[lucide--lock] size-3.5 shrink-0 {locked
				? 'text-amber-600 dark:text-amber-400'
				: 'text-muted-foreground'}"
			title={label}
			aria-hidden="true"
		></span>
		<span class="sr-only">{label}</span>
	{/if}
{/snippet}

{#snippet toggleButton(row: FileSystemRow)}
	{#if row.children > 0}
		{@const open = isExpanded(row)}
		<button
			type="button"
			class="text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-ring/50 -my-1 flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-md outline-none focus-visible:ring-[3px]"
			aria-expanded={open}
			aria-label="{open ? 'Collapse' : 'Expand'} {row.dataset.name}"
			onclick={() => toggle(row)}
		>
			<span
				class="{open ? 'icon-[lucide--chevron-down]' : 'icon-[lucide--chevron-right]'} size-4"
				aria-hidden="true"
			></span>
		</button>
	{:else}
		<span class="size-6 shrink-0" aria-hidden="true"></span>
	{/if}
{/snippet}

{#snippet childCount(row: FileSystemRow)}
	{#if tree && row.children > 0 && !isExpanded(row)}
		<span class="text-muted-foreground shrink-0 text-xs whitespace-nowrap">
			{row.children}
			{row.children === 1 ? 'child' : 'children'}
		</span>
	{/if}
{/snippet}

{#snippet datasetIcon(dataset: Dataset)}
	<span
		class="{isPoolRoot(dataset)
			? 'icon-[bi--hdd-stack-fill]'
			: 'icon-[eos-icons--file-system]'} text-muted-foreground size-4 shrink-0"
		aria-hidden="true"
	></span>
{/snippet}

{#snippet nameCell(row: FileSystemRow)}
	<div
		class="flex min-w-0 items-center gap-2"
		style:padding-left={tree ? `${row.depth * 1.25}rem` : undefined}
	>
		{#if tree}{@render toggleButton(row)}{/if}
		{@render datasetIcon(row.dataset)}
		{@render nameText(row, tree && row.dataset.name !== pageStart)}
		{@render childCount(row)}
		{@render markers(row.dataset)}
	</div>
{/snippet}

{#snippet treeButton(label: string, icon: string, open: boolean)}
	<Tooltip.Root delayDuration={500}>
		<Tooltip.Trigger
			type="button"
			aria-disabled={treeReason ? 'true' : undefined}
			aria-label={treeReason ? `${label}: ${treeReason}` : label}
			class={cn(
				buttonVariants({ variant: 'outline', size: 'icon' }),
				'size-9 aria-disabled:pointer-events-auto aria-disabled:cursor-help'
			)}
			onclick={() => {
				if (!treeReason) setAll(open);
			}}
		>
			<span class="{icon} size-4" aria-hidden="true"></span>
		</Tooltip.Trigger>
		<Tooltip.Content
			side="bottom"
			align="end"
			sideOffset={6}
			class="max-w-64 text-left text-pretty"
		>
			<p class="font-medium">{label}</p>
			{#if treeReason}<p>{treeReason}</p>{/if}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet treeControls()}
	{#if canModify && selectedRows.length > 0}
		<Button
			variant="outline"
			size="sm"
			class="text-destructive h-9 gap-2"
			onclick={() => (deleteTargets = selectedRows)}
		>
			<span class="icon-[lucide--trash-2] size-4" aria-hidden="true"></span>
			Delete Selected ({selectedRows.length})
		</Button>
	{/if}
	{@render treeButton('Expand All', 'icon-[lucide--chevrons-up-down]', true)}
	{@render treeButton('Collapse All', 'icon-[lucide--chevrons-down-up]', false)}
{/snippet}

{#snippet sizeCell(bytes: number)}
	<span class="whitespace-nowrap tabular-nums">{formatBytesBinary(bytes)}</span>
{/snippet}

{#snippet usedCell(dataset: Dataset)}
	<Usage {dataset} class="ml-auto max-w-40" />
{/snippet}

{#snippet mountCell(dataset: Dataset)}
	{@const label = mountLabel(dataset)}
	{#if label.startsWith('/')}
		<span class="font-mono text-xs break-all">{label}</span>
		{#if !isMounted(dataset)}<span class="text-muted-foreground text-xs whitespace-nowrap">
				· Not Mounted</span
			>{/if}
	{:else}
		<span class="text-muted-foreground text-xs">{label}</span>
	{/if}
{/snippet}

{#snippet poolFilter()}
	{#if pools.length > 1}
		<Select.Root type="single" bind:value={pool.current}>
			<Select.Trigger
				class="bg-background h-9 w-full @xl:w-44 dark:hover:bg-input/30"
				aria-label="Filter by pool"
			>
				<span class="truncate">{selectedPool === 'all' ? 'All Pools' : selectedPool}</span>
			</Select.Trigger>
			<Select.Content>
				<Select.Item value="all" label="All Pools" />
				{#each pools as name (name)}
					<Select.Item value={name} label={name} />
				{/each}
			</Select.Content>
		</Select.Root>
	{/if}
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, FileSystemRow>)}
	{@const dataset = row.original.dataset}
	{@const depth = tree ? Math.min(row.original.depth, 4) : 0}
	<div
		class={depth > 0 ? 'border-l-2 pl-3' : undefined}
		style:margin-left={depth > 1 ? `${(depth - 1) * 0.75}rem` : undefined}
	>
		<div class="mb-3 flex min-w-0 items-center gap-2">
			{#if tree && row.original.children > 0}{@render toggleButton(row.original)}{/if}
			{@render datasetIcon(dataset)}
			{@render nameText(row.original, tree && dataset.name !== pageStart)}
			{@render childCount(row.original)}
			{@render markers(dataset)}
			<div class="-my-2 -mr-2 ml-auto shrink-0">{@render rowActions(row.original)}</div>
		</div>
		{@render mobileStats(dataset)}
	</div>
{/snippet}

{#snippet mobileStats(dataset: DetailedDataset)}
	<dl class="grid grid-cols-2 gap-3 text-xs">
		<div>
			<dt class="text-muted-foreground">Used</dt>
			<dd class="mt-1"><Usage {dataset} class="max-w-40" /></dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Available</dt>
			<dd class="mt-1">{@render sizeCell(dataset.available)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Referenced</dt>
			<dd class="mt-1">{@render sizeCell(dataset.referenced)}</dd>
		</div>
		<div class="min-w-0">
			<dt class="text-muted-foreground">Mount Point</dt>
			<dd class="mt-1">{@render mountCell(dataset)}</dd>
		</div>
	</dl>
{/snippet}

<svelte:head><title>File Systems · {initial.hostname}</title></svelte:head>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[eos-icons--file-system] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">File Systems</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<div class="flex items-center gap-2">
			{#if canModify && rows.length === 0}
				<Tooltip.Root delayDuration={500}>
					<Tooltip.Trigger
						type="button"
						aria-disabled="true"
						class={cn(buttonVariants(), 'h-9 cursor-help gap-2 aria-disabled:pointer-events-auto')}
					>
						<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>New File System
						<span class="sr-only">: Create a pool first</span>
					</Tooltip.Trigger>
					<Tooltip.Content side="bottom" align="end" sideOffset={6} class="max-w-64 text-left">
						Create a pool first
					</Tooltip.Content>
				</Tooltip.Root>
			{:else if canModify}
				<Button class="h-9 gap-2" onclick={() => openEditor({ parent: defaultParent() })}>
					<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>New File System
				</Button>
			{/if}
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				disabled={datasets.loading}
				onclick={() => datasets.refetch()}
				aria-label="Refresh file systems"
			>
				<span
					class="icon-[lucide--refresh-cw] size-4"
					class:animate-spin={datasets.loading}
					aria-hidden="true"
				></span>
			</Button>
		</div>
	</header>
	{#if failed}
		<div
			class="bg-card mb-4 flex flex-wrap items-center gap-3 rounded-lg border px-4 py-3 text-sm"
			role="status"
		>
			<span
				class="icon-[lucide--triangle-alert] size-4 shrink-0 text-amber-600 dark:text-amber-400"
				aria-hidden="true"
			></span>
			<p class="min-w-0 flex-1">
				{rows.length > 0
					? 'File systems could not be refreshed. Showing the last successful list.'
					: 'File systems could not be loaded. Please try again.'}
			</p>
			<Button
				variant="outline"
				size="sm"
				disabled={datasets.loading}
				onclick={() => datasets.refetch()}>Try Again</Button
			>
		</div>
	{/if}
	{#if !failed || rows.length > 0}
		<DataTable
			data={shown}
			{columns}
			getRowId={rowId}
			getRowLabel={(row) => row.dataset.name}
			globalFilterFn={(row, _columnId, value) =>
				`${row.original.dataset.name} ${mountLabel(row.original.dataset)}`
					.toLocaleLowerCase()
					.includes(String(value).trim().toLocaleLowerCase())}
			initialSorting={[{ id: 'name', desc: false }]}
			bind:sorting
			bind:searchText={search}
			bind:pageOffset
			bind:selection
			selectable={canModify}
			selectionBlock={deleteReason}
			pageResetKey={selectedPool}
			initialPageSize={25}
			pageSizeOptions={[25, 50, 100]}
			columnClasses={{
				name: 'min-w-56',
				used: 'pr-6 text-right',
				available: 'pr-6 text-right',
				referenced: 'pr-6 text-right',
				actions: 'w-14 pr-3'
			}}
			loading={datasets.loading}
			searchPlaceholder="Search file systems or mount points…"
			emptyMessage="No file systems on this node."
			heading={pools.length > 1 ? poolFilter : undefined}
			toolbar={treeControls}
			{mobileRow}
		/>
	{/if}
</div>

{#if selected}
	{#key selectedId}
		<Details row={selected} stale={failed} onClose={() => (selectedId = '')} />
	{/key}
{/if}

{#if deleteTargets}
	<Delete
		hostname={initial.hostname}
		targets={deleteTargets}
		onClose={() => (deleteTargets = null)}
		onDone={() => void datasets.refetch()}
	/>
{/if}

{#if snapshotRow}
	<Snapshot
		hostname={initial.hostname}
		dataset={snapshotRow.dataset}
		onClose={() => (snapshotId = '')}
	/>
{/if}

{#if editor && (!editor.id || editing)}
	{#key editor.session}
		<Editor
			hostname={initial.hostname}
			row={editing}
			parent={editor.parent}
			parents={rows}
			{names}
			onClose={() => (editor = null)}
			onSaved={(created) => {
				if (created) revealPath(created);
				return datasets.refetch();
			}}
		/>
	{/key}
{/if}
