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
		clearPartitionTable,
		destroyPartition,
		initializeGPT,
		listDisksResult
	} from '$lib/api/disk/disk';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { Disk, Partition } from '$lib/types/disk/disk';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import {
		APIRequestError,
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		removeCache,
		updateCache
	} from '$lib/utils/http';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';
	import { resource, useInterval, watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import CreatePartitions from './CreatePartitions.svelte';
	import Details from './Details.svelte';
	import {
		clearBlockReason,
		createBlockReason,
		diskActionError,
		diskHealth,
		diskIcon,
		diskIdentity,
		diskModel,
		diskWearout,
		gptBlockReason,
		partitionBlockReason,
		wearoutPercent,
		type DisksData
	} from './disks';
	import MenuAction from './MenuAction.svelte';
	import MissingSchedules from './MissingSchedules.svelte';

	type DiskAction =
		| { kind: 'gpt'; disk: Disk }
		| { kind: 'clear'; disk: Disk }
		| { kind: 'delete'; disk: Disk; partition: Partition };

	const actionText = {
		gpt: {
			title: 'Initialize GPT?',
			icon: 'icon-[carbon--logical-partition]',
			confirm: 'Initialize GPT',
			working: 'Initializing…',
			failed: 'Could not initialize GPT. Please try again.'
		},
		clear: {
			title: 'Clear Partition Table?',
			icon: 'icon-[lucide--eraser]',
			confirm: 'Clear Partition Table',
			working: 'Clearing…',
			failed: 'Could not clear the partition table. Please try again.'
		},
		delete: {
			title: 'Delete Partition?',
			icon: 'icon-[lucide--trash-2]',
			confirm: 'Delete Partition',
			working: 'Deleting…',
			failed: 'Could not delete the partition. Please try again.'
		}
	};

	let { data }: { data: DisksData } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const id = $props.id();
	const lifetime = new AbortController();
	let initialFailure = $state(isAPIResponse(initial.disks));
	let selectedIdentity = $state('');
	let smartSamples = $state.raw(new Map<string, Disk>());
	let readingSmart = $state(false);
	let smartFailed = $state(false);
	let smartReadAt = $state<number | null>(null);
	let action = $state<DiskAction | null>(null);
	let actionOpen = $state(false);
	let actionError = $state('');
	let confirmDevice = $state('');
	let working = $state(false);
	let createIdentity = $state('');
	let createOpen = $state(false);
	let createSession = $state(0);
	const disks = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal]);
			const result = await listDisksResult('none', { hostname, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialFailure = false;
			void updateCache('disk-list-inventory', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.disks) ? initial.disks : [],
			lazy: initial.fresh ?? false
		}
	);
	let rows = $derived(
		disks.current.map((disk) => {
			const sample = smartSamples.get(diskIdentity(disk));
			return sample ? { ...disk, smartData: sample.smartData, wearOut: sample.wearOut } : disk;
		})
	);
	let failed = $derived(initialFailure || Boolean(disks.error));
	let selected = $derived(rows.find((disk) => diskIdentity(disk) === selectedIdentity));
	let createDisk = $derived(rows.find((disk) => diskIdentity(disk) === createIdentity));
	let canModify = $derived(allowed('Sys.Modify', nodePath(initial.hostname)));
	let smartReadable = $derived(rows.some((disk) => disk.type !== 'Virtual' && disk.size > 0));
	let searchText = $derived(
		new Map(
			rows.map((disk) => [
				diskIdentity(disk),
				[
					disk.device,
					disk.type,
					disk.usage,
					diskModel(disk),
					disk.serial,
					disk.uuid,
					formatBytesBinary(disk.size),
					disk.gpt ? 'GPT' : 'Not GPT',
					diskHealth(disk, smartSamples.get(diskIdentity(disk)), readingSmart),
					diskWearout(disk, smartSamples.get(diskIdentity(disk)), readingSmart),
					...disk.partitions.flatMap((partition) => [
						partition.name,
						partition.usage,
						partition.uuid
					])
				]
					.join(' ')
					.toLocaleLowerCase()
			])
		)
	);

	$effect(() => {
		if (selectedIdentity && !selected) selectedIdentity = '';
	});

	let smartRequested = false;
	$effect(() => {
		if (!smartReadable || smartRequested) return;
		smartRequested = true;
		untrack(() => void readSmart());
	});

	function refresh() {
		void disks.refetch();
		void readSmart();
	}

	async function readSmart() {
		if (readingSmart || !smartReadable || lifetime.signal.aborted) return;
		readingSmart = true;
		smartFailed = false;
		try {
			const result = await listDisksResult('full', {
				hostname: initial.hostname,
				signal: lifetime.signal
			});
			lifetime.signal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			smartSamples = new Map(result.map((disk) => [diskIdentity(disk), disk]));
			smartReadAt = Date.now();
			void updateCache('disk-list', result, initial.hostname);
		} catch (error) {
			if (!lifetime.signal.aborted && !isRequestCancellation(error)) smartFailed = true;
		} finally {
			if (!lifetime.signal.aborted) readingSmart = false;
		}
	}

	function actionBlockReason(target: DiskAction): string {
		const disk = disks.current.find((item) => diskIdentity(item) === diskIdentity(target.disk));
		if (!disk)
			return 'This disk is no longer reported by the node. Close this dialog and refresh the list.';
		if (target.kind === 'gpt') return gptBlockReason(disk);
		if (target.kind === 'clear') return clearBlockReason(disk);
		const partition = disk.partitions.find(
			(item) => item.name === target.partition.name && item.uuid === target.partition.uuid
		);
		if (!partition) {
			return 'This partition is no longer reported by the node. Close this dialog and refresh the list.';
		}
		return partitionBlockReason(partition);
	}

	function openAction(next: DiskAction) {
		if (working || !canModify || actionBlockReason(next)) return;
		action = next;
		actionError = '';
		confirmDevice = '';
		actionOpen = true;
	}

	function openCreate(disk: Disk) {
		if (working || !canModify || createBlockReason(disk)) return;
		createIdentity = diskIdentity(disk);
		createSession += 1;
		createOpen = true;
	}

	async function partitionsCreated() {
		working = true;
		try {
			await removeCache('disk-list', initial.hostname);
			await disks.refetch();
		} finally {
			if (!lifetime.signal.aborted) working = false;
		}
	}

	function successMessage(target: DiskAction): string {
		if (target.kind === 'gpt') return `GPT initialized on ${target.disk.device}`;
		if (target.kind === 'clear') return `Partition table cleared on ${target.disk.device}`;
		return `Partition ${target.partition.name} deleted`;
	}

	async function runAction() {
		const target = action;
		if (working || !actionOpen || !canModify || !target) return;
		if (target.kind === 'clear' && confirmDevice !== target.disk.device) return;
		const blocked = actionBlockReason(target);
		if (blocked) {
			actionError = blocked;
			return;
		}
		working = true;
		actionError = '';
		try {
			const options = { hostname: initial.hostname };
			const response =
				target.kind === 'gpt'
					? await initializeGPT(target.disk.device, options)
					: target.kind === 'clear'
						? await clearPartitionTable(target.disk.device, options)
						: await destroyPartition(target.partition.name, options);
			if (lifetime.signal.aborted) return;
			if (response.status !== 'success') {
				actionError = diskActionError(response, actionText[target.kind].failed);
				handleAPIError(response);
				void disks.refetch();
				return;
			}
			actionOpen = false;
			toast.success(successMessage(target), { position: 'bottom-center' });
			await removeCache('disk-list', initial.hostname);
			await disks.refetch();
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted) {
				actionError = actionText[target.kind].failed;
			}
		} finally {
			if (!lifetime.signal.aborted) working = false;
		}
	}

	const interval = useInterval(() => (storage.idle ? 120_000 : 60_000), {
		callback: () => {
			if (storage.visible && !disks.loading) void disks.refetch();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle && !disks.loading) void disks.refetch();
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		interval.pause();
	});

	const helper = createColumnHelper<DataTableFeatures, Disk>();
	const columns = helper.columns([
		helper.accessor('device', {
			header: 'Device',
			cell: ({ row }) => renderSnippet(deviceCell, row.original)
		}),
		helper.accessor(diskModel, { id: 'model', header: 'Model' }),
		helper.accessor('type', { header: 'Type' }),
		helper.accessor((disk) => disk.usage || 'Not Reported', { id: 'usage', header: 'Usage' }),
		helper.accessor('size', {
			header: 'Size',
			sortFn: 'basic',
			cell: ({ row }) => renderSnippet(sizeCell, row.original.size)
		}),
		helper.accessor('serial', {
			header: 'Serial',
			cell: ({ row }) => renderSnippet(serialCell, row.original.serial)
		}),
		helper.accessor((disk) => (disk.gpt ? 'GPT' : 'Not GPT'), {
			id: 'gpt',
			header: 'Partition Table'
		}),
		helper.accessor(
			(disk) => diskHealth(disk, smartSamples.get(diskIdentity(disk)), readingSmart),
			{
				id: 'smart',
				header: 'S.M.A.R.T.',
				cell: ({ row }) => renderSnippet(healthCell, row.original)
			}
		),
		helper.accessor((disk) => wearoutPercent(smartSamples.get(diskIdentity(disk))) ?? -1, {
			id: 'wearout',
			header: 'Wearout',
			sortFn: 'basic',
			cell: ({ row }) => renderSnippet(wearoutCell, row.original)
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

{#snippet deviceButton(disk: Disk)}
	<button
		type="button"
		class="text-zen-highlight-foreground focus-visible:ring-ring rounded-sm font-mono text-[13px] leading-5 font-medium whitespace-nowrap underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
		onclick={() => (selectedIdentity = diskIdentity(disk))}
		aria-label="View details for {disk.device}"
		aria-haspopup="dialog">{disk.device}</button
	>
{/snippet}

{#snippet deviceCell(disk: Disk)}
	<div class="flex items-center gap-2.5">
		<span class="{diskIcon(disk)} text-muted-foreground size-5 shrink-0" aria-hidden="true"></span>
		{@render deviceButton(disk)}
	</div>
{/snippet}

{#snippet sizeCell(size: number)}
	<span class="whitespace-nowrap tabular-nums"
		>{size > 0 ? formatBytesBinary(size) : 'Not Reported'}</span
	>
{/snippet}

{#snippet serialCell(serial: string)}
	<span class="font-mono text-xs whitespace-nowrap">{serial.trim() || 'Not Reported'}</span>
{/snippet}

{#snippet healthCell(disk: Disk)}
	{@const health = diskHealth(disk, smartSamples.get(diskIdentity(disk)), readingSmart)}
	<span class="whitespace-nowrap text-xs">
		<span
			class="mr-1.5 inline-block size-1.5 rounded-full align-middle {health === 'Failed'
				? 'bg-red-500'
				: health === 'Passed'
					? 'bg-emerald-500'
					: health === 'Reading…'
						? 'bg-muted-foreground/50 animate-pulse'
						: 'bg-muted-foreground/50'}"
			aria-hidden="true"
		></span>{health}
	</span>
{/snippet}

{#snippet wearoutCell(disk: Disk)}
	{@const sample = smartSamples.get(diskIdentity(disk))}
	<span
		class="whitespace-nowrap text-xs {wearoutPercent(sample) === null
			? 'text-muted-foreground'
			: 'tabular-nums'}">{diskWearout(disk, sample, readingSmart)}</span
	>
{/snippet}

{#snippet actionsHeader(_value: undefined)}
	<span class="sr-only">Actions</span>
{/snippet}

{#snippet rowActions(disk: Disk)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={working}
						aria-label="Actions for {disk.device}"
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
					if (selectedIdentity || createOpen) event.preventDefault();
				}}
			>
				<DropdownMenu.Item
					class="cursor-pointer"
					onSelect={() => (selectedIdentity = diskIdentity(disk))}
				>
					<span class="icon-[lucide--eye] size-4" aria-hidden="true"></span>View
				</DropdownMenu.Item>
				{#if canModify}
					<MenuAction
						label={actionText.gpt.confirm}
						icon={actionText.gpt.icon}
						blocked={gptBlockReason(disk)}
						onSelect={() => openAction({ kind: 'gpt', disk })}
					/>
					<MenuAction
						label="Create Partitions"
						icon="icon-[ant-design--partition-outlined]"
						blocked={createBlockReason(disk)}
						onSelect={() => openCreate(disk)}
					/>
					<DropdownMenu.Separator />
					<MenuAction
						label={actionText.clear.confirm}
						icon={actionText.clear.icon}
						blocked={clearBlockReason(disk)}
						destructive
						onSelect={() => openAction({ kind: 'clear', disk })}
					/>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}

{#snippet mobileRow(row: Row<DataTableFeatures, Disk>)}
	{@const partitions = row.original.partitions.length}
	{@const partitionTable =
		(row.original.gpt ? 'GPT' : 'Not GPT') +
		(partitions > 0 ? ` · ${partitions} ${partitions === 1 ? 'Partition' : 'Partitions'}` : '')}
	<div class="mb-3 flex items-start gap-2.5">
		<span class="{diskIcon(row.original)} text-muted-foreground size-5 shrink-0" aria-hidden="true"
		></span>
		<div class="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-1">
			{@render deviceButton(row.original)}
			<span class="text-muted-foreground min-w-0 text-xs break-words"
				>{diskModel(row.original)}</span
			>
		</div>
		<div class="-my-2 -mr-2 shrink-0">{@render rowActions(row.original)}</div>
	</div>
	<dl class="grid grid-cols-2 gap-3 text-xs">
		<div>
			<dt class="text-muted-foreground">Type</dt>
			<dd class="mt-1">{row.original.type || 'Not Reported'}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Usage</dt>
			<dd class="mt-1">{row.original.usage || 'Not Reported'}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Size</dt>
			<dd class="mt-1">{@render sizeCell(row.original.size)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Partition Table</dt>
			<dd class="mt-1">{partitionTable}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">S.M.A.R.T.</dt>
			<dd class="mt-1">{@render healthCell(row.original)}</dd>
		</div>
		<div>
			<dt class="text-muted-foreground">Wearout</dt>
			<dd class="mt-1">{@render wearoutCell(row.original)}</dd>
		</div>
		<div class="col-span-2 min-w-0">
			<dt class="text-muted-foreground">Serial</dt>
			<dd class="mt-1 truncate">{@render serialCell(row.original.serial)}</dd>
		</div>
	</dl>
{/snippet}

<svelte:head><title>Storage Disks · {initial.hostname}</title></svelte:head>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex min-w-0 items-center gap-3">
			<span
				class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
				aria-hidden="true"><span class="icon-[mdi--harddisk] size-5"></span></span
			>
			<Breadcrumb.Root class="min-w-0">
				<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
					<Breadcrumb.Item>
						<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
					</Breadcrumb.Item>
					<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
					<Breadcrumb.Item class="min-w-0">
						<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
							<Breadcrumb.Page class="font-semibold">Disks</Breadcrumb.Page>
						</h1>
					</Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		</div>
		<Button
			variant="outline"
			size="icon"
			class="size-9"
			disabled={disks.loading || readingSmart}
			onclick={refresh}
			aria-label="Refresh disks and S.M.A.R.T. data"
		>
			<span
				class="icon-[lucide--refresh-cw] size-4"
				class:animate-spin={disks.loading || readingSmart}
				aria-hidden="true"
			></span>
		</Button>
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
					? 'Disk inventory could not be refreshed. Showing its last successful sample.'
					: 'Disk inventory could not be loaded. Please try again.'}
			</p>
			<Button variant="outline" size="sm" disabled={disks.loading} onclick={() => disks.refetch()}
				>Try Again</Button
			>
		</div>
	{/if}
	<MissingSchedules disks={rows} ready={!failed} hostname={initial.hostname} {canModify} />
	{#if !failed || rows.length > 0}
		<DataTable
			data={rows}
			{columns}
			getRowId={diskIdentity}
			getRowLabel={(disk) => disk.device}
			globalFilterFn={(row, _columnId, value) =>
				(searchText.get(diskIdentity(row.original)) ?? '').includes(
					String(value).trim().toLocaleLowerCase()
				)}
			initialSorting={[{ id: 'device', desc: false }]}
			columnClasses={{
				device: 'whitespace-nowrap',
				model: 'whitespace-nowrap',
				type: 'whitespace-nowrap',
				usage: 'whitespace-nowrap',
				size: 'pr-6 text-right tabular-nums',
				gpt: 'whitespace-nowrap',
				actions: 'w-14 pr-3'
			}}
			loading={disks.loading}
			searchPlaceholder="Search disks, serials or partitions…"
			emptyMessage="No disks were reported by this node."
			{mobileRow}
		/>
	{/if}
</div>

{#if selected}
	{#key selectedIdentity}
		<Details
			hostname={initial.hostname}
			disk={selected}
			sample={smartSamples.get(selectedIdentity)}
			{readingSmart}
			{smartFailed}
			{smartReadAt}
			stale={failed}
			{canModify}
			busy={working}
			onCreatePartitions={() => selected && openCreate(selected)}
			onDeletePartition={(partition) =>
				selected && openAction({ kind: 'delete', disk: selected, partition })}
			onClose={() => (selectedIdentity = '')}
		/>
	{/key}
{/if}

{#if createDisk}
	{#key createSession}
		<CreatePartitions
			bind:open={createOpen}
			disk={createDisk}
			hostname={initial.hostname}
			{canModify}
			onChanged={partitionsCreated}
		/>
	{/key}
{/if}

{#snippet strong(text: string)}
	<span class="text-foreground text-sm font-semibold break-all">{text}</span>
{/snippet}

<AlertDialog.Root bind:open={actionOpen}>
	<AlertDialog.Content
		class="z-60 flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		overlayClass="z-60"
		escapeKeydownBehavior={working ? 'ignore' : 'close'}
		aria-busy={working}
	>
		{#if action}
			{@const text = actionText[action.kind]}
			{@const destructive = action.kind !== 'gpt'}
			{@const confirmed = action.kind !== 'clear' || confirmDevice === action.disk.device}
			<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
				<AlertDialog.Header class="min-w-0 text-left">
					<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
						<span
							class="{text.icon} size-5 shrink-0 {destructive
								? 'text-destructive'
								: 'text-zen-highlight-foreground'}"
							aria-hidden="true"
						></span>
						{text.title}
					</AlertDialog.Title>
				</AlertDialog.Header>
				<AlertDialog.Cancel disabled={working}>
					{#snippet child({ props })}
						<Button
							{...props}
							variant="ghost"
							size="icon"
							class="size-9 shrink-0"
							disabled={working}
						>
							<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
							<span class="sr-only">Close</span>
						</Button>
					{/snippet}
				</AlertDialog.Cancel>
			</div>
			<div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
				<AlertDialog.Description class="w-full text-left leading-relaxed text-pretty break-words">
					{#if action.kind === 'gpt'}
						This writes an empty GPT partition table to {@render strong(action.disk.device)} on
						{@render strong(initial.hostname)}, so partitions can be created on it.
					{:else if action.kind === 'clear'}
						{@const count = action.disk.partitions.length}
						This deletes the partition table on {@render strong(action.disk.device)} on
						{@render strong(initial.hostname)}{#if count > 0}, along with its {count}
							{count === 1 ? 'partition' : 'partitions'}{/if}. This cannot be undone.
					{:else}
						This deletes partition {@render strong(action.partition.name)}
						({formatBytesBinary(action.partition.size)}{action.partition.usage
							? `, ${action.partition.usage}`
							: ''}) from {@render strong(action.disk.device)} on {@render strong(
							initial.hostname
						)}. This cannot be undone.
					{/if}
				</AlertDialog.Description>
				<p class="text-muted-foreground text-sm leading-relaxed text-pretty">
					{#if action.kind === 'gpt'}
						Make sure the disk holds nothing you need. Data written to the whole disk without a
						partition table, such as an exported ZFS pool, can be damaged.
					{:else if action.kind === 'clear'}
						The data is not securely erased, but it can no longer be reached through the partitions.
						The first and last 1 MiB of the disk are overwritten with zeros.
					{:else}
						The data in the partition is not securely erased, but it can no longer be reached
						through it.
					{/if}
				</p>
				{#if action.kind === 'clear'}
					<div class="space-y-2">
						<Label for="{id}-confirm-device" class="block leading-relaxed">
							Type {@render strong(action.disk.device)} to confirm
						</Label>
						<Input
							id="{id}-confirm-device"
							bind:value={confirmDevice}
							placeholder={action.disk.device}
							autocomplete="off"
							spellcheck={false}
							disabled={working}
						/>
					</div>
				{/if}
				{#if !canModify}
					<p class="text-destructive text-sm" role="alert">
						You do not have permission to modify disks on this node.
					</p>
				{/if}
				{#if actionError}<p class="text-destructive text-sm" role="alert">{actionError}</p>{/if}
			</div>
			<AlertDialog.Footer class="shrink-0 border-t p-4">
				<Button
					variant={destructive ? 'destructive' : 'default'}
					class="gap-2"
					disabled={working || !canModify || !confirmed}
					onclick={runAction}
				>
					{#if working}
						<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"
						></span>
					{/if}
					{working ? text.working : text.confirm}
				</Button>
			</AlertDialog.Footer>
		{/if}
	</AlertDialog.Content>
</AlertDialog.Root>
