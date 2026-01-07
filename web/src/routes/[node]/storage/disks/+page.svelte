<script lang="ts">
	import { destroyDisk, destroyPartition, initializeGPT, listDisks } from '$lib/api/disk/disk';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import KvTableModal from '$lib/components/custom/KVTableModal.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import CreatePartition from '$lib/components/disk/CreatePartition.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import { type Disk, type Partition } from '$lib/types/disk/disk';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { diskSpaceAvailable, generateTableData, parseSMART } from '$lib/utils/disk';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { resource, watch } from 'runed';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Data {
		disks: Disk[];
		pools: Zpool[];
	}

	let { data }: { data: Data } = $props();
	const disks = resource(
		() => 'disks',
		async (key, prevKey, { signal }) => {
			const result = await listDisks();
			updateCache('disk-list', result);
			return result;
		},
		{
			initialValue: data.disks
		}
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				disks.refetch();
				reload = false;
			}
		}
	);

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let { rows, columns } = $derived(generateTableData(disks.current));

	let wipeModal = $state({
		open: false,
		title: ''
	});

	let partitionModal: {
		open: boolean;
		disk: Disk | null;
	} = $state({
		open: false,
		disk: null
	});

	let smartModal = $state({
		open: false,
		title: '',
		KV: {},
		type: ''
	});

	let activeDisk: Disk | null = $derived.by(() => {
		if (activeRow !== null) {
			return disks.current.find((disk) => disk.device === activeRow?.device) || null;
		}
		return null;
	});

	let activePartition: Partition | null = $derived.by(() => {
		if (activeRow !== null) {
			const partition = disks.current.filter((disk) => {
				return disk.partitions.some((part) => part.name === activeRow?.device);
			});

			if (partition.length > 0) {
				return partition[0].partitions.find((part) => part.name === activeRow?.device) || null;
			} else {
				return null;
			}
		}
		return null;
	});

	async function diskAction(action: string) {
		if (action === 'smart') {
			if (activeDisk) {
				smartModal.open = false;
				smartModal.title = `S.M.A.R.T Values (${activeDisk.device})`;
				if (activeDisk.type === 'NVMe') {
					smartModal.KV = parseSMART($state.snapshot(activeDisk));
					smartModal.open = true;
					smartModal.type = 'kv';
				} else if (activeDisk.type === 'HDD' || activeDisk.type === 'SSD') {
					smartModal.KV = parseSMART($state.snapshot(activeDisk));
					smartModal.open = true;
					smartModal.type = 'array';
				}
			}
		}

		if (action === 'wipe') {
			wipeModal.open = true;
			if (activePartition !== null) {
				wipeModal.title = `This action cannot be undone. This will permanently <b>delete</b> partition <b>${activePartition.name}</b>.`;
			} else if (activeDisk !== null) {
				wipeModal.title = `This action cannot be undone. This will permanently <b>wipe</b> disk <b>${activeDisk.device}</b>.`;
			}
		}

		if (action === 'gpt') {
			if (activeDisk) {
				const response = await initializeGPT(activeDisk.device);
				disks.refetch();
				if (response.status === 'success') {
					toast.success(`Disk ${activeDisk.device} initialized with GPT`, {
						position: 'bottom-center'
					});
				} else {
					handleAPIError(response);
				}
			}
		}

		if (action === 'partition') {
			partitionModal.open = true;
			partitionModal.disk = activeDisk;
		}
	}

	let buttonAbilities = $state({
		smart: {
			ability: false
		},
		gpt: {
			ability: false
		},
		wipe: {
			ability: false
		},
		createPartition: {
			ability: false
		}
	});

	$effect(() => {
		if (activeDisk) {
			untrack(() => {
				buttonAbilities.smart.ability = activeDisk.smartData !== null;
				buttonAbilities.gpt.ability = !activeDisk.gpt;

				if (activeDisk.usage === 'ZFS') {
					buttonAbilities.gpt.ability = false;
					buttonAbilities.wipe.ability = false;
				}

				if (activeDisk.usage === 'Unused' || activeDisk.usage === 'Partitions') {
					if (activeDisk.gpt) {
						buttonAbilities.wipe.ability = true;
					} else {
						buttonAbilities.wipe.ability = false;
					}
				}

				buttonAbilities.createPartition.ability =
					activeDisk.gpt &&
					diskSpaceAvailable(activeDisk, 128 * 1024 * 1024) &&
					activeDisk.usage !== 'ZFS';
			});
		} else if (activePartition) {
			untrack(() => {
				buttonAbilities.gpt.ability = false;
				buttonAbilities.wipe.ability = true;
				buttonAbilities.createPartition.ability = false;
				buttonAbilities.smart.ability = false;
			});
		} else {
			untrack(() => {
				buttonAbilities.gpt.ability = false;
				buttonAbilities.wipe.ability = false;
				buttonAbilities.createPartition.ability = false;
				buttonAbilities.smart.ability = false;
			});
		}
	});

	let query = $state('');
