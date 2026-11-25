<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { getTargetsResult } from '$lib/api/iscsi/target';
	import { getDetailedDatasetsResult } from '$lib/api/zfs/datasets';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import { GZFSDatasetTypeSchema, type DetailedDataset } from '$lib/types/zfs/dataset';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { APIRequestError, isAPIResponse, updateCache } from '$lib/utils/http';
	import {
		createColumnHelper,
		renderSnippet,
		type Row,
		type RowSelectionState
	} from '@tanstack/svelte-table';
	import { PersistedState, resource, useInterval, watch } from 'runed';
	import { onDestroy } from 'svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import MenuAction from '../Disks/MenuAction.svelte';
	import { isEncrypted, isKeyUnavailable, propertyValue } from '../FileSystems/filesystems';
	import Snapshot from '../FileSystems/Snapshot.svelte';
	import Delete from './Delete.svelte';
	import Details from './Details.svelte';
	import FlashImage from './FlashImage.svelte';
	import VolumeEditor from './VolumeEditor.svelte';
	import {
		blockSize,
		datasetNames,
		flashBlock,
		sharedBlock,
		isSparse,
		parentChoices,
		userLabel,
		visibleDatasets,
		volumeSize,
		volumeUsers,
		type VolumeUser,
		type VolumesData
	} from './volumes';

	let { data }: { data: VolumesData } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const lifetime = new AbortController();
	let initialFailure = $state(isAPIResponse(initial.datasets));
	const datasets = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getDetailedDatasetsResult(
				GZFSDatasetTypeSchema.enum.VOLUME,
				hostname,
				requestSignal
			);
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialFailure = false;
			void updateCache('zfs-volumes-detailed', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.datasets) ? initial.datasets : [],
			lazy: initial.fresh ?? false
		}
	);
	const targets = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const result = await getTargetsResult(hostname, AbortSignal.any([signal, lifetime.signal]));
			return isAPIResponse(result) ? [] : result;
		},
		{ initialValue: [] }
	);
	const fileSystems = resource(
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
			return result;
		},
		{ initialValue: [] }
	);

	const pool = new PersistedState(`sylve-zen-volumes-pool:${initial.hostname}`, 'all');

	let failed = $derived(initialFailure || Boolean(datasets.error));
	let volumes = $derived(visibleDatasets(datasets.current));
	let users = $derived(volumeUsers(targets.current));
	let pools = $derived([...new Set(volumes.map((volume) => volume.pool))].sort());
	let selectedPool = $derived(
		pool.current === 'all' || pools.includes(pool.current) ? pool.current : 'all'
	);
	let shown = $derived(
		selectedPool === 'all' ? volumes : volumes.filter((volume) => volume.pool === selectedPool)
	);
	let selectedName = $state('');
	let selected = $derived(
		selectedName ? volumes.find((volume) => volume.name === selectedName) : undefined
	);
	let canModify = $derived(allowed('Sys.Modify', nodePath(initial.hostname)));
	let parents = $derived(parentChoices(fileSystems.current));
	let names = $derived(datasetNames(datasets.current, fileSystems.current));
	let editor = $state<{ session: number; name?: string } | null>(null);
	let editing = $derived(
		editor?.name ? volumes.find((volume) => volume.name === editor?.name) : undefined
	);
	let editorSession = 0;
	let selection = $state<RowSelectionState>({});
	let selectedRows = $derived(shown.filter((volume) => selection[rowId(volume)]));
	let deleteTargets = $state<DetailedDataset[] | null>(null);
	let snapshotName = $state('');
	let snapshotTarget = $derived(
		snapshotName ? volumes.find((volume) => volume.name === snapshotName) : undefined
	);
	let flashName = $state('');
	let flashTarget = $derived(
		flashName ? volumes.find((volume) => volume.name === flashName) : undefined
	);

	function rowId(volume: DetailedDataset): string {
		return volume.guid || volume.name;
	}

	function usersOf(volume: DetailedDataset): VolumeUser[] {
		return users.get(volume.name) ?? [];
	}

	function deleteReason(volume: DetailedDataset): string {
		return sharedBlock(usersOf(volume));
	}

	function openEditor(name?: string) {
		selectedName = '';
		editor = { session: ++editorSession, name };
	}

	function defaultParent(): string {
		if (selectedPool !== 'all') return selectedPool;
		const roots = parents.filter((entry) => entry.name === entry.pool);
		return roots.length === 1 ? roots[0].name : '';
	}

	function refresh() {
		void datasets.refetch();
		void targets.refetch();
	}

	function refreshAll() {
		refresh();
		void fileSystems.refetch();
	}

	const interval = useInterval(() => (storage.idle ? 60_000 : 30_000), {
		callback: () => {
			if (storage.visible && !datasets.loading) refresh();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle && !datasets.loading) refresh();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	const helper = createColumnHelper<DataTableFeatures, DetailedDataset>();
	const columns = helper.columns([
		helper.accessor('name', {
			header: 'Name',
			sortFn: 'alphanumeric',
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		}),
		helper.accessor((volume) => volumeSize(volume), {
			id: 'size',
			header: 'Size',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ getValue }) => renderSnippet(sizeCell, getValue())
		}),
		helper.accessor('used', {
			header: 'Used',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ getValue }) => renderSnippet(sizeCell, getValue())
		}),
		helper.accessor((volume) => (isSparse(volume) ? 'Sparse' : 'Thick'), {
			id: 'provisioning',
			header: 'Provisioning',
			sortFn: 'alphanumeric',
			enableGlobalFilter: false,
			cell: ({ getValue }) => renderSnippet(textCell, getValue())
		}),
		helper.accessor((volume) => blockSize(volume), {
			id: 'blockSize',
			header: 'Block Size',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ getValue }) => renderSnippet(blockCell, getValue())
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

