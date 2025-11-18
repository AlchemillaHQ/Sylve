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
	import type { APIResponse } from '$lib/types/common';
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
	import { useQueries } from '@sveltestack/svelte-query';
	import { toast } from 'svelte-sonner';

	interface Data {
		disks: Disk[];
		pools: Zpool[];
	}

	let { data }: { data: Data } = $props();

	const results = useQueries([
		{
			queryKey: ['diskList'],
			queryFn: async () => {
				return await listDisks();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.disks,
			onSuccess: (data: Disk[]) => {
				updateCache('disks', data);
			}
		},
		{
			queryKey: ['poolList'],
			queryFn: async () => {
				let pools = await getPools();

				if (pools.length === 0) {
					return data.pools;
				}

				return pools;
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.pools,
			onSuccess: (data: Zpool[]) => {
				updateCache('pools', data);
			}
		}
	]);

	let disks = $derived($results[0].data as Disk[]);
	let pools = $derived($results[1].data as Zpool[]);

	let tableData = $derived(generateTableData(pools, disks));
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let activePool: Zpool | null = $derived(
		activeRow && isPool(pools, activeRow.name)
			? pools.find((p) => p.guid === activeRow.guid) || null
			: null
	);

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

	let usable = $derived({
		disks: zpoolUseableDisks(disks, pools),
		partitions: zpoolUseablePartitions(disks, pools)
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

	export function parsePoolActionError(error: APIResponse): string {
		if (error.message && error.message === 'pool_create_failed') {
			if (error.error) {
				if (error.error.includes('mirror contains devices of different sizes')) {
					return 'Pool contains a mirror with devices of different sizes';
				} else if (error.error.includes('raidz contains devices of different sizes')) {
					return 'Pool contains a RAIDZ vdev with devices of different sizes';
				}
			}
		}

		if (error.message && error.message === 'pool_delete_failed') {
			if (error.error) {
				if (error.error.includes('pool or dataset is busy')) {
					return 'Pool is busy';
				}

				if (
					error.error.startsWith('dataset ') &&
					error.error.endsWith('is in use and cannot be deleted')
				) {
					return 'Pool has a dataset that is in use by a VM or Jail';
				}
			}
		}

		if (error.message && error.message === 'pool_edit_failed') {
			if (error.error) {
				if (error.error.startsWith('spare_device') && error.error.includes('is too small')) {
					return 'Spare device is too small';
				}
			}

			return 'Pool edit failed';
		}

		return '';
	}
</script>

{#snippet button(type: string)}
	{#if activeRow && Object.keys(activeRow).length > 0}
		{#if isPool(pools, activeRow.name)}
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
				{#if isPool(pools, activeRow.name)}
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
			{#if isReplaceableDevice(pools, activeRow.name)}
				<Button
					onclick={() => {
						let pool = getPoolByDevice(pools, activeRow.name);

						modals.replace.open = true;
						modals.replace.data = {
							pool: pool ? pools.find((p) => p.name === pool) || null : null,
							old: activeRow.name as string,
							latest: ''
						};
					}}
					size="sm"
					class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
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

<Status bind:open={modals.status.open} pool={activePool} />

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

<Create bind:open={modals.create.open} {usable} {disks} {pools} {parsePoolActionError} />

{#if activePool}
	<Edit bind:open={modals.edit.open} pool={activePool} {usable} {parsePoolActionError} />
{/if}
