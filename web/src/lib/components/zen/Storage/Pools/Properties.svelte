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
	import type { Zpool } from '$lib/types/zfs/pool';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';

	let { pool }: { pool: Zpool } = $props();
	type Property = { name: string; value: string; source: string };
	let properties = $derived(
		Object.entries(pool.properties).map(([name, property]) => ({
			name,
			value: property.value,
			source: [property.source.type || 'Not Reported', property.source.data]
				.filter(Boolean)
				.join(' · ')
		}))
	);
	const helper = createColumnHelper<DataTableFeatures, Property>();
	const columns = helper.columns([
		helper.accessor('name', {
			header: 'Property',
			cell: ({ row }) => renderSnippet(nameCell, row.original.name)
		}),
		helper.accessor('value', {
			header: 'Value',
			cell: ({ row }) => renderSnippet(valueCell, row.original.value)
		}),
		helper.accessor('source', {
			header: 'Source',
			cell: ({ row }) => renderSnippet(sourceCell, row.original.source)
		})
	]);
</script>

{#snippet nameCell(name: string)}<span class="font-mono break-all">{name}</span>{/snippet}
{#snippet valueCell(value: string)}
	<span class="whitespace-pre-wrap break-all">{value || '—'}</span>
{/snippet}
{#snippet sourceCell(source: string)}
	<span class="text-muted-foreground break-all">{source}</span>
{/snippet}
{#snippet mobileRow(row: Row<DataTableFeatures, Property>)}
	<div class="mb-2 font-medium">{@render nameCell(row.original.name)}</div>
	<dl class="space-y-2 text-xs">
		<div>
			<dt class="text-muted-foreground">Value</dt>
			<dd class="mt-1">{@render valueCell(row.original.value)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Source</dt>
			<dd class="mt-1">{@render sourceCell(row.original.source)}</dd>
		</div>
	</dl>
{/snippet}

{#snippet heading()}
	<div class="flex min-h-9 items-center gap-2 px-2 text-base leading-6">
		<span
			class="bg-zen-highlight text-zen-highlight-foreground flex size-7 shrink-0 items-center justify-center rounded-md"
			aria-hidden="true"
		>
			<span class="icon-[carbon--data-table] size-5"></span>
		</span>
		<h2 class="font-bold">Pool Properties</h2>
	</div>
{/snippet}

<section class="bg-card min-w-0 rounded-lg border px-4 pt-3.5 pb-4">
	<DataTable
		data={properties}
		{columns}
		{heading}
		preserveHeightOnFilter
		columnWidths={{ name: '30%', value: '45%', source: '25%' }}
		getRowId={(property) => property.name}
		getRowLabel={(property) => property.name}
		initialSorting={[{ id: 'name', desc: false }]}
		searchPlaceholder="Search properties…"
		emptyMessage="No properties have been reported."
		{mobileRow}
	/>
</section>