{#snippet rowActions(volume: DetailedDataset)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						aria-label="Actions for {volume.name}"
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
					if (selectedName || editor || deleteTargets || snapshotName || flashName)
						event.preventDefault();
				}}
			>
				<DropdownMenu.Item class="cursor-pointer" onSelect={() => (selectedName = volume.name)}>
					<span class="icon-[lucide--eye] size-4" aria-hidden="true"></span>View Details
				</DropdownMenu.Item>
				{#if canModify}
					<DropdownMenu.Separator />
					<MenuAction
						label="Edit"
						icon="icon-[lucide--pencil]"
						onSelect={() => openEditor(volume.name)}
					/>
					<MenuAction
						label="Take Snapshot"
						icon="icon-[lucide--camera]"
						onSelect={() => (snapshotName = volume.name)}
					/>
					<MenuAction
						label="Flash Image"
						icon="icon-[lucide--hard-drive-download]"
						blocked={flashBlock(volume, usersOf(volume))}
						onSelect={() => (flashName = volume.name)}
					/>
					<DropdownMenu.Separator />
					<MenuAction
						label="Delete"
						icon="icon-[lucide--trash-2]"
						blocked={deleteReason(volume)}
						destructive
						onSelect={() => (deleteTargets = [volume])}
					/>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet nameText(volume: DetailedDataset)}
	{@const slash = volume.name.lastIndexOf('/')}
	<button
		type="button"
		class="focus-visible:ring-ring min-w-0 cursor-pointer truncate rounded-sm text-left underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
		title={volume.name}
		aria-label="View details for {volume.name}"
		aria-haspopup="dialog"
		onclick={() => (selectedName = volume.name)}
	>
		<span class="text-muted-foreground">{volume.name.slice(0, slash + 1)}</span><span
			class="text-zen-highlight-foreground font-medium">{volume.name.slice(slash + 1)}</span
		>
	</button>
{/snippet}

