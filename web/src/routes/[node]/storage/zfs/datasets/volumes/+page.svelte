<script lang="ts">
	import { getBasicSettings } from '$lib/api/system/settings';
	import { getDownloads } from '$lib/api/utilities/downloader';
	import { bulkDelete, deleteVolume, getDatasets } from '$lib/api/zfs/datasets';
	import AlertDialogModal from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import CreateVolume from '$lib/components/custom/ZFS/datasets/volumes/Create.svelte';
	import EditVolume from '$lib/components/custom/ZFS/datasets/volumes/Edit.svelte';
	import FlashFile from '$lib/components/custom/ZFS/datasets/volumes/FlashFile.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { BasicSettings } from '$lib/types/system/settings';
	import type { Download } from '$lib/types/utilities/downloader';
	import { GZFSDatasetTypeSchema, type Dataset, type GroupedByPool } from '$lib/types/zfs/dataset';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { groupByPoolNames } from '$lib/utils/zfs/dataset/dataset';
	import { generateTableData } from '$lib/utils/zfs/dataset/volume';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		datasets: Dataset[];
		downloads: Download[];
		settings: BasicSettings;
	}

	let { data }: { data: Data } = $props();
	let tableName = 'tt-zfsVolumes';

	const datasets = resource(
		() => 'zfs-volumes',
		async () => {
			const datasets = await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME);
			updateCache('zfs-volumes', datasets);
			return datasets;
		},
		{
			initialValue: data.datasets
		}
	);

	const downloads = resource(
		() => 'downloads',
		async () => {
			const downloads = await getDownloads();
			updateCache('downloads', downloads);
			return downloads;
		},
		{
			initialValue: data.downloads
		}
	);

	const pools = resource(
		() => 'basic-settings',
		async () => {
			const settings = await getBasicSettings();
			updateCache('basic-settings', settings);
			return settings.pools;
		},
		{
			initialValue: data.settings.pools
		}
	);

	let reload = $state(false);
	$effect(() => {
		if (reload) {
			datasets.refetch();
			downloads.refetch();
			pools.refetch();

			reload = false;
		}
	});

	let grouped: GroupedByPool[] = $derived(groupByPoolNames(pools.current, datasets.current));
	let table: {
		rows: Row[];
		columns: Column[];
	} = $derived(generateTableData(grouped));

	let activeRows = $state<Row[] | null>(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let activePool: string | null = $derived.by(() => {
		const pool = pools.current.find((pool) => pool === activeRow?.name);
		return pool ?? null;
	});

	let activeDatasets: Dataset[] = $derived.by(() => {
		if (activeRows) {
			let datasets: Dataset[] = [];
			for (const row of activeRows) {
				for (const dataset of grouped) {
					const volumes = dataset.volumes;
					const snapshots = dataset.snapshots;

					for (const vol of volumes) {
						if (vol.name === row.name) {
							datasets.push(vol);
						}
					}

					for (const snap of snapshots) {
						if (snap.name === row.name) {
							datasets.push(snap);
						}
					}
				}
			}
			return datasets;
		}

		return [];
	});

	let activeVolume: Dataset | null = $derived.by(() => {
		if (activePool) return null;
		const volumes = datasets.current.filter(
			(volume) => volume.type === GZFSDatasetTypeSchema.enum.VOLUME
		);
		const volume = volumes.find((volume) => volume.name.endsWith(activeRow?.name));
		return volume ?? null;
	});

	let poolsSelected = $derived.by(() => {
		if (activeRows && activeRows.length > 0) {
			const filtered = activeRows.filter((row) => {
				return row.type === 'pool';
			});

			return filtered.length > 0;
		}

		return false;
	});

	let modals = $state({
		volume: {
			flash: {
				open: false
			},
			delete: {
				open: false
			},
			create: {
				open: false
			},
			edit: {
				open: false
			}
		},
		snapshot: {
			create: {
				open: false
			},
			delete: {
				open: false
			}
		},
		bulk: {
			delete: {
				open: false,
				title: ''
			}
		}
	});

	let query = $state('');
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows.length == 1}
		{#if type === 'flash-file' && activeVolume?.type === GZFSDatasetTypeSchema.enum.VOLUME}
			<Button
				onclick={async () => {
					if (activeVolume) {
						modals.volume.flash.open = true;
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--usb-flash-drive-outline] mr-1 h-4 w-4"></span>

					<span>Flash File</span>
				</div>
			</Button>
		{/if}

		{#if type === 'delete-volume' && activeVolume?.type === GZFSDatasetTypeSchema.enum.VOLUME}
			<Button
				onclick={() => {
					if (activeVolume) {
						modals.volume.delete.open = true;
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
					<span>Delete Volume</span>
				</div>
			</Button>
		{/if}

		{#if type === 'edit-volume' && activeVolume?.type === GZFSDatasetTypeSchema.enum.VOLUME}
			<Button
				onclick={() => {
					if (activeVolume) {
						modals.volume.edit.open = true;
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
					<span>Edit Volume</span>
				</div>
			</Button>
		{/if}
	{:else if activeRows && activeRows.length > 1}
		{#if activeDatasets.length > 0 && !poolsSelected}
			{#if type === 'bulk-delete'}
				<Button
					onclick={async () => {
						let title = `${activeDatasets.length} dataset${activeDatasets.length > 1 ? 's' : ''}`;

						modals.bulk.delete.open = true;
						modals.bulk.delete.title = title;
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<div class="flex items-center">
						<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

						<span>Delete Volumes</span>
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
			onclick={() => {
				modals.volume.create.open = true;
			}}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{@render button('flash-file')}
		{@render button('edit-volume')}
		{@render button('delete-volume')}
		{@render button('delete-volumes')}
		{@render button('bulk-delete')}
	</div>

	<TreeTable
		data={table}
		name={tableName}
		bind:parentActiveRow={activeRows}
		bind:query
		multipleSelect={true}
	/>
</div>

<!-- Flash File to Volume -->
{#if modals.volume.flash.open && activeVolume && activeVolume.type === GZFSDatasetTypeSchema.enum.VOLUME}
	<FlashFile
		bind:open={modals.volume.flash.open}
		dataset={activeVolume}
		downloads={downloads.current}
		bind:reload
	/>
{/if}

<!-- Delete Volume -->
{#if modals.volume.delete.open && activeVolume && activeVolume.type === GZFSDatasetTypeSchema.enum.VOLUME}
	<AlertDialogModal
		bind:open={modals.volume.delete.open}
		names={{
			parent: 'volume',
			element: activeVolume.name
		}}
		actions={{
			onConfirm: async () => {
				if (activeVolume.guid) {
					const response = await deleteVolume(activeVolume);
					reload = true;
					if (response.status === 'success') {
						toast.success(`Deleted volume ${activeVolume.name}`, {
							position: 'bottom-center'
						});
					} else {
						handleAPIError(response);
						toast.error(`Failed to delete volume ${activeVolume.name}`, {
							position: 'bottom-center'
						});
					}
				} else {
					toast.error('Volume GUID not found', {
						position: 'bottom-center'
					});
				}

				modals.volume.delete.open = false;
			},
			onCancel: () => {
				modals.volume.delete.open = false;
			}
		}}
	/>
{/if}

<!-- Bulk Delete -->
{#if modals.bulk.delete.open && activeDatasets.length > 0}
	<AlertDialogModal
		bind:open={modals.bulk.delete.open}
		customTitle={`This will delete ${modals.bulk.delete.title}. This action cannot be undone.`}
		actions={{
			onConfirm: async () => {
				const activeSnapshot = $state.snapshot(activeDatasets);
				const response = await bulkDelete(activeDatasets);
				reload = true;
				if (response.status === 'success') {
					toast.success(`Deleted ${activeSnapshot.length} datasets`, {
						position: 'bottom-center'
					});
				} else {
					handleAPIError(response);
					toast.error('Failed to delete datasets', {
						position: 'bottom-center'
					});
				}

				modals.bulk.delete.open = false;
			},
			onCancel: () => {
				modals.bulk.delete.open = false;
			}
		}}
	/>
{/if}

<!-- Create Volume -->
{#if modals.volume.create.open}
	<CreateVolume bind:open={modals.volume.create.open} {grouped} bind:reload />
{/if}

<!-- Edit Volume -->
{#if modals.volume.edit.open && activeVolume}
	<EditVolume bind:open={modals.volume.edit.open} dataset={activeVolume} bind:reload />
{/if}
