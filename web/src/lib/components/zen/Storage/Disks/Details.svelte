<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { Button, buttonVariants } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import type { Disk, Partition } from '$lib/types/disk/disk';
	import { cn } from '$lib/utils.js';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';
	import { createBlockReason, diskIcon, diskModel, partitionBlockReason } from './disks';
	import MenuAction from './MenuAction.svelte';
	import SelfTest from './SelfTest.svelte';
	import Smart from './Smart.svelte';

	let {
		hostname,
		disk,
		sample,
		readingSmart,
		smartFailed,
		smartReadAt,
		stale,
		canModify,
		busy,
		onCreatePartitions,
		onDeletePartition,
		onClose
	}: {
		hostname: string;
		disk: Disk;
		sample?: Disk;
		readingSmart: boolean;
		smartFailed: boolean;
		smartReadAt: number | null;
		stale: boolean;
		canModify: boolean;
		busy: boolean;
		onCreatePartitions: () => void;
		onDeletePartition: (partition: Partition) => void;
		onClose: () => void;
	} = $props();
	let tab = $state('overview');
	let inventory = $derived([
		{ label: 'Device', value: disk.device },
		{ label: 'Type', value: disk.type || 'Not Reported' },
		{ label: 'Size', value: disk.size > 0 ? formatBytesBinary(disk.size) : 'Not Reported' },
		{ label: 'Usage', value: disk.usage || 'Not Reported' },
		{ label: 'Partition Table', value: disk.gpt ? 'GPT' : 'Not GPT' },
		{ label: 'Partitions', value: String(disk.partitions.length) },
		{ label: 'Model', value: diskModel(disk) },
		{ label: 'Serial', value: disk.serial.trim() || 'Not Reported' },
		{ label: 'Sylve Disk ID', value: disk.uuid || 'Not Reported' },
		{ label: 'Stable Identity', value: disk.identityStable ? 'Available' : 'Not Available' }
	]);
	const helper = createColumnHelper<DataTableFeatures, Partition>();
	let columns = $derived(
		helper.columns([
			helper.accessor('name', {
				header: 'Partition',
				cell: ({ row }) => renderSnippet(monoCell, row.original.name)
			}),
			helper.accessor((partition) => partition.usage || 'Not Reported', {
				id: 'usage',
				header: 'Type'
			}),
			helper.accessor('size', {
				header: 'Size',
				sortFn: 'basic',
				cell: ({ row }) => renderSnippet(sizeCell, row.original.size)
			}),
			helper.accessor('uuid', {
				header: 'GPT UUID',
				cell: ({ row }) => renderSnippet(monoCell, row.original.uuid)
			}),
			...(canModify
				? [
						helper.display({
							id: 'actions',
							header: () => renderSnippet(actionsHeader, undefined),
							cell: ({ row }) => renderSnippet(partitionActions, row.original),
							enableSorting: false,
							enableGlobalFilter: false
						})
					]
				: [])
		])
	);
</script>

