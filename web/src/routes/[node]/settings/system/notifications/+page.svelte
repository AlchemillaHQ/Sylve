<script lang="ts">
	import { deleteNotificationTransport, getNotificationTransports } from '$lib/api/notifications';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import CreateOrEdit from '$lib/components/custom/Notifications/CreateOrEdit.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { NotificationConfig } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		config: NotificationConfig;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let configResource = resource(
		() => 'notification-config',
		async (key) => {
			const loaded = await getNotificationTransports();
			if (!isAPIResponse(loaded)) {
				updateCache(key, loaded);
			}
			return isAPIResponse(loaded) ? data.config : loaded;
		},
		{ initialValue: data.config }
	);

	let modals = $state({
		create: { open: false },
		edit: { open: false, id: 0 },
		delete: { open: false, id: 0 }
	});

	let columns: Column[] = $state([
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'name',
			title: 'Name',
			formatter: (cell: CellComponent) => cell.getValue() || '-'
		},
		{
			field: 'type',
			title: 'Type',
			formatter: (cell: CellComponent) => {
				const v = cell.getValue();
				return v === 'ntfy' ? 'ntfy' : v === 'smtp' ? 'SMTP' : v || '-';
			}
		},
		{
			field: 'enabled',
			title: 'Enabled',
			formatter: (cell: CellComponent) => (cell.getValue() ? 'Yes' : 'No')
		}
	]);

	const tableData: { rows: Row[]; columns: Column[] } = $derived({
		columns,
		rows: ((configResource.current as NotificationConfig).transports || []).map((t) => ({
			id: t.id,
			name: t.name,
			type: t.type,
			enabled: t.enabled
		}))
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let query: string = $state('');
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows.length === 1}
		{#if type === 'edit'}
			<Button
				onclick={() => {
					modals.edit.open = true;
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
		{:else if type === 'delete'}
			<Button
				onclick={() => {
					modals.delete.open = true;
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
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={() => (modals.create.open = true)}>
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
		name="tt-notification-transports"
		bind:parentActiveRow={activeRows}
		bind:query
	/>
</div>

{#if modals.create.open}
	<CreateOrEdit
		bind:open={modals.create.open}
		edit={false}
		transports={(configResource.current as NotificationConfig).transports || []}
		afterChange={() => configResource.refetch()}
	/>
{/if}

{#if modals.edit.open}
	<CreateOrEdit
		bind:open={modals.edit.open}
		edit={true}
		id={modals.edit.id}
		transports={(configResource.current as NotificationConfig).transports || []}
		afterChange={() => configResource.refetch()}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{ parent: 'transport', element: String(activeRow?.name ?? '') }}
	actions={{
		onConfirm: async () => {
			const result = await deleteNotificationTransport(modals.delete.id);
			configResource.refetch();
			if (isAPIResponse(result) && result.status === 'success') {
				toast.success(`Transport deleted`, { position: 'bottom-center' });
			} else {
				handleAPIError(result);
				toast.error(`Failed to delete transport`, { position: 'bottom-center' });
			}
			activeRows = null;
			modals.delete.open = false;
		},
		onCancel: () => {
			activeRows = null;
			modals.delete.open = false;
		}
	}}
/>
