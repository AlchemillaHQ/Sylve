<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import {
		getDetailedDatasetsResult,
		getPeriodicSnapshotsResult,
		getSnapshotSummariesResult
	} from '$lib/api/zfs/datasets';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import {
		GZFSDatasetTypeSchema,
		type PeriodicSnapshot,
		type SnapshotSummary
	} from '$lib/types/zfs/dataset';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { APIRequestError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { dateToAgo } from '$lib/utils/time';
	import {
		createColumnHelper,
		renderSnippet,
		type Row,
		type RowSelectionState
	} from '@tanstack/svelte-table';
	import { PersistedState, resource, useInterval, watch } from 'runed';
	import { onDestroy } from 'svelte';
	import MenuAction from '../Disks/MenuAction.svelte';
	import Snapshot from '../FileSystems/Snapshot.svelte';
	import Delete from './Delete.svelte';
	import Rollback from './Rollback.svelte';
	import ScheduleEditor from './ScheduleEditor.svelte';
	import Schedules from './Schedules.svelte';
	import {
		countCategories,
		createdTime,
		defaultCategories,
		indexByName,
		isVisibleSnapshot,
		pickableDatasets,
		rollbackBlock,
		sameNamedInside,
		scheduledMatcher,
		snapshotCategories,
		snapshotCategory,
		snapshotDeleteBlock,
		sylveSnapshotKind,
		systemMountpoints,
		toggledCategories,
		type SnapshotCategory,
		type SnapshotsData
	} from './snapshots';

	let { data }: { data: SnapshotsData } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const lifetime = new AbortController();
	let initialFailure = $state(isAPIResponse(initial.snapshots));
	const snapshots = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getSnapshotSummariesResult(hostname, requestSignal);
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialFailure = false;
			void updateCache('zfs-snapshot-summaries', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.snapshots) ? initial.snapshots : [],
			lazy: initial.fresh ?? false
		}
	);
	let initialJobsFailure = $state(isAPIResponse(initial.periodicSnapshots));
	const jobs = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await getPeriodicSnapshotsResult(hostname, requestSignal);
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialJobsFailure = false;
			void updateCache('zfs-periodic-snapshots', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.periodicSnapshots) ? initial.periodicSnapshots : [],
			lazy: initial.fresh ?? false
		}
	);

	const datasets = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const results = await Promise.all(
				[GZFSDatasetTypeSchema.enum.FILESYSTEM, GZFSDatasetTypeSchema.enum.VOLUME].map((type) =>
					getDetailedDatasetsResult(type, hostname, requestSignal)
				)
			);
			requestSignal.throwIfAborted();
			return results.flatMap((result) => {
				if (isAPIResponse(result)) throw new APIRequestError(result);
				return result;
			});
		},
		{ initialValue: [] }
	);

	const pool = new PersistedState(`sylve-zen-snapshots-pool:${initial.hostname}`, 'all');
	const categories = new PersistedState<SnapshotCategory[]>(
		`sylve-zen-snapshots-show:${initial.hostname}`,
		defaultCategories
	);

	let canModify = $derived(allowed('Sys.Modify', nodePath(initial.hostname)));
	let failed = $derived(initialFailure || Boolean(snapshots.error));
	let jobsFailed = $derived(initialJobsFailure || Boolean(jobs.error));
	let tab = $state('snapshots');
	let index = $derived(indexByName(snapshots.current));
	let system = $derived(systemMountpoints(datasets.current));
	let isScheduled = $derived(scheduledMatcher(jobs.current));
	let visible = $derived(snapshots.current.filter(isVisibleSnapshot));
	let pools = $derived([...new Set(visible.map((snapshot) => snapshot.pool))].sort());
	let selectedPool = $derived(
		pool.current === 'all' || pools.includes(pool.current) ? pool.current : 'all'
	);
	let poolSnapshots = $derived(
		selectedPool === 'all' ? visible : visible.filter((snapshot) => snapshot.pool === selectedPool)
	);
	let categoryOf = $derived(
		new Map(
			poolSnapshots.map((snapshot) => [snapshot.name, snapshotCategory(snapshot, isScheduled)])
		)
	);
	let categoryCounts = $derived(countCategories(categoryOf.values()));
	let shown = $derived(
		poolSnapshots.filter((snapshot) => {
			const category = categoryOf.get(snapshot.name);
			return category !== undefined && categories.current.includes(category);
		})
	);
	let showLabel = $derived.by(() => {
		const chosen = snapshotCategories.filter((category) =>
			categories.current.includes(category.value)
		);
		if (chosen.length === 0) return 'Nothing';
		if (chosen.length === snapshotCategories.length) return 'All Snapshots';
		if (chosen.length <= 2) return chosen.map((category) => category.label).join(', ');
		return `${chosen.length} Kinds`;
	});
	let selection = $state<RowSelectionState>({});
	let selectedSnapshots = $derived(shown.filter((snapshot) => selection[snapshot.name]));
	let deleteTargets = $state<SnapshotSummary[] | null>(null);
	let rollbackTarget = $state<SnapshotSummary | null>(null);
	let taking = $state(false);
	let scheduleEditor = $state<{ job?: PeriodicSnapshot; datasetName?: string } | null>(null);
	let pickable = $derived(
		datasets.current.length === 0 && (datasets.loading || datasets.error)
			? null
			: pickableDatasets(datasets.current)
	);
	let pickableFailed = $derived(Boolean(datasets.error) && datasets.current.length === 0);

	function toggleCategory(category: SnapshotCategory, checked: boolean) {
		categories.current = toggledCategories(categories.current, category, checked);
	}

	function refresh() {
		void snapshots.refetch();
		void jobs.refetch();
	}

	function refreshAll() {
		refresh();
		void datasets.refetch();
	}

	const interval = useInterval(() => (storage.idle ? 60_000 : 30_000), {
		callback: () => {
			if (storage.visible && !snapshots.loading) refresh();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([isVisible, idle]) => {
			if (isVisible && !idle && !snapshots.loading) refresh();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	const helper = createColumnHelper<DataTableFeatures, SnapshotSummary>();
	const columns = helper.columns([
		helper.accessor('name', {
			header: 'Name',
			sortFn: 'alphanumeric',
			cell: ({ row }) => renderSnippet(nameCell, row.original)
		}),
		helper.accessor((snapshot) => createdTime(snapshot), {
			id: 'created',
			header: 'Created',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(createdCell, row.original)
		}),
		helper.accessor('used', {
			header: 'Used',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(sizeCell, row.original.used)
		}),
		helper.accessor('referenced', {
			header: 'Referenced',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(sizeCell, row.original.referenced)
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

{#snippet nameText(snapshot: SnapshotSummary)}
	<span class="min-w-0 break-all" title={snapshot.name}>
		<span class="text-muted-foreground">{snapshot.dataset}@</span><span class="font-medium"
			>{snapshot.snapshot}</span
		>
	</span>
{/snippet}

{#snippet markers(snapshot: SnapshotSummary)}
	{@const kind = sylveSnapshotKind(snapshot)}
	{@const inside = sameNamedInside(snapshot, index).length}
	{#if kind}
		<Badge
			variant="outline"
			class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
			title="Managed by Sylve"
		>
			{kind}
		</Badge>
	{:else if categoryOf.get(snapshot.name) === 'scheduled'}
		<Badge
			variant="outline"
			class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
			title="Taken by a snapshot schedule"
		>
			Scheduled
		</Badge>
	{/if}
	{#if inside > 0}
		<span
			class="text-muted-foreground shrink-0 text-xs whitespace-nowrap"
			title="{inside} {inside === 1
				? 'snapshot'
				: 'snapshots'} with the same name inside {snapshot.dataset}"
		>
			+{inside} inside
		</span>
	{/if}
	{#if snapshot.clones.length > 0}
		<span
			class="icon-[lucide--git-fork] text-muted-foreground size-3.5 shrink-0"
			title="Cloned to {snapshot.clones.join(', ')}"
			aria-hidden="true"
		></span>
		<span class="sr-only">Cloned to {snapshot.clones.join(', ')}</span>
	{/if}
	{#if snapshot.holds > 0}
		<span
			class="icon-[lucide--lock-keyhole] text-muted-foreground size-3.5 shrink-0"
			title="{snapshot.holds} {snapshot.holds === 1 ? 'hold' : 'holds'}"
			aria-hidden="true"
		></span>
		<span class="sr-only">{snapshot.holds} {snapshot.holds === 1 ? 'hold' : 'holds'}</span>
	{/if}
{/snippet}

{#snippet nameCell(snapshot: SnapshotSummary)}
	<div class="flex min-w-0 items-center gap-2">
		<span
			class="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots] text-muted-foreground size-4 shrink-0"
			aria-hidden="true"
		></span>
		{@render nameText(snapshot)}
		{@render markers(snapshot)}
	</div>
{/snippet}

{#snippet createdCell(snapshot: SnapshotSummary)}
	{#if createdTime(snapshot)}
		<span class="whitespace-nowrap" title={snapshot.createdAt.toLocaleString()}>
			{dateToAgo(snapshot.createdAt)}
		</span>
	{:else}
		<span class="text-muted-foreground text-xs">Not Reported</span>
	{/if}
{/snippet}

{#snippet sizeCell(bytes: number)}
	<span class="whitespace-nowrap tabular-nums">{formatBytesBinary(bytes)}</span>
{/snippet}

{#snippet actionsHeader(_value: undefined)}
	<span class="sr-only">Actions</span>
{/snippet}

{#snippet rowActions(snapshot: SnapshotSummary)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						aria-label="Actions for {snapshot.name}"
						disabled={!canModify}
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
					if (deleteTargets || rollbackTarget) event.preventDefault();
				}}
			>
				<MenuAction
					label="Roll Back"
					icon="icon-[lucide--history]"
					blocked={rollbackBlock(snapshot, snapshots.current, system)}
					onSelect={() => (rollbackTarget = snapshot)}
				/>
				<DropdownMenu.Separator />
				<MenuAction
					label="Delete"
					icon="icon-[lucide--trash-2]"
					blocked={snapshotDeleteBlock(snapshot)}
					destructive
					onSelect={() => (deleteTargets = [snapshot])}
				/>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
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

{#snippet toolbar()}
	{#if canModify && selectedSnapshots.length > 0}
		<Button
			variant="outline"
			size="sm"
			class="text-destructive h-9 gap-2"
			onclick={() => (deleteTargets = selectedSnapshots)}
		>
			<span class="icon-[lucide--trash-2] size-4" aria-hidden="true"></span>
			Delete Selected ({selectedSnapshots.length})
		</Button>
	{/if}
	<DropdownMenu.Root>
		<DropdownMenu.Trigger>
			{#snippet child({ props })}
				<Button
					{...props}
					variant="outline"
					class="bg-background h-9 gap-2 font-normal dark:hover:bg-input/30"
					aria-label="Show snapshot kinds: {showLabel}"
				>
					<span class="icon-[lucide--list-filter] text-muted-foreground size-4" aria-hidden="true"
					></span>
					<span class="max-w-40 truncate">{showLabel}</span>
					<span class="icon-[lucide--chevron-down] size-4 opacity-50" aria-hidden="true"></span>
				</Button>
			{/snippet}
		</DropdownMenu.Trigger>
		<DropdownMenu.Content align="end" class="w-56">
			<DropdownMenu.Label>Show</DropdownMenu.Label>
			<DropdownMenu.Separator />
			{#each snapshotCategories as category (category.value)}
				<DropdownMenu.CheckboxItem
					class="cursor-pointer"
					checked={categories.current.includes(category.value)}
					closeOnSelect={false}
					onCheckedChange={(checked) => toggleCategory(category.value, checked)}
				>
					{category.label}
					<span class="text-muted-foreground ml-auto text-xs tabular-nums">
						{categoryCounts.get(category.value) ?? 0}
					</span>
				</DropdownMenu.CheckboxItem>
			{/each}
		</DropdownMenu.Content>
	</DropdownMenu.Root>
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, SnapshotSummary>)}
	{@const snapshot = row.original}
	<div class="mb-3 flex min-w-0 items-start gap-2">
		<div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
			<span
				class="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots] text-muted-foreground size-4 shrink-0"
				aria-hidden="true"
			></span>
			{@render nameText(snapshot)}
			{@render markers(snapshot)}
		</div>
		<div class="-my-2 -mr-2 shrink-0">{@render rowActions(snapshot)}</div>
	</div>
	<dl class="grid grid-cols-3 gap-3 text-xs">
		<div>
			<dt class="text-muted-foreground">Created</dt>
			<dd class="mt-1">{@render createdCell(snapshot)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Used</dt>
			<dd class="mt-1">{@render sizeCell(snapshot.used)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Referenced</dt>
			<dd class="mt-1">{@render sizeCell(snapshot.referenced)}</dd>
		</div>
	</dl>
{/snippet}

<svelte:head><title>Snapshots · {initial.hostname}</title></svelte:head>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"
				><span class="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">Snapshots</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<div class="flex items-center gap-2">
			{#if canModify && tab === 'snapshots'}
				<Button class="h-9 gap-2" onclick={() => (taking = true)}>
					<span class="icon-[lucide--camera] size-4" aria-hidden="true"></span>Take Snapshot
				</Button>
			{:else if canModify}
				<Button class="h-9 gap-2" onclick={() => (scheduleEditor = {})}>
					<span class="icon-[lucide--calendar-plus] size-4" aria-hidden="true"></span>New Schedule
				</Button>
			{/if}
			<Button
				variant="outline"
				size="icon"
				class="size-9"
				disabled={snapshots.loading || jobs.loading}
				onclick={refreshAll}
				aria-label="Refresh snapshots and schedules"
			>
				<span
					class="icon-[lucide--refresh-cw] size-4"
					class:animate-spin={snapshots.loading || jobs.loading}
					aria-hidden="true"
				></span>
			</Button>
		</div>
	</header>
	<Tabs.Root bind:value={tab} class="min-w-0">
		<Tabs.List class="mb-4 w-full">
			<Tabs.Trigger value="snapshots" class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm">
				<span
					class="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots] size-3.5"
					aria-hidden="true"
				></span>Snapshots
			</Tabs.Trigger>
			<Tabs.Trigger value="schedules" class="gap-1 px-1 text-xs sm:gap-1.5 sm:px-2 sm:text-sm">
				<span class="icon-[lucide--calendar-clock] size-3.5" aria-hidden="true"></span>Schedules
				{#if jobs.current.length > 0}
					<span class="text-muted-foreground tabular-nums">{jobs.current.length}</span>
				{/if}
			</Tabs.Trigger>
		</Tabs.List>
		<Tabs.Content value="snapshots" class="mt-0">
			{@render snapshotsTab()}
		</Tabs.Content>
		<Tabs.Content value="schedules" class="mt-0">
			<Schedules
				hostname={initial.hostname}
				jobs={jobs.current}
				datasets={datasets.current}
				snapshots={snapshots.current}
				loading={jobs.loading}
				failed={jobsFailed}
				{canModify}
				editing={scheduleEditor !== null}
				onRetry={refresh}
				onChanged={refresh}
				onEdit={(row) => (scheduleEditor = { job: row.job, datasetName: row.dataset })}
			/>
		</Tabs.Content>
	</Tabs.Root>
</div>

{#snippet snapshotsTab()}
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
				{snapshots.current.length > 0
					? 'Snapshots could not be refreshed. Showing the last successful list.'
					: 'Snapshots could not be loaded. Please try again.'}
			</p>
			<Button variant="outline" size="sm" disabled={snapshots.loading} onclick={refresh}
				>Try Again</Button
			>
		</div>
	{/if}
	{#if !failed || snapshots.current.length > 0}
		<DataTable
			data={shown}
			{columns}
			getRowId={(snapshot) => snapshot.name}
			getRowLabel={(snapshot) => snapshot.name}
			globalFilterFn={(row, _columnId, value) =>
				row.original.name.toLocaleLowerCase().includes(String(value).trim().toLocaleLowerCase())}
			initialSorting={[{ id: 'created', desc: true }]}
			pageResetKey={`${selectedPool}:${categories.current.join(',')}`}
			bind:selection
			selectable={canModify}
			selectionBlock={snapshotDeleteBlock}
			initialPageSize={25}
			pageSizeOptions={[25, 50, 100]}
			columnClasses={{
				name: 'min-w-64',
				created: 'whitespace-nowrap',
				used: 'pr-6 text-right',
				referenced: 'pr-6 text-right',
				actions: 'w-14 pr-3'
			}}
			loading={snapshots.loading}
			searchPlaceholder="Search snapshots…"
			emptyMessage={poolSnapshots.length > 0
				? 'No snapshots match the Show filter.'
				: 'No snapshots on this node.'}
			heading={pools.length > 1 ? poolFilter : undefined}
			{toolbar}
			{mobileRow}
		/>
	{/if}
{/snippet}

{#if deleteTargets}
	<Delete
		hostname={initial.hostname}
		targets={deleteTargets}
		all={snapshots.current}
		onClose={() => (deleteTargets = null)}
		onDone={refresh}
	/>
{/if}

{#if rollbackTarget}
	<Rollback
		hostname={initial.hostname}
		target={rollbackTarget}
		all={snapshots.current}
		onClose={() => (rollbackTarget = null)}
		onDone={refresh}
	/>
{/if}

{#if taking}
	<Snapshot
		hostname={initial.hostname}
		choices={pickable}
		choicesFailed={pickableFailed}
		onRetryChoices={() => datasets.refetch()}
		onClose={() => (taking = false)}
		onDone={refresh}
	/>
{/if}

{#if scheduleEditor}
	<ScheduleEditor
		hostname={initial.hostname}
		job={scheduleEditor.job}
		datasetName={scheduleEditor.datasetName}
		choices={pickable}
		choicesFailed={pickableFailed}
		onRetryChoices={() => datasets.refetch()}
		jobs={jobs.current}
		onClose={() => (scheduleEditor = null)}
		onDone={refresh}
	/>
{/if}
