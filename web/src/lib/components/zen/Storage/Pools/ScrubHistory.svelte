<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import type { PoolScrubRun } from '$lib/types/zfs/pool';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';
	import { reportedNumber } from './pools';
	import {
		scanBytes,
		scanDate,
		scanDuration,
		scanTime,
		scrubDot,
		scrubLabel,
		scrubTone
	} from './scan';

	let {
		runs,
		loading = false,
		failed = false
	}: { runs: PoolScrubRun[]; loading?: boolean; failed?: boolean } = $props();
	let searchText = $derived(
		new Map(
			runs.map((run) => [
				run.id,
				[
					scanDate(run.startedAt || run.requestedAt),
					`${scrubLabel(run.source)}${run.scheduleId === null ? '' : ` #${run.scheduleId}`}`,
					scrubLabel(run.status),
					scanDuration(scanTime(run.startedAt), scanTime(run.completedAt)),
					run.errors ?? '—',
					scanBytes(run.repairedBytes),
					run.repairedBytes ?? '',
					run.error ||
						(run.examinedBytes === null
							? 'Not Reported'
							: `${scanBytes(run.examinedBytes)} scanned`),
					run.examinedBytes ?? ''
				]
					.join(' ')
					.toLocaleLowerCase()
			])
		)
	);
	const helper = createColumnHelper<DataTableFeatures, PoolScrubRun>();
	const columns = helper.columns([
		helper.accessor((run) => scanTime(run.startedAt || run.requestedAt) ?? 0, {
			id: 'date',
			header: 'Date',
			sortFn: 'basic',
			cell: ({ row }) => renderSnippet(dateCell, row.original)
		}),
		helper.accessor((run) => `${run.source} ${run.scheduleId ?? ''}`, {
			id: 'source',
			header: 'Source',
			cell: ({ row }) => renderSnippet(sourceCell, row.original)
		}),
		helper.accessor('status', {
			header: 'Result',
			cell: ({ row }) => renderSnippet(resultCell, row.original.status)
		}),
		helper.display({
			id: 'duration',
			header: 'Duration',
			cell: ({ row }) => renderSnippet(durationCell, row.original)
		}),
		helper.accessor((run) => reportedNumber(run.errors) ?? -1, {
			id: 'errors',
			header: 'Errors',
			sortFn: 'basic',
			cell: ({ row }) => renderSnippet(errorsCell, row.original.errors)
		}),
		helper.accessor((run) => reportedNumber(run.repairedBytes) ?? -1, {
			id: 'repairedBytes',
			header: 'Repaired',
			sortFn: 'basic',
			cell: ({ row }) => renderSnippet(bytesCell, row.original.repairedBytes)
		}),
		helper.accessor((run) => run.error || scanBytes(run.examinedBytes), {
			id: 'details',
			header: 'Details',
			enableSorting: false,
			cell: ({ row }) => renderSnippet(detailsCell, row.original)
		})
	]);
</script>

{#snippet dateCell(run: PoolScrubRun)}
	<span class="whitespace-nowrap tabular-nums">{scanDate(run.startedAt || run.requestedAt)}</span>
{/snippet}
{#snippet sourceCell(run: PoolScrubRun)}
	<span>{scrubLabel(run.source)}{run.scheduleId === null ? '' : ` #${run.scheduleId}`}</span>
{/snippet}
{#snippet resultCell(value: string)}
	<span class="inline-flex items-center gap-1.5 whitespace-nowrap">
		<span class="size-1.5 shrink-0 rounded-full {scrubDot[scrubTone(value)]}" aria-hidden="true"
		></span>{scrubLabel(value)}
	</span>
{/snippet}
{#snippet durationCell(run: PoolScrubRun)}
	<span class="whitespace-nowrap tabular-nums"
		>{scanDuration(scanTime(run.startedAt), scanTime(run.completedAt))}</span
	>
{/snippet}
{#snippet errorsCell(value: string | null)}
	<span class="tabular-nums" class:text-destructive={value !== null && Number(value) > 0}
		>{value ?? '—'}</span
	>
{/snippet}
{#snippet bytesCell(value: string | null)}
	<span class="whitespace-nowrap tabular-nums">{scanBytes(value)}</span>
{/snippet}
{#snippet detailsCell(run: PoolScrubRun)}
	<span class="text-muted-foreground text-xs break-words">
		{run.error ||
			(run.examinedBytes === null ? 'Not Reported' : `${scanBytes(run.examinedBytes)} scanned`)}
	</span>
{/snippet}
{#snippet mobileRow(row: Row<DataTableFeatures, PoolScrubRun>)}
	<div class="mb-3 flex flex-wrap items-center justify-between gap-2 text-sm">
		{@render dateCell(row.original)}{@render resultCell(row.original.status)}
	</div>
	<dl class="grid grid-cols-2 gap-3 text-xs">
		<div>
			<dt class="text-muted-foreground">Source</dt>
			<dd class="mt-1">{@render sourceCell(row.original)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Duration</dt>
			<dd class="mt-1">{@render durationCell(row.original)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Errors</dt>
			<dd class="mt-1">{@render errorsCell(row.original.errors)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Repaired</dt>
			<dd class="mt-1">{@render bytesCell(row.original.repairedBytes)}</dd>
		</div>
	</dl>
	<p class="mt-3">{@render detailsCell(row.original)}</p>
{/snippet}

<p class="text-muted-foreground mb-3 text-xs leading-5">
	Sylve-tracked scrub runs and skipped scheduled occurrences. Dates use your browser’s local time.
</p>
<DataTable
	data={runs}
	{columns}
	{loading}
	getRowId={(run) => String(run.id)}
	getRowLabel={(run) => `Scrub run ${run.id}`}
	globalFilterFn={(row, _columnId, value) =>
		(searchText.get(row.original.id) ?? '').includes(String(value).trim().toLocaleLowerCase())}
	initialSorting={[{ id: 'date', desc: true }]}
	initialPageSize={5}
	pageSizeOptions={[5, 10, 20, 50]}
	searchPlaceholder="Search scrub history…"
	emptyMessage={failed
		? 'Scrub history is unavailable.'
		: 'No scrub history has been recorded for this pool.'}
	{mobileRow}
/>
