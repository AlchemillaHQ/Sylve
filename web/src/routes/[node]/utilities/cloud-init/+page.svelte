<script lang="ts">
	import { deleteTemplate, getTemplates } from '$lib/api/utilities/cloud-init';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Form from '$lib/components/custom/Utilities/Cloud-Init/Form.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Row } from '$lib/types/components/tree-table';
	import type { CloudInitTemplate } from '$lib/types/utilities/cloud-init';
	import {
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import { generateTableData } from '$lib/utils/utilities/cloud-init';
	import { resource } from 'runed';
	import { onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Data {
		node: string;
		templates: CloudInitTemplate[];
		loadErrors: APIResponse[];
	}

	let { data }: { data: Data } = $props();
	const initialData = untrack(() => data);
	const lastTemplatesByNode: Record<string, CloudInitTemplate[]> = Object.create(null);
	lastTemplatesByNode[initialData.node] = initialData.templates;

	const templates = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			try {
				const result = await getTemplates({ hostname: node, signal });
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return lastTemplatesByNode[node] ?? [];
				}
				lastTemplatesByNode[node] = result;
				await updateCache('cloud-init-templates', result, node);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) return lastTemplatesByNode[node] ?? [];
				throw error;
			}
		},
		{ initialValue: initialData.templates }
	);

	async function refreshTemplates() {
		await templates.refetch();
	}

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	let tableData = $derived(generateTableData(templates.current));
	let query = $state('');
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
					modal.template = templates.current.find((t) => t.id === activeRows?.[0].id) ?? null;
					if (modal.template) modal.edit = true;
				}}
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{:else}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				onclick={() => {
					if (activeRows === null) return;
					modal.template = templates.current.find((t) => t.id === activeRows?.[0].id) ?? null;
					if (modal.template) modal.delete = true;
				}}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button onclick={() => (modal.new = true)} size="sm" class="h-6">
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
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
	<Form
		bind:open={modal.new}
		template={null}
		node={data.node}
		onSaved={refreshTemplates}
	/>
{/if}

{#if modal.edit}
	<Form
		bind:open={modal.edit}
		template={modal.template}
		node={data.node}
		onSaved={async () => {
			await refreshTemplates();
			activeRows = null;
		}}
	/>
{/if}

{#if modal.delete}
	<AlertDialog
		open={modal.delete}
		names={{ parent: 'template', element: modal.template?.name || '' }}
		actions={{
			onConfirm: async () => {
				if (!modal.template) return;
				const result = await deleteTemplate(modal.template.id, { hostname: data.node });
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return;
				}

				await refreshTemplates();
				toast.success(`Template ${result.name} deleted`, { position: 'bottom-center' });
				modal.delete = false;
				modal.template = null;
				activeRows = null;
			},
			onCancel: () => {
				modal.delete = false;
				modal.template = null;
				activeRows = null;
			}
		}}
	/>
{/if}