{#snippet markers(volume: DetailedDataset)}
	{@const shared = usersOf(volume)}
	{#if shared.length > 0}
		<Badge
			variant="outline"
			class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
			title="Shared over iSCSI by {shared.map(userLabel).join(', ')}"
		>
			iSCSI
		</Badge>
	{/if}
	{#if propertyValue(volume, 'readonly') === 'on'}
		<Badge
			variant="outline"
			class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
		>
			Read Only
		</Badge>
	{/if}
	{#if isEncrypted(volume)}
		{@const locked = isKeyUnavailable(volume)}
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

{#snippet nameCell(volume: DetailedDataset)}
	<div class="flex min-w-0 items-center gap-2">
		<span
			class="icon-[carbon--volume-block-storage] text-muted-foreground size-4 shrink-0"
			aria-hidden="true"
		></span>
		{@render nameText(volume)}
		{@render markers(volume)}
	</div>
{/snippet}

{#snippet sizeCell(bytes: number)}
	<span class="whitespace-nowrap tabular-nums">{formatBytesBinary(bytes)}</span>
{/snippet}

{#snippet blockCell(bytes: number)}
	{#if bytes}
		<span class="whitespace-nowrap tabular-nums">{formatBytesBinary(bytes)}</span>
	{:else}
		<span class="text-muted-foreground text-xs">Not Reported</span>
	{/if}
{/snippet}

{#snippet textCell(value: string)}
	<span class="whitespace-nowrap">{value}</span>
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

{#snippet selectionControls()}
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
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, DetailedDataset>)}
	{@const volume = row.original}
	<div class="mb-3 flex min-w-0 items-center gap-2">
		<span
			class="icon-[carbon--volume-block-storage] text-muted-foreground size-4 shrink-0"
			aria-hidden="true"
		></span>
		{@render nameText(volume)}
		{@render markers(volume)}
		<div class="-my-2 -mr-2 ml-auto shrink-0">{@render rowActions(volume)}</div>
	</div>
	<dl class="grid grid-cols-2 gap-3 text-xs">
		<div>
			<dt class="text-muted-foreground">Size</dt>
			<dd class="mt-1">{@render sizeCell(volumeSize(volume))}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Used</dt>
			<dd class="mt-1">{@render sizeCell(volume.used)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Provisioning</dt>
			<dd class="mt-1">{isSparse(volume) ? 'Sparse' : 'Thick'}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Block Size</dt>
			<dd class="mt-1">{@render blockCell(blockSize(volume))}</dd>
		</div>
	</dl>
{/snippet}

<svelte:head><title>Volumes · {initial.hostname}</title></svelte:head>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[carbon--volume-block-storage] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">Volumes</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<div class="flex items-center gap-2">
			{#if canModify}
				<Button class="h-9 gap-2" onclick={() => openEditor()}>
					<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>New Volume
				</Button>
			{/if}
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				disabled={datasets.loading}
				onclick={refreshAll}
				aria-label="Refresh volumes"
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
				{volumes.length > 0
					? 'Volumes could not be refreshed. Showing the last successful list.'
					: 'Volumes could not be loaded. Please try again.'}
			</p>
			<Button variant="outline" size="sm" disabled={datasets.loading} onclick={refresh}
				>Try Again</Button
			>
		</div>
	{/if}
	{#if !failed || volumes.length > 0}
		<DataTable
			data={shown}
			{columns}
			getRowId={rowId}
			getRowLabel={(volume) => volume.name}
			globalFilterFn={(row, _columnId, value) =>
				row.original.name.toLocaleLowerCase().includes(String(value).trim().toLocaleLowerCase())}
			initialSorting={[{ id: 'name', desc: false }]}
			bind:selection
			selectable={canModify}
			selectionBlock={deleteReason}
			toolbar={selectionControls}
			pageResetKey={selectedPool}
			initialPageSize={25}
			pageSizeOptions={[25, 50, 100]}
			columnClasses={{
				name: 'min-w-64',
				size: 'pr-6 text-right',
				used: 'pr-6 text-right',
				actions: 'w-14 pr-3'
			}}
			loading={datasets.loading}
			searchPlaceholder="Search volumes…"
			emptyMessage={volumes.length > 0 ? 'No volumes in this pool.' : 'No volumes on this node.'}
			heading={pools.length > 1 ? poolFilter : undefined}
			{mobileRow}
		/>
	{/if}
</div>

{#if selected}
	<Details
		dataset={selected}
		users={usersOf(selected)}
		stale={failed}
		onClose={() => (selectedName = '')}
	/>
{/if}

{#if deleteTargets}
	<Delete
		hostname={initial.hostname}
		targets={deleteTargets}
		onClose={() => (deleteTargets = null)}
		onDone={refreshAll}
	/>
{/if}

{#if snapshotTarget}
	<Snapshot
		hostname={initial.hostname}
		dataset={snapshotTarget}
		onClose={() => (snapshotName = '')}
	/>
{/if}

{#if flashTarget}
	<FlashImage
		hostname={initial.hostname}
		volume={flashTarget}
		onClose={() => (flashName = '')}
		onDone={refresh}
	/>
{/if}

{#if editor && (!editor.name || editing)}
	{#key editor.session}
		<VolumeEditor
			hostname={initial.hostname}
			volume={editing}
			parent={defaultParent()}
			{parents}
			{names}
			users={editing ? usersOf(editing) : []}
			onClose={() => (editor = null)}
			onSaved={refreshAll}
		/>
	{/key}
{/if}