</script>

{#snippet button(type: string)}
	{#if type == 'smart' && buttonAbilities.smart.ability}
		<Button onclick={() => diskAction('smart')} size="sm" variant="outline" class="h-6.5">
			<div class="flex items-center">
				<span class="icon-[icon-park-outline--hdd] mr-1 h-4 w-4"></span>

				<span>S.M.A.R.T Values</span>
			</div>
		</Button>
	{/if}

	{#if type == 'gpt' && buttonAbilities.gpt.ability}
		<Button onclick={() => diskAction('gpt')} size="sm" variant="outline" class="h-6.5">
			<div class="flex items-center">
				<span class="icon-[carbon--logical-partition] mr-1 h-4 w-4"></span>
				<span>Initialize GPT</span>
			</div>
		</Button>
	{/if}

	{#if type == 'wipe-disk' && buttonAbilities.wipe.ability && activeDisk !== null}
		<Button onclick={() => diskAction('wipe')} size="sm" variant="outline" class="h-6.5">
			<div class="flex items-center">
				<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
				<span>Wipe Disk</span>
			</div>
		</Button>
	{/if}

	{#if type == 'wipe-partition' && buttonAbilities.wipe.ability && activePartition !== null}
		<Button onclick={() => diskAction('wipe')} size="sm" variant="outline" class="h-6.5">
			<div class="flex items-center">
				<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
				<span>Delete Partition</span>
			</div>
		</Button>
	{/if}

	{#if type == 'partition' && buttonAbilities.createPartition.ability}
		<Button onclick={() => diskAction('partition')} size="sm" variant="outline" class="h-6.5">
			<div class="flex items-center">
				<span class="icon-[ant-design--partition-outlined] mr-1 h-4 w-4"></span>
				<span>Create Partition</span>
			</div>
		</Button>
	{/if}
{/snippet}

<div class="flex h-full flex-col overflow-hidden">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		{@render button('smart')}
		{@render button('gpt')}
		{@render button('partition')}
		{@render button('wipe-disk')}
		{@render button('wipe-partition')}
	</div>

	<KvTableModal
		titles={{
			main: smartModal.title,
			key: 'Attribute',
			value: 'Value'
		}}
		bind:open={smartModal.open}
		KV={smartModal.KV}
	></KvTableModal>

	<TreeTable
		data={{
			rows: rows,
			columns: columns
		}}
		name={'tt-disks'}
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

<AlertDialog
	open={wipeModal.open}
	names={{ parent: 'disks', element: wipeModal.title || '' }}
	actions={{
		onConfirm: async () => {
			if (activeDisk || activePartition) {
				const message = activeDisk ? 'Disk Wiped' : 'Partition Deleted';

				const result = activeDisk
					? await destroyDisk(`/dev/${activeDisk.device}`)
					: await destroyPartition(`/dev/${activePartition?.name}`);

				disks.refetch();
				if (result.status === 'success') {
					toast.success(message, { position: 'bottom-center' });
					activeRow = null;
				} else {
					handleAPIError(result);
					if (
						(result.status === 'error' && result.message === 'error_wiping_disk') ||
						result.message === 'error_deleting_partition'
					) {
						let message = '';
						if (result.error?.includes('Device busy')) {
							if (activeDisk) {
								message = 'Unable to wipe busy disk';
							} else {
								message = 'Unable to delete busy partition';
							}
						} else {
							message = `Error ${activeDisk ? 'wiping disk' : 'deleting partition'}: ${result.error}`;
						}

						toast.error(message, { position: 'bottom-center' });
					}
				}
			}
			wipeModal.title = '';
			wipeModal.open = false;
		},
		onCancel: () => {
			wipeModal.title = '';
			wipeModal.open = false;
		}
	}}
	customTitle={wipeModal.title}
></AlertDialog>

<CreatePartition
	open={partitionModal.open}
	disk={partitionModal.disk}
	onCancel={() => {
		partitionModal.open = false;
		partitionModal.disk = null;
	}}
	bind:reload
/>
