<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { page } from '$app/state';
	import { handleAPIResponse } from '$lib/api/common';
	import { listDisks } from '$lib/api/disk/disk';
	import {
		deletePool,
		detachDevice,
		getPools,
		getPoolStatusResult,
		listPoolScrubRuns,
		listPoolScrubSchedules
	} from '$lib/api/zfs/pool';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Create from '$lib/components/custom/ZFS/pools/Create.svelte';
	import Edit from '$lib/components/custom/ZFS/pools/Edit.svelte';
	import Replace from '$lib/components/custom/ZFS/pools/Replace.svelte';
	import Status from '$lib/components/custom/ZFS/pools/Status.svelte';
	import Scrub from '$lib/components/custom/ZFS/pools/Scrub.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Disk } from '$lib/types/disk/disk';
	import { isPoolScrubActive, type Zpool } from '$lib/types/zfs/pool';
	import { deepSearchKey } from '$lib/utils/arr';
	import { zpoolUseableDisks, zpoolUseablePartitions } from '$lib/utils/disk';
	import { getAPIErrorText, isAPIResponse, updateCache } from '$lib/utils/http';
	import {
		generateTableData,
		getPoolByDevice,
		isPool,
		isPoolScanActive,
		isReplaceableDevice
	} from '$lib/utils/zfs/pool';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { IsDocumentVisible, resource, useInterval, watch } from 'runed';
	import { storage } from '$lib';
	import { parsePoolActionError } from '$lib/utils/zfs/pool.svelte';

	interface Data {
		disks: Disk[];
		pools: Zpool[];
	}

	let visible = new IsDocumentVisible();
	let reload = $state(false);

	let { data }: { data: Data } = $props();

	const pools = resource(
		() => 'pool-list',
		async () => {
			const pools = await getPools(false);
			updateCache('pool-list', pools);
			return pools;
		},
		{
			initialValue: untrack(() => data.pools)
		}
	);

	const disks = resource(
		() => 'disk-list-inventory',
		async () => {
			const disks = await listDisks('none');
			updateCache('disk-list-inventory', disks);
			return disks;
		},
		{
			initialValue: untrack(() => data.disks)
		}
	);

	useInterval(2000, {
		callback: async () => {
			if (visible.current && !storage.idle) {
				pools.refetch();
			}
		}
	});

	useInterval(60000, {
		callback: async () => {
			if (visible.current && !storage.idle) {
				disks.refetch();
			}
		}
	});

	watch(
		() => reload,
		(value) => {
			if (value) {
				pools.refetch();
				disks.refetch();
				reload = false;
			}
		}
	);

	let tableData = $derived(generateTableData(pools.current, disks.current));
	let activeRows = $state<Row[] | null>(null);
	let activeRow: Row | null = $derived(
		activeRows && Array.isArray(activeRows) && activeRows.length > 0 ? (activeRows[0] as Row) : null
	);

	let activePool: Zpool | null = $derived.by(() => {
		if (activeRow && typeof activeRow.name === 'string' && isPool(pools.current, activeRow.name)) {
			return pools.current.find((p) => p.pool_guid === activeRow.guid) || null;
		} else {
			return null;
		}
	});

	let replacing = $derived.by(() => {
		if (tableData.rows.length > 0) {
			const names = deepSearchKey(tableData.rows, 'name');
			if (names.every((name) => typeof name === 'string')) {
				if (names.some((name) => name.includes('[OLD]') || name.includes('[NEW]'))) {
					return true;
				} else {
					return false;
				}
			}
		}

		return false;
	});

	let selectedMutationPool = $derived.by(() => {
		if (activePool) return activePool;
		const name = activeRow ? getPoolByDevice(pools.current, String(activeRow.name || '')) : null;
		return pools.current.find((candidate) => candidate.name === name) || null;
	});
	const poolActivity = resource(
		() => `${page.params.node || ''}|${selectedMutationPool?.pool_guid || ''}`,
		async (key) => {
			const [hostname, guid] = key.split('|');
			if (!guid) return null;
			const options = { hostname };
			const [status, schedules, runs] = await Promise.all([
				getPoolStatusResult(guid, options),
				listPoolScrubSchedules(options),
				listPoolScrubRuns(guid, options)
			]);
			const poolStatus = status && !isAPIResponse(status) ? status : null;
			const scrubSchedules = Array.isArray(schedules) ? schedules : [];
			const scrubRuns = Array.isArray(runs) ? runs : [];
			return {
				hostname,
				poolGuid: guid,
				status: poolStatus,
				schedules: scrubSchedules,
				runs: scrubRuns,
				active:
					isPoolScanActive(poolStatus?.scan_stats) ||
					scrubSchedules.some(
						(schedule) => schedule.poolGuid === guid && isPoolScrubActive(schedule.lastStatus)
					) ||
					scrubRuns.some((run) => isPoolScrubActive(run.status))
			};
		},
		{ initialValue: null }
	);
	let scrubbing = $derived(poolActivity.current?.active || false);
	useInterval(15000, {
		callback: () => {
			if (visible.current && !storage.idle && selectedMutationPool) poolActivity.refetch();
		}
	});

	let usable = $derived.by(() => {
		return {
			disks: zpoolUseableDisks(disks.current),
			partitions: zpoolUseablePartitions(disks.current, pools.current)
		};
	});

	let query = $state('');
	let modals = $state({
		create: {
			open: false
		},
		edit: {
			open: false
		},
		delete: {
			open: false
		},
		status: {
			open: false
		},
		scrub: {
			open: false,
			pool: null as Zpool | null
		},
		replace: {
			open: false,
			data: {
				pool: null as Zpool | null,
				old: '',
				latest: ''
			}
		},
		detach: {
			open: false
		}
	});
	watch(
		() => page.params.node,
		() => {
			modals.scrub.open = false;
		}
	);
