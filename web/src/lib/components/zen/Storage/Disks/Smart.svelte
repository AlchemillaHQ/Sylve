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
	import type { Disk } from '$lib/types/disk/disk';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';
	import {
		ataAttributeRows,
		diskHealth,
		diskWearout,
		formatCount,
		formatPowerOn,
		isNVMeSmart,
		nvmeHealthItems,
		wearoutApplies,
		type AtaAttributeRow,
		type Attention
	} from './disks';

	let {
		disk,
		sample,
		readingSmart,
		smartFailed,
		smartReadAt
	}: {
		disk: Disk;
		sample?: Disk;
		readingSmart: boolean;
		smartFailed: boolean;
		smartReadAt: number | null;
	} = $props();
	let data = $derived(sample?.smartData);
	let health = $derived(diskHealth(disk, sample, readingSmart));
	let nvme = $derived(data && isNVMeSmart(data) ? data : undefined);
	let ata = $derived(data && !isNVMeSmart(data) ? data : undefined);
	let rows = $derived(ata ? ataAttributeRows(ata) : []);
	let items = $derived(nvme ? nvmeHealthItems(nvme) : []);
	let normalized = $derived(ata?.device.protocol.toUpperCase() !== 'SCSI');
	let attention = $derived([...rows, ...items].filter((entry) => entry.attention).length);
	const attributeSearch = (
		row: Row<DataTableFeatures, AtaAttributeRow>,
		_columnId: string,
		value: unknown
	) =>
		`${row.original.id} ${row.original.name}`
			.toLocaleLowerCase()
			.includes(String(value).trim().toLocaleLowerCase());
	let wearout = $derived(diskWearout(disk, sample));
	let metrics = $derived(
		data
			? [
					{
						label: 'Temperature',
						value: data.temperature > 0 ? `${data.temperature} °C` : 'Not Reported'
					},
					{
						label: 'Power On',
						value: formatPowerOn(nvme?.power_on_hours_exact ?? '', data.power_on_hours)
					},
					{
						label: 'Power Cycles',
						value: formatCount(nvme?.power_cycle_count_exact ?? '', data.power_cycle_count)
					},
					...(wearoutApplies(disk) ? [{ label: 'Wearout', value: wearout }] : []),
					...(ata?.firmware ? [{ label: 'Firmware', value: ata.firmware }] : [])
				]
			: []
	);
	const tone: Record<Attention, { text: string; row: string; icon: string }> = {
		failing: {
			text: 'text-red-700 dark:text-red-400',
			row: 'bg-red-500/5 hover:bg-red-500/10',
			icon: 'icon-[lucide--circle-alert]'
		},
		warning: {
			text: 'text-amber-700 dark:text-amber-400',
			row: 'bg-amber-500/5 hover:bg-amber-500/10',
			icon: 'icon-[lucide--triangle-alert]'
		}
	};
	const helper = createColumnHelper<DataTableFeatures, AtaAttributeRow>();
	const normalizedColumn = (id: 'value' | 'worst' | 'threshold', header: string) =>
		helper.accessor((row) => row[id] ?? -1, {
			id,
			header,
			sortFn: 'basic',
			enableGlobalFilter: false,
			cell: ({ row }) => renderSnippet(numberCell, row.original[id])
		});
	let columns = $derived(
		helper.columns([
			helper.display({
				id: 'status',
				header: () => renderSnippet(statusHeader, undefined),
				cell: ({ row }) => renderSnippet(statusCell, row.original),
				enableSorting: false,
				enableGlobalFilter: false
			}),
			helper.accessor('id', {
				header: 'ID',
				sortFn: 'basic',
				cell: ({ row }) => renderSnippet(idCell, row.original.id)
			}),
			helper.accessor('name', {
				header: 'Attribute',
				cell: ({ row }) => renderSnippet(nameCell, row.original)
			}),
			...(normalized
				? [
						normalizedColumn('value', 'Value'),
						normalizedColumn('worst', 'Worst'),
						normalizedColumn('threshold', 'Threshold')
					]
				: []),
			helper.accessor('rawValue', {
				id: 'raw',
				header: 'Raw',
				sortFn: 'basic',
				enableGlobalFilter: false,
				cell: ({ row }) => renderSnippet(rawCell, row.original.raw)
			})
		])
	);
</script>

