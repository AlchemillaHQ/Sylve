<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';
	import type { Snippet } from 'svelte';
	import {
		compressRatioLabel,
		isEncrypted,
		isKeyUnavailable,
		isPoolRoot,
		propertyRows,
		propertyValue,
		sizeLabel,
		switchLabel,
		type FileSystemRow,
		type PropertyRow
	} from './filesystems';
	import { compressionLabel, encryptionLabel } from './form';
	import Usage from './Usage.svelte';

	let { row, stale, onClose }: { row: FileSystemRow; stale: boolean; onClose: () => void } =
		$props();
	const id = $props.id();
	let tab = $state('overview');
	let dataset = $derived(row.dataset);

	type Item = { label: string; value: string; hint?: string; mono?: boolean; warning?: boolean };

	let mountPoint = $derived.by(() => {
		const point = dataset.mountpoint.trim();
		if (point === 'legacy') return 'Legacy';
		return point.startsWith('/') ? point : 'None';
	});

	let space = $derived<Item[]>([
		{ label: 'Available', value: formatBytesBinary(dataset.available) },
		{
			label: 'Referenced',
			value: formatBytesBinary(dataset.referenced),
			hint: 'Data this file system holds itself, without its child file systems. Some of it may be shared with snapshots.'
		},
		{ label: 'Quota', value: sizeLabel(propertyValue(dataset, 'quota'), 'None') },
		{
			label: 'Logical Used',
			value: sizeLabel(propertyValue(dataset, 'logicalused')),
			hint: 'How much space the data would take without compression.'
		},
		{
			label: 'Compression',
			value: compressionLabel(propertyValue(dataset, 'compression') ?? '') || 'Not Reported'
		},
		{
			label: 'Compression Ratio',
			value: compressRatioLabel(dataset),
			hint: 'Logical size divided by the space actually used on disk.'
		}
	]);

	let status = $derived.by<Item[]>(() => {
		const encrypted = isEncrypted(dataset);
		const root = propertyValue(dataset, 'encryptionroot');
		const origin = propertyValue(dataset, 'origin');
		return [
			{ label: 'Pool', value: dataset.pool },
			{ label: 'Mount Point', value: mountPoint, mono: mountPoint.startsWith('/') },
			{ label: 'Mounted', value: switchLabel(propertyValue(dataset, 'mounted')) },
			{ label: 'Read Only', value: switchLabel(propertyValue(dataset, 'readonly')) },
			{
				label: 'Encryption',
				value: encryptionLabel(encrypted ? (propertyValue(dataset, 'encryption') ?? 'on') : 'off')
			},
			...(encrypted
				? [
						{
							label: 'Encryption Key',
							value: isKeyUnavailable(dataset) ? 'Not Loaded' : 'Loaded',
							warning: isKeyUnavailable(dataset),
							hint: "Without its key, this file system's data can't be read and it can't be mounted."
						},
						{ label: 'Key Format', value: propertyValue(dataset, 'keyformat') ?? 'Not Reported' },
						{
							label: 'Encryption Root',
							value: !root ? 'Not Reported' : root === dataset.name ? 'This File System' : root,
							hint: 'The file system whose key unlocks this one.'
						}
					]
				: []),
			{
				label: 'Child File Systems',
				value:
					row.children === 0
						? 'None'
						: row.descendants > row.children
							? `${row.children} (${row.descendants} including nested)`
							: String(row.children)
			},
			...(origin
				? [
						{
							label: 'Clone Of',
							value: origin,
							mono: true,
							hint: 'The snapshot this file system was cloned from.'
						}
					]
				: [])
		];
	});

	let properties = $derived(propertyRows(dataset));
	const helper = createColumnHelper<DataTableFeatures, PropertyRow>();
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