</script>

{#snippet button(type: string)}
	{#if activeRow && Object.keys(activeRow).length > 0}
		{#if typeof activeRow.name === 'string' && isPool(pools.current, activeRow.name)}
			{#if type === 'pool-status'}
				<Button
					onclick={() => {
						modals.status.open = true;
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<SpanWithIcon icon="icon-[mdi--eye]" size="h-4 w-4" gap="gap-1" title="Status" />
				</Button>
			{/if}

			{#if type === 'pool-scrub' && activePool}
				<Button
					onclick={() => {
						modals.scrub = { open: true, pool: activePool };
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<SpanWithIcon icon="icon-[cil--scrubber]" size="h-4 w-4" gap="gap-1" title="Scrub" />
				</Button>
			{/if}

			{#if type === 'pool-edit'}
				<Button
					onclick={() => {
						modals.edit.open = true;
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
					disabled={replacing || scrubbing}
					title={replacing || scrubbing
						? 'Please wait for the scrub/replace operation to finish'
						: ''}
				>
					<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit" />
				</Button>
			{/if}

			{#if type === 'pool-delete'}
				<Button
					onclick={() => {
						modals.delete.open = true;
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
					disabled={replacing || scrubbing}
					title={replacing || scrubbing
						? 'Please wait for the current scan/replace operation to finish'
						: ''}
				>
					<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-1" title="Delete" />
				</Button>
			{/if}
		{/if}

		{#if type === 'pool-replace'}
			{#if typeof activeRow.name === 'string' && isReplaceableDevice(pools.current, activeRow.name) && usable.disks.length + usable.partitions.length > 0}
				<Button
					onclick={() => {
						let pool = getPoolByDevice(pools.current, activeRow.name as string);
						modals.replace.open = true;
						modals.replace.data = {
							pool: pool ? pools.current.find((p) => p.name === pool) || null : null,
							old: activeRow.name as string,
							latest: ''
						};
					}}
					variant="outline"
					size="sm"
					class="h-6.5"
					disabled={replacing || scrubbing}
					title={replacing || scrubbing ? 'A scan or replacement is already in progress' : ''}
				>
					<SpanWithIcon
						icon="icon-[mdi--swap-horizontal]"
						size="h-4 w-4"
						gap="gap-1"
						title="Replace Device"
					/>
				</Button>
			{/if}
		{/if}

		{#if type === 'pool-detach'}
			{#if typeof activeRow.name === 'string' && isReplaceableDevice(pools.current, activeRow.name)}
				<Button
					onclick={() => {
						modals.detach.open = true;
					}}
					variant="outline"
					size="sm"
					class="h-6.5"
					disabled={replacing || scrubbing}
					title={replacing || scrubbing
						? 'Please wait for the current scan/replace operation to finish'
						: ''}
				>
					<SpanWithIcon
						icon="icon-[mdi--link-variant-off]"
						size="h-4 w-4"
						gap="gap-1"
						title="Detach Device"
					/>
				</Button>
			{/if}
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => (modals.create.open = !modals.create.open)}
			size="sm"
			class="h-6"
			disabled={replacing}
			title={replacing ? 'Please wait for the current replace operation to finish' : ''}
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-1" title="New" />
		</Button>

		{@render button('pool-status')}
		{@render button('pool-scrub')}
		{@render button('pool-edit')}
		{@render button('pool-delete')}
		{@render button('pool-replace')}
		{@render button('pool-detach')}
	</div>

	<TreeTable
		data={tableData}
		name="tt-zfsPool"
		bind:parentActiveRow={activeRows}
		bind:query
		multipleSelect={false}
	/>
</div>

{#if activePool}
	<Status bind:open={modals.status.open} pool={activePool} />
{/if}

{#if modals.scrub.pool}
	<Scrub
		bind:open={modals.scrub.open}
		pool={modals.scrub.pool}
		pools={pools.current}
		initialSnapshot={poolActivity.current ?? undefined}
	/>
{/if}

<!-- Delete -->
<AlertDialog
	open={modals.delete.open}
	names={{
		parent: 'ZFS Pool',
		element: activeRow ? (activeRow.name as string) : ''
	}}
	actions={{
		onConfirm: async () => {
			const pool = $state.snapshot(activePool);
			const response = await deletePool(pool?.guid as string);
			reload = true;
			if (response.status === 'success') modals.delete.open = false;
			handleAPIResponse(response, {
				success: `Pool ${pool?.name} deleted`,
				error: parsePoolActionError(response)
			});
		},
		onCancel: () => {
			modals.delete.open = false;
		}
	}}
/>

<!-- Detach -->
<AlertDialog
	open={modals.detach.open}
	names={{
		parent: 'ZFS Pool',
		element: activeRow ? (activeRow.name as string) : ''
	}}
	customTitle={`This will detach <span class='font-semibold'>${activeRow?.name}</span> from the pool.`}
	confirmLabel="Detach"
	actions={{
		onConfirm: async () => {
			const device = String(activeRow?.name || '');
			const pool = getPoolByDevice(pools.current, device);
			if (!pool) return;
			const poolObj = pools.current.find((p) => p.name === pool);
			if (!poolObj) return;
			const response = await detachDevice(poolObj.guid, device);
			if (response.status === 'error') {
				toast.error(getAPIErrorText(response, 'Detach failed'), { position: 'bottom-center' });
			} else {
				modals.detach.open = false;
				toast.success(`Detached ${device}`, { position: 'bottom-center' });
			}
		},
		onCancel: () => {
			modals.detach.open = false;
		}
	}}
/>

{#if modals.replace.data.pool}
	<Replace
		bind:open={modals.replace.open}
		bind:replacing
		pool={modals.replace.data.pool}
		old={activeRow ? (activeRow.name as string) : ''}
		latest={modals.replace.data.latest}
		{usable}
	/>
{/if}

{#if activePool && modals.edit.open}
	<Edit bind:open={modals.edit.open} pool={activePool} {usable} bind:reload />
{/if}

{#if modals.create.open}
	<Create
		bind:open={modals.create.open}
		{usable}
		disks={disks.current}
		pools={pools.current}
		bind:reload
	/>
{/if}
