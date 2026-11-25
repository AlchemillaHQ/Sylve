<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import { listDisksResult } from '$lib/api/disk/disk';
	import { deletePool, getPoolsResult } from '$lib/api/zfs/pool';
	import { ranges } from '$lib/components/custom/ZFS/Dashboard/dashboard';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import * as Breadcrumb from '$lib/components/ui/breadcrumb/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import DataTable from '$lib/components/zen/DataTable/DataTable.svelte';
	import type { DataTableFeatures } from '$lib/components/zen/DataTable/features';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
	import type { Zpool, ZpoolStatusPool } from '$lib/types/zfs/pool';
	import {
		APIRequestError,
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		removeCache,
		updateCache
	} from '$lib/utils/http';
	import { parsePoolActionError } from '$lib/utils/zfs/pool.svelte';
	import { createColumnHelper, renderSnippet, type Row } from '@tanstack/svelte-table';
	import { resource, useInterval, watch } from 'runed';
	import { onDestroy, tick } from 'svelte';
	import { toast } from 'svelte-sonner';
	import Capacity from './Capacity.svelte';
	import Create from './Create.svelte';
	import Details from './Details.svelte';
	import Edit from './Edit.svelte';
	import { poolEditBlockReason } from './edit';
	import {
		poolCreateHref,
		poolDeviceCount,
		poolHref,
		poolLayout,
		poolUsage,
		type PoolsData
	} from './pools';
	import { scrubRunsCacheKey, scrubSchedulesCacheKey } from './scan';
	import State from './State.svelte';
	import { preloadPoolStatuses } from './status';

	let {
		data,
		selectedGUID,
		create = false
	}: { data: PoolsData; selectedGUID: string; create?: boolean } = $props();
	// svelte-ignore state_referenced_locally
	const initial = data;
	const id = $props.id();
	const lifetime = new AbortController();
	let inventory = new AbortController();
	let editOpen = $state(false);
	let editGUID = $state('');
	let deleteOpen = $state(false);
	let deleteTarget = $state<Zpool | null>(null);
	let deleteName = $state('');
	let deleteError = $state('');
	let deleting = $state(false);
	let initialPoolFailure = $state(isAPIResponse(initial.pools));
	let initialDiskFailure = $state(isAPIResponse(initial.disks));
	let statuses = $state(initial.statuses ?? {});
	const pools = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal, inventory.signal]);
			const result = await getPoolsResult(false, { hostname, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialPoolFailure = false;
			void updateCache('pool-list', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.pools) ? initial.pools : [],
			lazy: initial.fresh?.pools ?? false
		}
	);
	const disks = resource(
		() => initial.hostname,
		async (hostname, _previous, { signal }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal, inventory.signal]);
			const result = await listDisksResult('none', { hostname, signal: requestSignal });
			requestSignal.throwIfAborted();
			if (isAPIResponse(result)) throw new APIRequestError(result);
			initialDiskFailure = false;
			void updateCache('disk-list-inventory', result, hostname);
			return result;
		},
		{
			initialValue: Array.isArray(initial.disks) ? initial.disks : [],
			lazy: initial.fresh?.disks ?? false
		}
	);
	const poolStatuses = resource(
		() => pools.current,
		async (items, _previous, { signal, refetching }) => {
			const requestSignal = AbortSignal.any([signal, lifetime.signal, inventory.signal]);
			return await preloadPoolStatuses(initial.hostname, items, {
				signal: requestSignal,
				fresh: refetching === true,
				onStatus: (status) => {
					requestSignal.throwIfAborted();
					statuses[status.pool_guid] = status;
				}
			});
		},
		{ initialValue: initial.statuses ?? {} }
	);
	let poolFailure = $derived(initialPoolFailure || Boolean(pools.error));
	let diskFailure = $derived(initialDiskFailure || Boolean(disks.error));
	let selected = $derived(pools.current.find((pool) => pool.guid === selectedGUID));
	let editTarget = $derived(pools.current.find((pool) => pool.guid === editGUID));
	let busy = $derived(pools.loading || disks.loading || deleting);
	let canModify = $derived(allowed('Sys.Modify', nodePath(initial.hostname)));
	let deleteBlocked = $derived(deleteTarget ? deleteBlockReason(deleteTarget) : '');
	let canConfirmDelete = $derived(
		canModify && deleteTarget !== null && deleteName === deleteTarget.name && !deleteBlocked
	);

	function openEditor(pool: Zpool) {
		const current = pools.current.find((item) => item.guid === pool.guid);
		if (
			!current ||
			deleting ||
			deleteOpen ||
			!canModify ||
			poolEditBlockReason(current, statuses[current.guid] ?? null)
		)
			return;
		editGUID = current.guid;
		editOpen = true;
	}

	function deleteBlockReason(pool: Zpool): string {
		const bootfs = pool.properties.bootfs?.value;
		if (bootfs && bootfs !== '-' && bootfs.toLowerCase() !== 'none')
			return 'Boot pools cannot be deleted.';
		const scan = statuses[pool.guid]?.scan_stats?.state?.toUpperCase();
		if (scan === 'SCANNING' || scan === 'ERRORSCRUBBING' || scan === 'PAUSED')
			return 'Wait for the active scrub or resilver to finish before deleting this pool.';
		return '';
	}

	function confirmDelete(pool: Zpool) {
		if (deleting || !canModify || deleteBlockReason(pool)) return;
		deleteTarget = pool;
		deleteName = '';
		deleteError = '';
		deleteOpen = true;
	}

	async function deleteConfirmed() {
		if (deleting || !deleteOpen || !canConfirmDelete || !deleteTarget) return;
		const target = deleteTarget;
		const current = pools.current.find((pool) => pool.guid === target.guid);
		if (!current || current.name !== target.name) {
			deleteError = 'The pool has changed. Close this dialog and select it again.';
			return;
		}
		const blocked = deleteBlockReason(current);
		if (blocked) {
			deleteError = blocked;
			return;
		}
		deleting = true;
		deleteError = '';
		inventory.abort();
		try {
			const response = await deletePool(target.guid, { hostname: initial.hostname });
			if (response.status !== 'success') {
				if (!lifetime.signal.aborted) {
					deleteError = parsePoolActionError(response);
					handleAPIError(response);
				}
				return;
			}
			const cacheKeys = [
				'pool-list',
				'zfs-pools-full',
				'disk-list-inventory',
				'basic-settings',
				'total-disk-usage-full',
				'zfs-filesystems',
				'zfs-volumes',
				'zfs-periodic-snapshots',
				`zen-pool-status-${target.guid}`,
				scrubSchedulesCacheKey,
				scrubRunsCacheKey(target.guid),
				`zfs-dashboard-snapshot:${initial.hostname}`,
				...ranges.flatMap(({ seconds }) => [
					`zfs-dashboard-history:${initial.hostname}:${seconds}:all`,
					`zfs-dashboard-history:${initial.hostname}:${seconds}:${target.guid}`
				])
			];
			await Promise.all(cacheKeys.map((key) => removeCache(key, initial.hostname)));
			if (lifetime.signal.aborted) return;
			const remaining = pools.current.filter((pool) => pool.guid !== target.guid);
			pools.mutate(remaining);
			delete statuses[target.guid];
			void updateCache('pool-list', remaining, initial.hostname);
			deleteOpen = false;
			toast.success(`Pool ${target.name} deleted`, { position: 'bottom-center' });
		} catch (error) {
			if (!isRequestCancellation(error) && !lifetime.signal.aborted) {
				deleteError = 'Could not delete the pool. Please try again.';
			}
		} finally {
			if (!lifetime.signal.aborted) {
				inventory = new AbortController();
				await Promise.all([pools.refetch(), disks.refetch()]);
				deleting = false;
			}
		}
	}

	async function poolCreated(name: string) {
		const cacheKeys = [
			'pool-list',
			'zfs-pools-full',
			'disk-list-inventory',
			'disk-list',
			'basic-settings',
			'total-disk-usage-full',
			'zfs-filesystems',
			'zfs-volumes',
			`zfs-dashboard-snapshot:${initial.hostname}`,
			...ranges.map(({ seconds }) => `zfs-dashboard-history:${initial.hostname}:${seconds}:all`)
		];
		await Promise.all(cacheKeys.map((key) => removeCache(key, initial.hostname)));
		if (lifetime.signal.aborted) return;
		await refresh();
		const created = pools.current.find((pool) => pool.name === name);
		await useSafeGoto(poolHref(initial.hostname, created?.guid));
	}

	function rememberStatus(status: ZpoolStatusPool) {
		statuses[status.pool_guid] = status;
	}

	async function refresh() {
		if (deleting) return;
		await Promise.all([pools.refetch(), disks.refetch()]);
		await tick();
		if (!deleting && !lifetime.signal.aborted) await poolStatuses.refetch(true);
	}
	const poolInterval = useInterval(() => (storage.idle ? 30_000 : 10_000), {
		callback: () => {
			if (storage.visible && !deleting && !pools.loading) void pools.refetch();
		}
	});
	const diskInterval = useInterval(60_000, {
		callback: () => {
			if (storage.visible && !deleting && !disks.loading) void disks.refetch();
		}
	});
	watch(
		[() => storage.visible, () => storage.idle],
		([visible, idle]) => {
			if (visible && !idle) void refresh();
		},
		{ lazy: true }
	);
	watch(
		[() => selectedGUID, () => canModify, () => editTarget?.guid],
		([selected, permitted, target]) => {
			if (selected || !permitted || !target) editOpen = false;
		},
		{ lazy: true }
	);
	onDestroy(() => {
		lifetime.abort();
		inventory.abort();
		poolInterval.pause();
		diskInterval.pause();
	});

	const helper = createColumnHelper<DataTableFeatures, Zpool>();
	const columns = helper.columns([
		helper.accessor((pool) => `${pool.name} ${poolLayout(pool)}`, {
			id: 'name',
			header: 'Name',
			cell: ({ row }) => renderSnippet(poolCell, row.original)
		}),
		helper.accessor('state', {
			header: 'Health',
			cell: ({ row }) => renderSnippet(stateCell, row.original)
		}),
		helper.accessor(poolUsage, {
			id: 'capacity',
			header: 'Capacity',
			cell: ({ row }) => renderSnippet(capacityCell, row.original),
			enableGlobalFilter: false,
			sortFn: 'basic'
		}),
		helper.accessor(poolDeviceCount, {
			id: 'devices',
			header: 'Devices',
			enableGlobalFilter: false,
			sortFn: 'basic'
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

{#snippet nameCell(pool: Zpool)}
	<a
		href={poolHref(initial.hostname, pool.guid)}
		class="text-zen-highlight-foreground focus-visible:ring-ring block max-w-60 rounded-sm font-medium break-all underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none"
		>{pool.name}</a
	>
{/snippet}
{#snippet poolCell(pool: Zpool)}
	{@render nameCell(pool)}
	<p class="text-muted-foreground mt-1 text-xs">{poolLayout(pool)}</p>
{/snippet}
{#snippet stateCell(pool: Zpool)}<State value={pool.state} />{/snippet}
{#snippet capacityCell(pool: Zpool)}<Capacity {pool} />{/snippet}
{#snippet actionsHeader(_value: undefined)}
	<span class="sr-only">Actions</span>
{/snippet}
{#snippet rowActions(pool: Zpool)}
	{@const editReason = deleting
		? 'A pool is currently being deleted.'
		: poolEditBlockReason(pool, statuses[pool.guid] ?? null)}
	{@const deleteReason = deleting ? 'A pool is currently being deleted.' : deleteBlockReason(pool)}
	<div class="flex justify-end">
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={deleting}
						aria-label="Actions for {pool.name}"
						{...props}
					>
						<span class="icon-[lucide--ellipsis] size-5" aria-hidden="true"></span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content
				align="end"
				class="w-40"
				onCloseAutoFocus={(event) => {
					if (editOpen) event.preventDefault();
				}}
			>
				<DropdownMenu.Item
					class="cursor-pointer"
					onSelect={() => useSafeGoto(poolHref(initial.hostname, pool.guid))}
				>
					<span class="icon-[lucide--eye] size-4" aria-hidden="true"></span>View
				</DropdownMenu.Item>
				{#if canModify}
					{#if editReason}
						<Tooltip.Root delayDuration={500}>
							<Tooltip.Trigger>
								{#snippet child({ props })}
									<DropdownMenu.Item
										{...props}
										class="data-[disabled]:pointer-events-auto data-[disabled]:cursor-help"
										disabled
									>
										<span class="icon-[lucide--pencil] size-4" aria-hidden="true"></span>Edit
										<span class="sr-only">: {editReason}</span>
										<span class="icon-[lucide--info] ml-auto size-3.5" aria-hidden="true"></span>
									</DropdownMenu.Item>
								{/snippet}
							</Tooltip.Trigger>
							<Tooltip.Content
								side="left"
								sideOffset={8}
								class="z-60 max-w-64 text-left text-pretty"
							>
								{editReason}
							</Tooltip.Content>
						</Tooltip.Root>
					{:else}
						<DropdownMenu.Item class="cursor-pointer" onSelect={() => openEditor(pool)}>
							<span class="icon-[lucide--pencil] size-4" aria-hidden="true"></span>Edit
						</DropdownMenu.Item>
					{/if}
					<DropdownMenu.Separator />
					{#if deleteReason}
						<Tooltip.Root delayDuration={500}>
							<Tooltip.Trigger>
								{#snippet child({ props })}
									<DropdownMenu.Item
										{...props}
										variant="destructive"
										class="data-[disabled]:pointer-events-auto data-[disabled]:cursor-help dark:[--destructive:var(--color-red-300)] dark:data-[variant=destructive]:data-highlighted:bg-destructive/10"
										disabled
									>
										<span class="icon-[lucide--trash-2] size-4" aria-hidden="true"></span>Delete
										<span class="sr-only">: {deleteReason}</span>
										<span class="icon-[lucide--info] ml-auto size-3.5" aria-hidden="true"></span>
									</DropdownMenu.Item>
								{/snippet}
							</Tooltip.Trigger>
							<Tooltip.Content
								side="left"
								sideOffset={8}
								class="z-60 max-w-64 text-left text-pretty"
							>
								{deleteReason}
							</Tooltip.Content>
						</Tooltip.Root>
					{:else}
						<DropdownMenu.Item
							variant="destructive"
							class="cursor-pointer dark:[--destructive:var(--color-red-300)] dark:data-[variant=destructive]:data-highlighted:bg-destructive/10"
							onSelect={() => confirmDelete(pool)}
						>
							<span class="icon-[lucide--trash-2] size-4" aria-hidden="true"></span>Delete
						</DropdownMenu.Item>
					{/if}
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
{/snippet}
{#snippet mobileRow(row: Row<DataTableFeatures, Zpool>)}
	{@const count = poolDeviceCount(row.original)}
	<div class="mb-3 flex items-start justify-between gap-3">
		<div class="min-w-0">
			{@render nameCell(row.original)}
			<p class="text-muted-foreground mt-1 text-xs">
				{poolLayout(row.original)} · {count}
				{count === 1 ? 'Device' : 'Devices'}
			</p>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			<State value={row.original.state} />
			{@render rowActions(row.original)}
		</div>
	</div>
	<Capacity pool={row.original} />
{/snippet}

<svelte:head
	><title
		>{create ? 'New Pool · Pools' : selected ? `${selected.name} · Pools` : 'Storage Pools'} · {initial.hostname}</title
	></svelte:head
>

<div class="@container px-4 pt-3 pb-4 md:px-5 md:pb-5">
	{#if poolFailure}
		<div
			class="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2.5 text-sm"
			role="status"
		>
			<p>
				{pools.current.length > 0
					? 'Pools could not be refreshed. Showing the last successful list.'
					: 'Pools could not be loaded. Please try again.'}
			</p>
			<Button variant="outline" size="sm" onclick={refresh} disabled={busy}>Try Again</Button>
		</div>
	{/if}
	{#if create}
		<Create
			hostname={initial.hostname}
			pools={pools.current}
			disks={disks.current}
			inventoryUnavailable={diskFailure}
			{canModify}
			onCreated={poolCreated}
		/>
	{:else if selectedGUID && selected}
		{#key selectedGUID}
			<Details
				hostname={initial.hostname}
				pool={selected}
				pools={pools.current}
				disks={disks.current}
				inventoryUnavailable={diskFailure}
				refreshPools={refresh}
				refreshing={busy}
				initialStatus={statuses[selectedGUID] ?? null}
				onStatus={rememberStatus}
			/>
		{/key}
	{:else if selectedGUID && !poolFailure}
		<div class="flex min-h-64 flex-col items-center justify-center gap-3 text-center">
			<span class="icon-[bi--hdd-stack-fill] text-muted-foreground size-8" aria-hidden="true"
			></span>
			<h1 class="text-lg font-semibold">Pool Not Found</h1>
			<p class="text-muted-foreground text-sm">
				This pool is no longer available on this node, or you do not have access to it.
			</p>
			<Button variant="outline" href={poolHref(initial.hostname)}>Back to Pools</Button>
		</div>
	{/if}
	<div hidden={create || Boolean(selectedGUID && (!poolFailure || selected))}>
		<header class="mb-5 flex flex-wrap items-center justify-between gap-3">
			<div class="flex min-w-0 items-center gap-3">
				<span
					class="bg-zen-highlight text-zen-highlight-foreground flex size-9 shrink-0 items-center justify-center rounded-lg"
					aria-hidden="true"><span class="icon-[bi--hdd-stack-fill] size-5"></span></span
				>
				<Breadcrumb.Root class="min-w-0">
					<Breadcrumb.List class="flex-nowrap items-baseline gap-2 sm:gap-2">
						<Breadcrumb.Item>
							<span class="text-lg font-medium tracking-tight sm:text-xl">Storage</span>
						</Breadcrumb.Item>
						<Breadcrumb.Separator class="text-muted-foreground/60 self-center" />
						<Breadcrumb.Item class="min-w-0">
							<h1 class="truncate text-lg leading-7 tracking-tight sm:text-xl">
								<Breadcrumb.Page class="font-semibold">Pools</Breadcrumb.Page>
							</h1>
						</Breadcrumb.Item>
					</Breadcrumb.List>
				</Breadcrumb.Root>
			</div>
			<div class="flex items-center gap-2">
				{#if canModify}
					<Button class="h-9 gap-2" href={poolCreateHref(initial.hostname)}>
						<span class="icon-[lucide--plus] size-4" aria-hidden="true"></span>New Pool
					</Button>
				{/if}
				<Button
					variant="outline"
					size="icon"
					class="size-9"
					onclick={refresh}
					disabled={busy}
					aria-label="Refresh pools"
					><span
						class="icon-[lucide--refresh-cw] size-4"
						class:animate-spin={busy}
						aria-hidden="true"
					></span></Button
				>
			</div>
		</header>
		{#if !poolFailure || pools.current.length > 0}
			<DataTable
				data={pools.current}
				{columns}
				columnClasses={{
					capacity: 'pr-8',
					devices: 'w-32 pr-6 text-right tabular-nums',
					actions: 'w-14 pr-3'
				}}
				getRowId={(pool) => pool.guid}
				getRowLabel={(pool) => pool.name}
				initialSorting={[{ id: 'capacity', desc: true }]}
				loading={pools.loading}
				searchPlaceholder="Search pools…"
				emptyMessage="No imported storage pools."
				{mobileRow}
			/>
		{/if}
	</div>
</div>

{#if editOpen && canModify && editTarget}
	{#key editTarget.guid}
		<Edit
			bind:open={editOpen}
			hostname={initial.hostname}
			pool={editTarget}
			pools={pools.current}
			disks={diskFailure ? null : disks.current}
			status={statuses[editTarget.guid] ?? null}
			onChanged={refresh}
		/>
	{/key}
{/if}

<AlertDialog.Root bind:open={deleteOpen}>
	<AlertDialog.Content
		class="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 overflow-hidden p-0"
		escapeKeydownBehavior={deleting ? 'ignore' : 'close'}
		aria-busy={deleting}
	>
		<div class="flex shrink-0 items-start justify-between gap-3 border-b px-4 pt-4 pb-3">
			<AlertDialog.Header class="min-w-0 text-left">
				<AlertDialog.Title class="flex min-h-9 items-center gap-2.5 text-base leading-snug">
					<span class="icon-[lucide--trash-2] text-destructive size-5 shrink-0" aria-hidden="true"
					></span>
					Delete Pool?
				</AlertDialog.Title>
			</AlertDialog.Header>
			<AlertDialog.Cancel disabled={deleting}>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon"
						class="size-9 shrink-0"
						disabled={deleting}
					>
						<span class="icon-[lucide--x] size-5" aria-hidden="true"></span>
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</AlertDialog.Cancel>
		</div>
		<div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
			<AlertDialog.Description class="w-full text-left leading-relaxed text-pretty break-words">
				Deleting
				<span class="text-foreground text-sm font-semibold break-all">{deleteTarget?.name}</span>
				on <span class="text-foreground text-sm font-semibold break-all">{initial.hostname}</span>
				permanently removes all its datasets, volumes, snapshots, and stored data. This cannot be undone.
			</AlertDialog.Description>
			<div class="space-y-2">
				<Label for="{id}-delete-name" class="block leading-relaxed">
					Type <span class="text-foreground text-sm font-semibold break-all"
						>{deleteTarget?.name}</span
					> to confirm
				</Label>
				<Input
					id="{id}-delete-name"
					bind:value={deleteName}
					placeholder={deleteTarget?.name}
					autocomplete="off"
					spellcheck={false}
					disabled={deleting}
				/>
			</div>
			{#if deleteBlocked}<p class="text-destructive text-sm" role="alert">{deleteBlocked}</p>{/if}
			{#if !canModify}
				<p class="text-destructive text-sm" role="alert">
					You do not have permission to delete pools on this node.
				</p>
			{/if}
			{#if deleteError}<p class="text-destructive text-sm" role="alert">{deleteError}</p>{/if}
		</div>
		<AlertDialog.Footer class="shrink-0 border-t p-4">
			<Button
				variant="destructive"
				class="gap-2"
				disabled={deleting || !canConfirmDelete}
				onclick={deleteConfirmed}
			>
				{#if deleting}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
				{/if}
				{deleting ? 'Deleting…' : 'Delete Pool'}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
