<script lang="ts">
	import { deleteNetworkObject, getNetworkObjects } from '$lib/api/network/object';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import CreateOrEdit from '$lib/components/custom/Network/Objects/CreateOrEdit.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { NetworkObject } from '$lib/types/network/object';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { createQuery } from '@tanstack/svelte-query';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		objects: NetworkObject[];
	}

	let { data }: { data: Data } = $props();

	const results = createQuery(() => ({
		queryKey: ['networkObjects'],
		queryFn: async () => {
			return await getNetworkObjects();
		},
		keepPreviousData: true,
		initialData: data.objects,
		onSuccess: (data: NetworkObject[]) => {
			updateCache('networkObjects', data);
		}
	}));

	let objects = $derived(results.data || []);
	let modals = $state({
		create: {
			open: false
		},
		edit: {
			open: false,
			id: 0
		},
		delete: {
			open: false,
			id: 0
		}
	});

	let columns: Column[] = $state([
		{
			field: 'id',
			title: 'ID',
			visible: false
		},
		{
			field: 'name',
			title: 'Name',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value || '-';
			}
		},
		{
			field: 'type',
			title: 'Type',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				switch (value) {
					case 'Host':
						return 'Host(s)';
					case 'Network':
						return 'Network(s)';
					case 'Mac':
						return 'MAC(s)';
					case 'DUID':
						return 'DUID(s)';
					default:
						return value || '-';
				}
			}
		},
		{
			field: 'entries',
			title: 'Data',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (Array.isArray(value)) {
					if (typeof value === 'object') {
						if (value && value.length > 0) {
							return value.map((entry) => entry.value).join(', ');
						}
					}
				}

				return value;
			}
		},
		{
			field: 'updatedAt',
			title: 'Updated At',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? new Date(value).toLocaleString() : '-';
			}
		}
	]);

	const tableData: { rows: Row[]; columns: Column[] } = $derived({
		columns,
		rows: objects.map((object) => {
			return {
				id: object.id,
				name: object.name,
				type: object.type,
				entries: object.entries,
				updatedAt: object.updatedAt
			};
		})
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let query: string = $state('');
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows.length == 1}
		{#if type === 'delete'}
			<Button
				onclick={() => {
					modals.delete.open = !modals.delete.open;
					modals.delete.id = Number(activeRow?.id);
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
		{:else if type === 'edit'}
			<Button
				onclick={() => {
					modals.edit.open = !modals.edit.open;
					modals.edit.id = Number(activeRow?.id);
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
					<span>Edit</span>
				</div>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={() => (modals.create.open = !modals.create.open)}>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}
	</div>

	<TreeTable
		data={tableData}
		name="tt-network-objects"
		multipleSelect={false}
		bind:parentActiveRow={activeRows}
		bind:query
	/>
</div>

{#if modals.create.open}
	<CreateOrEdit
		bind:open={modals.create.open}
		networkObjects={objects}
		edit={false}
		afterChange={() => {
			results.refetch();
		}}
	/>
{/if}

{#if modals.edit.open}
	<CreateOrEdit
		bind:open={modals.edit.open}
		networkObjects={objects}
		edit={true}
		id={Number(modals.edit.id)}
		afterChange={() => {
			results.refetch();
		}}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{ parent: 'network object', element: activeRow?.name || 'unknown' }}
	actions={{
		onConfirm: async () => {
			let active = $state.snapshot(activeRow);
			const result = await deleteNetworkObject(modals.delete.id);
			queryClient.refetchQueries('networkObjects');
			if (isAPIResponse(result) && result.status === 'success') {
				toast.success(`Object ${active?.name} deleted`, {
					position: 'bottom-center'
				});
			} else {
				handleAPIError(result);
				if (result.error?.includes('used') || result.error?.includes('in use')) {
					toast.error(`Object ${active?.name} is in use`, {
						position: 'bottom-center'
					});
				} else {
					toast.error(`Error deleting object ${active?.name}`, {
						position: 'bottom-center'
					});
				}
			}

			activeRows = null;
			modals.delete.open = false;
		},
		onCancel: () => {
			activeRows = null;
			modals.delete.open = false;
		}
	}}
></AlertDialog>