{#snippet toolbar()}
	{#if canModify}
		{@const blocked = createBlockReason(disk)}
		{#if blocked}
			<Tooltip.Root delayDuration={500}>
				<Tooltip.Trigger
					type="button"
					aria-disabled="true"
					class={cn(
						buttonVariants({ size: 'sm' }),
						'h-9 cursor-help gap-2 aria-disabled:pointer-events-auto'
					)}
				>
					<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>Create Partitions
					<span class="sr-only">: {blocked}</span>
				</Tooltip.Trigger>
				<Tooltip.Content
					side="bottom"
					align="end"
					sideOffset={6}
					class="z-60 max-w-64 text-left text-pretty"
				>
					{blocked}
				</Tooltip.Content>
			</Tooltip.Root>
		{:else}
			<Button size="sm" class="h-9 gap-2" disabled={busy} onclick={onCreatePartitions}>
				<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>Create Partitions
			</Button>
		{/if}
	{/if}
{/snippet}

{#snippet actionsHeader(_value: undefined)}
	<span class="sr-only">Actions</span>
{/snippet}

{#snippet partitionActions(partition: Partition)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={busy}
						aria-label="Actions for {partition.name}"
						{...props}
					>
						<span class="icon-[lucide--ellipsis] size-5" aria-hidden="true"></span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end" class="w-48">
				<MenuAction
					label="Delete Partition"
					icon="icon-[lucide--trash-2]"
					blocked={partitionBlockReason(partition)}
					destructive
					onSelect={() => onDeletePartition(partition)}
				/>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet monoCell(value: string)}
	<span class="font-mono text-xs break-all">{value || 'Not Reported'}</span>
{/snippet}

{#snippet sizeCell(size: number)}
	<span class="whitespace-nowrap tabular-nums"
		>{size > 0 ? formatBytesBinary(size) : 'Not Reported'}</span
	>
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, Partition>)}
	<div class="flex items-start gap-2">
		<div class="flex min-w-0 flex-1 flex-wrap items-baseline justify-between gap-2">
			{@render monoCell(row.original.name)}
			<span class="text-xs">{@render sizeCell(row.original.size)}</span>
		</div>
		{#if canModify}
			<div class="-my-2 -mr-2 shrink-0">{@render partitionActions(row.original)}</div>
		{/if}
	</div>
	<p class="text-muted-foreground mt-2 text-xs">{row.original.usage || 'Type not reported'}</p>
	<p class="text-muted-foreground mt-2 text-xs">GPT UUID: {@render monoCell(row.original.uuid)}</p>
{/snippet}

<Dialog.Root
	open
	onOpenChange={(open) => {
		if (!open) onClose();
	}}
>
	<Dialog.Content
		class="flex h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:h-[min(56rem,85dvh)] sm:max-w-3xl xl:max-w-4xl 2xl:max-w-5xl"
		showCloseButton={false}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<Dialog.Header class="min-w-0 gap-1 text-left">
				<Dialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span
						class="{diskIcon(disk)} text-zen-highlight-foreground size-5 shrink-0"
						aria-hidden="true"
					></span>
					<span class="min-w-0 break-all">Disk — {disk.device}</span>
				</Dialog.Title>
				<Dialog.Description class="text-xs break-words">{diskModel(disk)}</Dialog.Description>
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
						Disk inventory could not be refreshed. The last successful sample is shown.
					</p>
				{/if}
				<Tabs.List class="mb-4 w-full">
					<Tabs.Trigger value="overview" class="gap-1.5 text-xs sm:text-sm">
						<span class="icon-[lucide--hard-drive] size-3.5" aria-hidden="true"></span>Overview
					</Tabs.Trigger>
					<Tabs.Trigger value="partitions" class="gap-1.5 text-xs sm:text-sm">
						<span class="icon-[ant-design--partition-outlined] size-3.5" aria-hidden="true"
						></span>Partitions
					</Tabs.Trigger>
					<Tabs.Trigger value="smart" class="gap-1.5 text-xs sm:text-sm">
						<span class="icon-[lucide--heart-pulse] size-3.5" aria-hidden="true"></span>S.M.A.R.T.
					</Tabs.Trigger>
					<Tabs.Trigger value="selftest" class="gap-1.5 text-xs sm:text-sm">
						<span class="icon-[lucide--clipboard-check] size-3.5" aria-hidden="true"
						></span>Self-Test
					</Tabs.Trigger>
				</Tabs.List>
			</div>
			<Tabs.Content value="overview" class="mt-0 min-h-0 overflow-y-auto px-4 pb-4">
				<dl class="grid gap-x-6 gap-y-4 rounded-md border p-4 text-sm sm:grid-cols-2">
					{#each inventory as item (item.label)}
						<div class="min-w-0">
							<dt class="text-muted-foreground text-xs">{item.label}</dt>
							<dd class="mt-1 break-all">{item.value}</dd>
						</div>
					{/each}
				</dl>
			</Tabs.Content>
			<Tabs.Content value="partitions" class="mt-0 min-h-0 overflow-y-auto px-4 pb-4">
				<DataTable
					data={disk.partitions}
					{columns}
					getRowId={(partition) => partition.name}
					getRowLabel={(partition) => partition.name}
					initialSorting={[{ id: 'name', desc: false }]}
					searchPlaceholder="Search partitions…"
					fullWidthSearch
					{toolbar}
					emptyMessage="No partitions are reported on this disk."
					columnClasses={{ size: 'pr-6 text-right tabular-nums', actions: 'w-14 pr-3' }}
					{mobileRow}
				/>
			</Tabs.Content>
			<Tabs.Content value="smart" class="mt-0 min-h-0 overflow-y-auto px-4 pb-4">
				<Smart {disk} {sample} {readingSmart} {smartFailed} {smartReadAt} />
			</Tabs.Content>
			<Tabs.Content value="selftest" class="mt-0 min-h-0 overflow-y-auto px-4 pb-4">
				{#if tab === 'selftest'}
					<SelfTest {disk} {hostname} {canModify} />
				{/if}
			</Tabs.Content>
		</Tabs.Root>
	</Dialog.Content>
</Dialog.Root>
