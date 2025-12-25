<script lang="ts">
	import { handleAPIResponse } from '$lib/api/common';
	import { listDisks } from '$lib/api/disk/disk';
	import { deletePool, getPools, scrubPool } from '$lib/api/zfs/pool';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Create from '$lib/components/custom/ZFS/pools/Create.svelte';
	import Edit from '$lib/components/custom/ZFS/pools/Edit.svelte';
	import Replace from '$lib/components/custom/ZFS/pools/Replace.svelte';
	import Status from '$lib/components/custom/ZFS/pools/Status.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Disk } from '$lib/types/disk/disk';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { deepSearchKey } from '$lib/utils/arr';
	import { zpoolUseableDisks, zpoolUseablePartitions } from '$lib/utils/disk';
	import { updateCache } from '$lib/utils/http';
	import {
		generateTableData,
		getPoolByDevice,
		isPool,
		isReplaceableDevice
	} from '$lib/utils/zfs/pool';
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
			initialValue: data.pools
		}
	);

	const disks = resource(
		() => 'disk-list',
		async () => {
			const disks = await listDisks();
			updateCache('disk-list', disks);
			return disks;
		},
		{
			initialValue: data.disks
		}
	);

	useInterval(2000, {
		callback: async () => {
			if (visible.current && !storage.idle) {
				pools.refetch();
			}
		}
	});

	useInterval(5000, {
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
		if (activeRow && isPool(pools.current, activeRow.name)) {
			return pools.current.find((p) => p.pool_guid === activeRow.guid) || null;
		} else {
			return null;
		}
	});

	let replacing = $derived.by(() => {
		if (tableData.rows.length > 0) {
			const names = deepSearchKey(tableData.rows, 'name');
			if (names.some((name) => name.includes('[OLD]') || name.includes('[NEW]'))) {
				return true;
			} else {
				return false;
			}
		}

		return false;
	});

	let scrubbing = $derived.by(() => {
		if (JSON.stringify(pools).toLowerCase().includes('scrub in progress since')) {
			return true;
		} else {
			return false;
		}
	});

	let usable = $derived.by(() => {
		return {
			disks: zpoolUseableDisks(disks.current, pools.current),
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
		replace: {
			open: false,
			data: {
				pool: null as Zpool | null,
				old: '',
				latest: ''
			}
		}
	});
</script>

{#snippet button(type: string)}
	{#if activeRow && Object.keys(activeRow).length > 0}
		{#if isPool(pools.current, activeRow.name)}
			{#if type === 'pool-status'}
				<Button
					onclick={() => {
						modals.status.open = true;
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<div class="flex items-center">
						<span class="icon-[mdi--eye] mr-1 h-4 w-4"></span>

						<span>Status</span>
					</div>
				</Button>
			{/if}

			{#if type === 'pool-scrub'}
				{#if isPool(pools.current, activeRow.name)}
					<Button
						onclick={async () => {
							const response = await scrubPool(activeRow?.guid);
							if (response.status === 'error') {
								toast.error(parsePoolActionError(response), {
									position: 'bottom-center'
								});
							} else {
								toast.success('Scrub started', {
									position: 'bottom-center'
								});
							}
						}}
						size="sm"
						variant="outline"
						class="h-6.5"
						disabled={scrubbing}
						title={scrubbing ? 'A scrub is already in progress' : ''}
					>
						<div class="flex items-center">
							<span class="icon-[cil--scrubber] mr-1 h-4 w-4"></span>

							<span>Scrub</span>
						</div>
					</Button>
				{/if}
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
					<div class="flex items-center">
						<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>

						<span>Edit</span>
					</div>
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
					disabled={replacing}
					title={replacing ? 'Please wait for the current replace operation to finish' : ''}
				>
					<div class="flex items-center">
						<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
						<span>Delete</span>
					</div>
				</Button>
			{/if}
		{/if}

		{#if type === 'pool-replace'}
			{#if isReplaceableDevice(pools.current, activeRow.name) && usable.disks.length + usable.partitions.length > 0}
				<Button
					onclick={() => {
						let pool = getPoolByDevice(pools.current, activeRow.name);
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
					disabled={replacing}
					title={replacing ? 'Replace already in progress' : ''}
				>
					<div class="flex items-center">
						<span class="icon-[mdi--swap-horizontal] mr-1 h-4 w-4"></span>

						<span>Replace Device</span>
					</div>
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
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{@render button('pool-status')}
		{@render button('pool-scrub')}
		{@render button('pool-edit')}
		{@render button('pool-delete')}
		{@render button('pool-replace')}
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

<!-- Delete -->
<AlertDialog
	open={modals.delete.open}
	names={{
		parent: 'ZFS Pool',
		element: activeRow ? (activeRow.name as string) : ''
	}}
	actions={{
		onConfirm: async () => {
			modals.delete.open = false;
			let pool = $state.snapshot(activePool);
			let response = await deletePool(pool?.guid as string);
			reload = true;
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