{#snippet hint(label: string, description: string)}
	{@const helpId = `${id}-${label.toLowerCase().replaceAll(' ', '-')}`}
	<span id={helpId} class="sr-only">{description}</span>
	<Tooltip.Root delayDuration={150}>
		<Tooltip.Trigger
			type="button"
			aria-label="Help for {label}"
			aria-describedby={helpId}
			class="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex size-4 shrink-0 items-center justify-center rounded-sm focus-visible:ring-2 focus-visible:outline-none"
		>
			<span class="icon-[lucide--circle-help] size-3.5" aria-hidden="true"></span>
		</Tooltip.Trigger>
		<Tooltip.Content side="top" sideOffset={6} class="z-60 max-w-64 text-left leading-5">
			{description}
		</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

{#snippet item(entry: Item)}
	<div class="min-w-0">
		<dt class="text-muted-foreground flex items-center gap-1 text-xs">
			{entry.label}
			{#if entry.hint}{@render hint(entry.label, entry.hint)}{/if}
		</dt>
		<dd
			class="mt-1 break-all {entry.mono ? 'font-mono text-xs leading-5' : ''} {entry.warning
				? 'font-medium text-amber-600 dark:text-amber-400'
				: ''}"
		>
			{entry.value}
		</dd>
	</div>
{/snippet}

{#snippet section(title: string, entries: Item[], first?: boolean)}
	<section class="space-y-2">
		<h3 class="text-sm font-medium">{title}</h3>
		<dl class="grid gap-x-6 gap-y-4 rounded-md border p-4 text-sm sm:grid-cols-2 md:grid-cols-3">
			{#if first}
				<div class="min-w-0">
					<dt class="text-muted-foreground text-xs">Used</dt>
					<dd class="mt-1"><Usage {dataset} class="max-w-64" /></dd>
				</div>
			{/if}
			{#each entries as entry (entry.label)}
				{@render item(entry)}
			{/each}
		</dl>
	</section>
{/snippet}

{#snippet scroller(label: string, content: Snippet)}
	<ScrollArea
		class="h-full"
		fade
		scrollbarYClasses="hidden"
		viewportProps={{
			class: 'overscroll-contain',
			role: 'region',
			'aria-label': label,
			tabindex: 0
		}}
	>
		<div class="px-4 pb-4">{@render content()}</div>
	</ScrollArea>
{/snippet}

{#snippet overview()}
	<div class="space-y-5">
		{@render section('Space', space, true)}
		{@render section('Status', status)}
	</div>
{/snippet}

{#snippet propertyTable()}
	<DataTable
		data={properties}
		{columns}
		columnWidths={{ name: '30%', value: '40%', source: '30%' }}
		getRowId={(property) => property.name}
		getRowLabel={(property) => property.name}
		initialSorting={[{ id: 'name', desc: false }]}
		searchPlaceholder="Search properties…"
		fullWidthSearch
		emptyMessage="No properties have been reported."
		{mobileRow}
	/>
{/snippet}

{#snippet nameCell(name: string)}<span class="font-mono break-all">{name}</span>{/snippet}
{#snippet valueCell(value: string)}
	<span class="break-all whitespace-pre-wrap">{value}</span>
{/snippet}
{#snippet sourceCell(source: string)}
	<span class="text-muted-foreground break-all">{source}</span>
{/snippet}
{#snippet mobileRow(row: Row<DataTableFeatures, PropertyRow>)}
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

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open) onClose();
	}}
>
	<Dialog.Content
		class="flex h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:h-[min(52rem,90dvh)] sm:max-w-3xl"
		showCloseButton={false}
	>
		<div class="flex shrink-0 items-center justify-between gap-3 border-b px-4 pt-4 pb-3">
			<Dialog.Header class="min-w-0 text-left">
				<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="{isPoolRoot(dataset)
							? 'icon-[bi--hdd-stack-fill]'
							: 'icon-[eos-icons--file-system]'} text-zen-highlight-foreground size-5 shrink-0"
						aria-hidden="true"
					></span>
					<span class="min-w-0 break-all">File System — {dataset.name}</span>
				</Dialog.Title>
				<Dialog.Description class="sr-only">
					Space usage, status and ZFS properties of {dataset.name}.
				</Dialog.Description>
			</Dialog.Header>
			<Dialog.Close>
				{#snippet child({ props })}
					<Button {...props} variant="ghost" size="icon" class="size-9 shrink-0">
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</Dialog.Close>
		</div>
		<Tabs.Root bind:value={tab} class="min-h-0 min-w-0 flex-1 gap-0">
			<div class="shrink-0 px-4 pt-4">
				{#if stale}
					<p class="text-muted-foreground mb-4 text-xs leading-5" role="status">
						File systems could not be refreshed. The last successful sample is shown.
					</p>
				{/if}
				<Tabs.List class="mb-4 w-full">
					<Tabs.Trigger value="overview" class="gap-1.5 text-xs sm:text-sm">
						<span class="icon-[lucide--layout-dashboard] size-3.5" aria-hidden="true"
						></span>Overview
					</Tabs.Trigger>
					<Tabs.Trigger value="properties" class="gap-1.5 text-xs sm:text-sm">
						<span class="icon-[carbon--data-table] size-3.5" aria-hidden="true"></span>Properties
					</Tabs.Trigger>
				</Tabs.List>
			</div>
			<Tabs.Content value="overview" class="mt-0 min-h-0" tabindex={-1}>
				{@render scroller('Overview', overview)}
			</Tabs.Content>
			<Tabs.Content value="properties" class="mt-0 min-h-0" tabindex={-1}>
				{@render scroller('Properties', propertyTable)}
			</Tabs.Content>
		</Tabs.Root>
	</Dialog.Content>
</Dialog.Root>
