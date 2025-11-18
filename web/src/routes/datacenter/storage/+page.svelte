<script lang="ts">
	import { deleteS3Storage, getStorages } from '$lib/api/cluster/storage';
	import Create from '$lib/components/custom/Cluster/Storage/Create.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { ClusterStorages } from '$lib/types/cluster/storage';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import { toast } from 'svelte-sonner';

	interface Data {
		storages: ClusterStorages;
	}

	let { data }: { data: Data } = $props();

	const queryClient = useQueryClient();
	let results = useQueries([
		{
			queryKey: 'cluster-storages',
			queryFn: getStorages,
			keepPreviousData: true,
			initialData: data.storages,
			refetchOnMount: 'always',
			onSuccess: (data: ClusterStorages) => {
				updateCache('cluster-storages', data);
			}
		}
	]);

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			queryClient.refetchQueries('cluster-storages');
			activeRows = null;
			reload = false;
		}
	});

	let storages = $derived($results[0].data as ClusterStorages);

	let table = $derived.by(() => {
		const rows = [];
		const columns: Column[] = [
			{
				field: 'id',
				title: 'ID',
				visible: false
			},
			{
				field: 'type',
				title: 'Type'
			},
			{
				field: 'name',
				title: 'Name'
			},
			{
				field: 'details',
				title: 'Details',
				formatter: 'html'
			}
		];

		for (const s3Storage of storages.s3) {
			rows.push({
				id: s3Storage.id,
				type: 'S3',
				name: s3Storage.name,
				details: `Bucket: ${s3Storage.bucket}<br/>Region: ${s3Storage.region}`
			});
		}

		for (const dirStorage of storages.directories) {
			rows.push({
				id: dirStorage.id,
				type: 'Directory',
				name: dirStorage.name,
				details: `Path: ${dirStorage.path}`
			});
		}

		return {
			columns,
			rows
		};
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let query = $state('');
	let modals = $state({
		create: {
			open: false
		},
		delete: {
			open: false,
			type: '' as '' | 's3'
		}
	});
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows?.length !== 0}
		{#if type === 'delete'}
			<Button
				onclick={() => {
					modals.delete.open = true;
					if (activeRow?.type === 'S3') {
						modals.delete.type = 's3';
					}
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

					<span>Delete</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={() => (modals.create.open = true)} size="sm" class="h-6  ">
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('delete')}
	</div>

	<TreeTable
		name="cluster-storages-tt"
		data={table}
		{query}
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
	/>
</div>

{#if modals.create.open}
	<Create bind:open={modals.create.open} bind:reload {storages} />
{/if}

<AlertDialog
	open={modals.delete.open}
	customTitle={`This will delete ${activeRow?.name}`}
	actions={{
		onConfirm: async () => {
			let deleteFunc = modals.delete.type === 's3' ? deleteS3Storage : deleteS3Storage;

			const response = await deleteFunc(Number(activeRow?.id));
			reload = true;
			if (response.error) {
				handleAPIError(response);
				toast.error(`Failed to delete ${activeRow?.name}`, {
					position: 'bottom-center'
				});
				return;
			}

			toast.success(`Deleted ${activeRow?.name}`, {
				position: 'bottom-center'
			});

			modals.delete.open = false;
		},
		onCancel: () => {
			modals.delete.open = false;
		}
	}}
></AlertDialog>