{#snippet flag(level: Attention)}
	<span
		class="{tone[level].icon} {tone[level].text} inline-block size-3.5 shrink-0 align-[-0.125em]"
		aria-hidden="true"
	></span>
{/snippet}

{#snippet statusHeader()}
	<span class="sr-only">Status</span>
{/snippet}

{#snippet statusCell(row: AtaAttributeRow)}
	{#if row.attention}
		<span title={row.reason}>{@render flag(row.attention)}</span><span class="sr-only"
			>{row.reason}</span
		>
	{/if}
{/snippet}

{#snippet idCell(id: number)}
	<span class="text-muted-foreground tabular-nums">{id}</span>
{/snippet}

{#snippet nameCell(row: AtaAttributeRow)}
	<span class={row.attention ? tone[row.attention].text : ''} title={row.reason}>{row.name}</span>
{/snippet}

{#snippet numberCell(value: number | undefined)}
	<span class="tabular-nums">{value ?? '—'}</span>
{/snippet}

{#snippet rawCell(raw: string)}
	<span class="tabular-nums">{raw}</span>
{/snippet}

{#snippet attributeCard(row: Row<DataTableFeatures, AtaAttributeRow>)}
	{@const attribute = row.original}
	<p class="flex items-baseline gap-2 text-sm font-medium">
		<span class="text-muted-foreground text-xs font-normal tabular-nums">{attribute.id}</span>
		<span class="min-w-0 break-words {attribute.attention ? tone[attribute.attention].text : ''}"
			>{attribute.name}</span
		>
	</p>
	{#if attribute.attention}
		<p class="mt-1 text-xs {tone[attribute.attention].text}">
			<span class="mr-1">{@render flag(attribute.attention)}</span>{attribute.reason}
		</p>
	{/if}
	<p class="text-muted-foreground mt-1.5 text-xs leading-5 break-words">
		{#if normalized}
			Value <span class="text-foreground tabular-nums">{attribute.value ?? '—'}</span> · Worst
			<span class="text-foreground tabular-nums">{attribute.worst ?? '—'}</span> · Threshold
			<span class="text-foreground tabular-nums">{attribute.threshold ?? '—'}</span> ·
		{/if}
		Raw <span class="text-foreground tabular-nums">{attribute.raw}</span>
	</p>
{/snippet}

<div class="space-y-4">
	{#if data}
		<section class="rounded-md border" aria-label="S.M.A.R.T. summary">
			<div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-b px-4 py-3">
				<p class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm font-medium">
					<span
						class="size-4 shrink-0 {health === 'Failed'
							? 'icon-[lucide--circle-alert] text-red-600 dark:text-red-400'
							: health === 'Passed'
								? 'icon-[lucide--circle-check] text-emerald-600 dark:text-emerald-400'
								: 'icon-[lucide--info] text-muted-foreground'}"
						aria-hidden="true"
					></span>Health: {health}
					{#if attention > 0}
						<span class="font-normal text-amber-700 dark:text-amber-400">
							· {attention}
							{attention === 1 ? 'item needs' : 'items need'} attention
						</span>
					{/if}
				</p>
				{#if smartReadAt}
					<p class="text-muted-foreground text-xs leading-5" role="status">
						Read {new Date(smartReadAt).toLocaleString()}{readingSmart ? ' · Refreshing…' : ''}
						{#if smartFailed}· Latest refresh failed, showing this sample{/if}
					</p>
				{/if}
			</div>
			<dl class="grid grid-cols-[repeat(auto-fit,minmax(8.5rem,1fr))] gap-4 p-4 text-sm">
				{#each metrics as metric (metric.label)}
					<div class="min-w-0">
						<dt class="text-muted-foreground text-xs">{metric.label}</dt>
						<dd class="mt-1 break-words tabular-nums">{metric.value}</dd>
					</div>
				{/each}
			</dl>
		</section>

		{#if ata}
			{#if rows.length > 0}
				<section aria-label="S.M.A.R.T. attributes">
					<DataTable
						data={rows}
						{columns}
						getRowId={(row) => row.key}
						getRowLabel={(row) => `${row.id} ${row.name}`}
						getRowClass={(row) => (row.attention ? tone[row.attention].row : undefined)}
						globalFilterFn={attributeSearch}
						initialPageSize={5}
						pageSizeOptions={[5, 10, 25, 50]}
						searchPlaceholder="Search attributes by ID or name…"
						fullWidthSearch
						emptyMessage="No attributes were reported."
						columnWidths={{
							status: '2.25rem',
							id: '4.5rem',
							value: '6rem',
							worst: '6rem',
							threshold: '7.5rem',
							raw: '11rem'
						}}
						columnClasses={{
							status: 'pr-0',
							id: 'text-right',
							value: 'text-right',
							worst: 'text-right',
							threshold: 'text-right'
						}}
						mobileRow={attributeCard}
					/>
				</section>
			{:else}
				<p class="text-muted-foreground text-sm">
					No additional S.M.A.R.T. attributes were reported.
				</p>
			{/if}
		{:else if nvme}
			<section class="space-y-2" aria-label="NVMe health log">
				<h3 class="text-sm font-medium">Health Log</h3>
				<dl class="grid gap-x-6 gap-y-4 rounded-md border p-4 text-sm sm:grid-cols-2">
					{#each items as item (item.label)}
						<div class="min-w-0">
							<dt class="text-muted-foreground text-xs">{item.label}</dt>
							<dd
								class="mt-1 break-words tabular-nums {item.attention
									? tone[item.attention].text
									: ''}"
							>
								{#if item.attention}<span class="mr-1.5">{@render flag(item.attention)}</span
									>{/if}{item.value}
							</dd>
						</div>
					{/each}
				</dl>
			</section>
		{/if}
	{:else}
		<div
			class="text-muted-foreground rounded-md border border-dashed px-4 py-6 text-center text-sm leading-6"
			role="status"
		>
			{#if disk.type === 'Virtual'}
				S.M.A.R.T. is not available for virtual disks.
			{:else if sample}
				This disk did not report S.M.A.R.T. data. It may not be supported by the device or
				controller.
			{:else if readingSmart}
				<span class="inline-flex items-center gap-2">
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
					></span>Reading S.M.A.R.T. data…
				</span>
			{:else if smartFailed}
				S.M.A.R.T. data could not be read. Use the refresh button on the disk list to try again.
			{:else}
				S.M.A.R.T. data has not been read yet.
			{/if}
		</div>
	{/if}
</div>
