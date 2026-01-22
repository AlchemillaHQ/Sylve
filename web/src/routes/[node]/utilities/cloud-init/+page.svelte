<script lang="ts">
	import { deleteTemplate, getTemplates } from '$lib/api/utilities/cloud-init';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Form from '$lib/components/custom/Utilities/Cloud-Init/Form.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { CloudInitTemplate } from '$lib/types/utilities/cloud-init';
	import { updateCache } from '$lib/utils/http';
	import { generateTableData } from '$lib/utils/utilities/cloud-init';
	import { resource, watch } from 'runed';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import { toast } from 'svelte-sonner';

	interface Data {
		templates: CloudInitTemplate[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const templates = resource(
		() => 'cloud-init-templates',
		async () => {
			const results = await getTemplates();
			updateCache('cloud-init-templates', results);
			return results;
		},
		{
			initialValue: data.templates
		}
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				templates.refetch();
				reload = false;
			}
		}
	);

	let tableData = $derived(generateTableData(templates.current));
	let query: string = $state('');
	let activeRows: Row[] | null = $state(null);

	let modal = $state({
		new: false,
		edit: false,
		template: null as CloudInitTemplate | null,
		delete: false
	});
</script>

{#snippet button(type: 'edit' | 'delete')}
	{#if activeRows && activeRows.length === 1}
		{#if type === 'edit'}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={() => {
					if (activeRows === null) return;
					const id = activeRows[0].id;
					const template = templates.current.find((t) => t.id === id) || null;
					modal.template = template;
					modal.edit = true;
				}}
			>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
					<span>Edit</span>
				</div>
			</Button>
		{:else if type === 'delete'}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={() => {
					if (activeRows === null) return;
					const id = activeRows[0].id;
					const template = templates.current.find((t) => t.id === id) || null;
					modal.template = template;
					modal.delete = true;
				}}
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
		<Button onclick={() => (modal.new = true)} size="sm" class="h-6  ">
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
		name="tt-cloud-init-templates"
		multipleSelect={true}
		bind:parentActiveRow={activeRows}
		bind:query
	/>
</div>

{#if modal.new}
	<Form bind:open={modal.new} bind:reload template={null} />
{/if}

{#if modal.edit}
	<Form bind:open={modal.edit} bind:reload template={modal.template} />
{/if}

{#if modal.delete}
	<AlertDialog
		open={modal.delete}
		names={{ parent: 'template', element: modal.template?.name || '' }}
		actions={{
			onConfirm: async () => {
				const response = await deleteTemplate(modal.template?.id as number);
				reload = true;
				if (response.status === 'success') {
					toast.success(`Template ${modal.template?.name} deleted`, { position: 'bottom-center' });
					modal.delete = false;
					modal.template = null;
				} else {
					toast.error(`Failed to delete template ${modal.template?.name}`, {
						position: 'bottom-center'
					});
				}

				activeRows = null;
			},
			onCancel: () => {
				modal.delete = false;
				modal.template = null;
				activeRows = null;
			}
		}}
	></AlertDialog>
{/if}
