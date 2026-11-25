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
	import type { DetailedDataset } from '$lib/types/zfs/dataset';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';
	import type { Snippet } from 'svelte';
	import {
		compressRatioLabel,
		isEncrypted,
		isKeyUnavailable,
		propertyRows,
		propertyValue,
		sizeLabel,
		switchLabel,
		type PropertyRow
	} from '../FileSystems/filesystems';
	import { compressionLabel, encryptionLabel } from '../FileSystems/form';
	import {
		blockSize,
		devicePath,
		isSparse,
		reservation,
		userLabel,
		volumeModeLabel,
		volumeSize,
		type VolumeUser
	} from './volumes';

	let {
		dataset,
		users,
		stale,
		onClose
	}: { dataset: DetailedDataset; users: VolumeUser[]; stale: boolean; onClose: () => void } =
		$props();
	const id = $props.id();
	let tab = $state('overview');

	type Item = { label: string; value: string; hint?: string; mono?: boolean; warning?: boolean };

	let space = $derived<Item[]>([
		{ label: 'Size', value: formatBytesBinary(volumeSize(dataset)) },
		{
			label: 'Used',
			value: formatBytesBinary(dataset.used),
			hint: 'Space this volume takes in the pool, including its reservation and snapshots.'
		},
		{
			label: 'Referenced',
			value: formatBytesBinary(dataset.referenced),
			hint: 'Data written to the volume itself. Some of it may be shared with snapshots.'
		},
		{
			label: 'Reservation',
			value: isSparse(dataset) ? 'None (Sparse)' : formatBytesBinary(reservation(dataset)),
			hint: 'Space set aside in the pool for this volume. A sparse volume reserves nothing, so writes to it can fail if the pool fills up.'
		},
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
			{ label: 'Device', value: devicePath(dataset), mono: true },
			{
				label: 'Block Size',
				value: blockSize(dataset) ? formatBytesBinary(blockSize(dataset)) : 'Not Reported',
				hint: 'The smallest unit ZFS reads and writes on this volume. It can’t be changed after creation.'
			},
			{
				label: 'Volume Mode',
				value: volumeModeLabel(dataset),
				hint: 'How FreeBSD exposes the volume. Device Only gives a plain disk device for virtual machines and iSCSI. GEOM also lets FreeBSD read the partitions on it.'
			},
			{ label: 'Primary Cache', value: propertyValue(dataset, 'primarycache') ?? 'Not Reported' },
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
							hint: "Without its key, this volume's data can't be read."
						},
						{
							label: 'Encryption Root',
							value: !root ? 'Not Reported' : root === dataset.name ? 'This Volume' : root
						}
					]
				: []),
			...(origin
				? [
						{
							label: 'Clone Of',
							value: origin,
							mono: true,
							hint: 'The snapshot this volume was cloned from.'
						}
					]
				: []),
			{
				label: 'iSCSI',
				value: users.length > 0 ? users.map(userLabel).join(', ') : 'Not Shared',
				hint: 'iSCSI targets that share this volume with other machines.'
			}
		];
	});

	let properties = $derived(propertyRows(dataset, 'volume'));
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

{#snippet section(title: string, entries: Item[])}
	<section class="space-y-2">
		<h3 class="text-sm font-medium">{title}</h3>
		<dl class="grid gap-x-6 gap-y-4 rounded-md border p-4 text-sm sm:grid-cols-2 md:grid-cols-3">
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
		{@render section('Space', space)}
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
						class="icon-[carbon--volume-block-storage] text-zen-highlight-foreground size-5 shrink-0"
						aria-hidden="true"
					></span>
					<span class="min-w-0 break-all">Volume — {dataset.name}</span>
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
						Volumes could not be refreshed. The last successful sample is shown.
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
