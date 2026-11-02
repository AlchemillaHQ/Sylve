<script lang="ts">
	import { deleteRegistryCredential, getRegistryCredentials } from '$lib/api/container/resources';
	import RegistryForm from '$lib/components/custom/Container/RegistryForm.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import { toastContainerError } from '$lib/utils/container/errors';
	import { updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	// svelte-ignore state_referenced_locally
	const credentials = resource(
		() => data.node,
		async (node) => {
			const result = await getRegistryCredentials(node);
			updateCache('container-registries', result, node);
			return result;
		},
		{ initialValue: data.credentials }
	);

	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	let selected = $derived(
		activeRows?.length === 1
			? credentials.current.find((credential) => credential.id === activeRows?.[0].id)
			: undefined
	);
	let modals = $state({ create: false, edit: false, delete: false });

	let table = $derived({
		columns: [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'registry', title: 'Registry' },
			{ field: 'username', title: 'Username' },
			{
				field: 'updatedAt',
				title: 'Updated',
				formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
			}
		],
		rows: credentials.current.map((credential) => ({
			id: credential.id,
			registry: credential.registry,
			username: credential.username,
			updatedAt: credential.updatedAt
		}))
	});

	async function remove() {
		if (!selected) return;
		const registry = selected.registry;
		const result = await deleteRegistryCredential(selected.id, data.node);
		if (result.status === 'error') {
			toastContainerError(result);
			return;
		}

		toast.success(`Credentials for ${registry} deleted`, { position: 'bottom-center' });
		activeRows = null;
		await credentials.refetch();
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={() => (modals.create = true)}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{#if selected}
			<Button size="sm" variant="outline" class="h-6.5" onclick={() => (modals.edit = true)}>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
			<Button size="sm" variant="outline" class="h-6.5" onclick={() => (modals.delete = true)}>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	</div>

	<TreeTable
		data={table}
		name="tt-container-registries"
		multipleSelect={false}
		bind:parentActiveRow={activeRows}
		bind:query
	/>
</div>

{#if modals.create}
	<RegistryForm
		bind:open={modals.create}
		node={data.node}
		afterChange={() => credentials.refetch()}
	/>
{/if}

{#if modals.edit && selected}
	<RegistryForm
		bind:open={modals.edit}
		node={data.node}
		credential={selected}
		afterChange={() => credentials.refetch()}
	/>
{/if}

<AlertDialog
	bind:open={modals.delete}
	names={{ parent: 'registry credentials for', element: selected?.registry ?? '' }}
	actions={{ onConfirm: remove, onCancel: () => (modals.delete = false) }}
/>
