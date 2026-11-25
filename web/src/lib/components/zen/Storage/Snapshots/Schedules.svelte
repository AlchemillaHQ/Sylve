<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Badge } from '$lib/components/ui/badge/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import type { Dataset, PeriodicSnapshot, SnapshotSummary } from '$lib/types/zfs/dataset';
	import { dateToAgo } from '$lib/utils/time';
	import { snapshotScheduleLabel } from '$lib/utils/zfs/snapshot';
	import {
		createColumnHelper,
		renderSnippet,
		type Row,
		type RowSelectionState
	} from '@tanstack/svelte-table';
	import MenuAction from '../Disks/MenuAction.svelte';
	import DeleteSchedules from './DeleteSchedules.svelte';
	import {
		nextRunLabel,
		retentionSummary,
		runTime,
		scheduleRows,
		type ScheduleRow
	} from './snapshots';

	let {
		hostname,
		jobs,
		datasets,
		snapshots,
		loading,
		failed,
		canModify,
		editing,
		onRetry,
		onChanged,
		onEdit
	}: {
		hostname: string;
		jobs: PeriodicSnapshot[];
		datasets: Pick<Dataset, 'name' | 'guid'>[];
		snapshots: SnapshotSummary[];
		loading: boolean;
		failed: boolean;
		canModify: boolean;
		editing: boolean;
		onRetry: () => void;
		onChanged: () => void;
		onEdit: (row: ScheduleRow) => void;
	} = $props();

	let rows = $derived(scheduleRows(jobs, datasets, snapshots));
	let selection = $state<RowSelectionState>({});
	let selectedRows = $derived(rows.filter((row) => selection[rowId(row)]));
	let deleteTargets = $state<ScheduleRow[] | null>(null);

	function rowId(row: ScheduleRow): string {
		return String(row.job.id);
	}

	const helper = createColumnHelper<DataTableFeatures, ScheduleRow>();
	const columns = helper.columns([
		helper.accessor((row) => row.dataset, {
			id: 'dataset',
			header: 'File System',
			sortFn: 'alphanumeric',
			cell: ({ row }) => renderSnippet(datasetCell, row.original)
		}),
		helper.accessor((row) => snapshotScheduleLabel(row.job.interval, row.job.cronExpr), {
			id: 'schedule',
			header: 'Schedule',
			enableSorting: false,
			enableGlobalFilter: false,
			cell: ({ getValue }) => renderSnippet(textCell, getValue())
		}),
		helper.accessor((row) => retentionSummary(row.job).label, {
			id: 'keeps',
			header: 'Keeps',
			enableSorting: false,
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(keepsCell, row.original)
		}),
		helper.accessor('snapshots', {
			header: 'Snapshots',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ getValue }) => renderSnippet(countCell, getValue())
		}),
		helper.accessor((row) => runTime(row.job.lastExecutedAt), {
			id: 'lastRun',
			header: 'Last Run',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(lastRunCell, row.original)
		}),
		helper.accessor((row) => runTime(row.job.nextRunAt), {
			id: 'nextRun',
			header: 'Next Run',
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(nextRunCell, row.original)
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

{#snippet datasetName(row: ScheduleRow)}
	<span class="min-w-0 break-all">
		{#if row.dataset}
			<span>{row.dataset}</span>
		{:else}
			<span class="text-muted-foreground" title={row.job.guid}>File System Not Found</span>
		{/if}
		<span class="text-muted-foreground font-mono text-xs">@{row.job.prefix}-…</span>
	</span>
	{#if row.job.recursive}
		<Badge
			variant="outline"
			class="text-muted-foreground shrink-0 px-1.5 py-0 text-[10px] font-medium"
			title="Also snapshots every file system and volume inside it"
		>
			Recursive
		</Badge>
	{/if}
{/snippet}

{#snippet datasetCell(row: ScheduleRow)}
	<div class="flex min-w-0 items-center gap-2">
		<span
			class="icon-[lucide--calendar-clock] text-muted-foreground size-4 shrink-0"
			aria-hidden="true"
		></span>
		{@render datasetName(row)}
	</div>
{/snippet}

{#snippet textCell(value: string)}
	<span class="whitespace-nowrap">{value}</span>
{/snippet}

{#snippet keepsCell(row: ScheduleRow)}
	{@const retention = retentionSummary(row.job)}
	<span class="whitespace-nowrap" title={retention.description}>{retention.label}</span>
{/snippet}

{#snippet countCell(count: number)}
	<span class="tabular-nums">{count}</span>
{/snippet}

{#snippet lastRunCell(row: ScheduleRow)}
	{#if runTime(row.job.lastExecutedAt)}
		<span class="whitespace-nowrap" title={row.job.lastExecutedAt?.toLocaleString()}>
			{dateToAgo(row.job.lastExecutedAt!)}
		</span>
	{:else}
		<span class="text-muted-foreground text-xs">Never</span>
	{/if}
{/snippet}

{#snippet nextRunCell(row: ScheduleRow)}
	{@const label = nextRunLabel(row.job)}
	<span
		class={['whitespace-nowrap', label === 'Not Reported' && 'text-muted-foreground text-xs']}
		title={row.job.nextRunAt?.toLocaleString()}
	>
		{label}
	</span>
{/snippet}

{#snippet actionsHeader(_value: undefined)}
	<span class="sr-only">Actions</span>
{/snippet}

{#snippet rowActions(row: ScheduleRow)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						aria-label="Actions for the {row.dataset || 'missing file system'} schedule"
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
					if (deleteTargets || editing) event.preventDefault();
				}}
			>
				<MenuAction
					label="Edit"
					icon="icon-[lucide--pencil]"
					blocked={row.dataset ? '' : 'Its file system no longer exists.'}
					onSelect={() => onEdit(row)}
				/>
				<DropdownMenu.Separator />
				<MenuAction
					label="Delete"
					icon="icon-[lucide--trash-2]"
					destructive
					onSelect={() => (deleteTargets = [row])}
				/>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet toolbar()}
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

{#snippet mobileRow(tableRow: Row<DataTableFeatures, ScheduleRow>)}
	{@const row = tableRow.original}
	<div class="mb-3 flex min-w-0 items-start gap-2">
		<div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
			<span
				class="icon-[lucide--calendar-clock] text-muted-foreground size-4 shrink-0"
				aria-hidden="true"
			></span>
			{@render datasetName(row)}
		</div>
		<div class="-my-2 -mr-2 shrink-0">{@render rowActions(row)}</div>
	</div>
	<dl class="grid grid-cols-2 gap-3 text-xs">
		<div>
			<dt class="text-muted-foreground">Schedule</dt>
			<dd class="mt-1">{snapshotScheduleLabel(row.job.interval, row.job.cronExpr)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Keeps</dt>
			<dd class="mt-1">{@render keepsCell(row)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Last Run</dt>
			<dd class="mt-1">{@render lastRunCell(row)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Next Run</dt>
			<dd class="mt-1">{@render nextRunCell(row)}</dd>
		</div>
	</dl>
{/snippet}

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
			{jobs.length > 0
				? 'Snapshot schedules could not be refreshed. Showing the last successful list.'
				: 'Snapshot schedules could not be loaded. Please try again.'}
		</p>
		<Button variant="outline" size="sm" disabled={loading} onclick={onRetry}>Try Again</Button>
	</div>
{/if}
{#if !failed || jobs.length > 0}
	<DataTable
		data={rows}
		{columns}
		getRowId={rowId}
		getRowLabel={(row) => `${row.dataset || 'missing file system'} schedule ${row.job.prefix}`}
		globalFilterFn={(row, _columnId, value) => {
			const query = String(value).trim().toLocaleLowerCase();
			return `${row.original.dataset} ${row.original.job.prefix}`
				.toLocaleLowerCase()
				.includes(query);
		}}
		initialSorting={[{ id: 'dataset', desc: false }]}
		bind:selection
		selectable={canModify}
		initialPageSize={25}
		pageSizeOptions={[25, 50, 100]}
		columnClasses={{
			dataset: 'min-w-64',
			snapshots: 'pr-6 text-right',
			actions: 'w-14 pr-3'
		}}
		{loading}
		searchPlaceholder="Search schedules…"
		emptyMessage="No snapshot schedules on this node."
		{toolbar}
		{mobileRow}
	/>
{/if}

{#if deleteTargets}
	<DeleteSchedules
		{hostname}
		targets={deleteTargets}
		{rows}
		{snapshots}
		onClose={() => (deleteTargets = null)}
		onDone={() => {
			selection = {};
			onChanged();
		}}
	/>
{/if}
