<script lang="ts">
	import { getKerberosKeytabs, deleteKerberosKeytab } from '$lib/api/directory-services';
	import Editor from '$lib/components/custom/DirectoryServices/Keytab.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { KerberosKeytab } from '$lib/types/directory-services';
	import { handleAPIError } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let { data }: { data: { node: string; keytabs: KerberosKeytab[]; loadFailed: boolean } } =
		$props();
	const initial = untrack(() => data);
	let keytabs = $state(initial.keytabs);
	let loadFailed = $state(initial.loadFailed);
	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	let selected = $derived(
		activeRows?.length === 1
			? keytabs.find((keytab) => keytab.id === Number(activeRows?.[0].id))
			: undefined
	);
	let editorOpen = $state(false);
	let editing = $state<KerberosKeytab | null>(null);
	let deleteOpen = $state(false);
	let reload = $state(false);
	let refreshing = $state(false);
	let removing = $state(false);
	let generation = 0;
	onDestroy(() => {
		generation++;
	});
	let disabled = $derived(loadFailed || refreshing || removing);
	watch.pre(
		() => data,
		(next) => {
			generation++;
			refreshing = false;
			removing = false;
			reload = false;
			keytabs = next.keytabs;
			loadFailed = next.loadFailed;
			activeRows = null;
			editorOpen = false;
			deleteOpen = false;
			query = '';
		}
	);
	async function refresh() {
		if (refreshing) return;
		refreshing = true;
		const node = data.node;
		const requestGeneration = generation;
		try {
			const result = await getKerberosKeytabs({ hostname: node });
			if (generation !== requestGeneration) return;
			keytabs = result;
			loadFailed = false;
		} catch {
			if (generation === requestGeneration) loadFailed = true;
		} finally {
			if (generation === requestGeneration) refreshing = false;
		}
	}
	async function remove() {
		if (!selected || selected.managed || disabled) return;
		const node = data.node;
		const requestGeneration = generation;
		removing = true;
		try {
			const result = await deleteKerberosKeytab(selected.id, { hostname: node });
			if (generation !== requestGeneration) return;
			if (result.status === 'error') {
				handleAPIError(result);
				return;
			}
			toast.success('Kerberos keytab deleted', { position: 'bottom-center' });
			deleteOpen = false;
			activeRows = null;
			await refresh();
		} finally {
			if (generation === requestGeneration) removing = false;
		}
	}
	watch(
		() => reload,
		(value) => {
			if (value) {
				reload = false;
				void refresh();
			}
		}
	);
	let table = $derived({
		columns: [
			{ field: 'id', title: 'ID', visible: false },
			{
				field: 'name',
				title: 'Name',
				formatter: 'plaintext'
			},
			{
				field: 'principals',
				title: 'Principals',
				formatter: 'textarea'
			},
			{ field: 'source', title: 'Source' },
			{ field: 'updated', title: 'Updated At' }
		],
		rows: keytabs.map((keytab) => ({
			id: keytab.id,
			name: keytab.name,
			principals: keytab.principals.join('\n'),
			source: keytab.managed ? 'AD Machine Account' : 'Uploaded',
			updated: convertDbTime(keytab.updatedAt)
		}))
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query /><Button
			size="sm"
			class="h-6"
			{disabled}
			onclick={() => {
				editing = null;
				editorOpen = true;
			}}><span class="icon-[gg--add] mr-1 h-4 w-4"></span>New</Button
		>{#if selected}<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={selected.managed || disabled}
				onclick={() => {
					editing = selected ?? null;
					editorOpen = true;
				}}
				><SpanWithIcon
					icon="icon-[mdi--pencil]"
					size="h-4 w-4"
					gap="gap-2"
					title="Edit Keytab"
				/></Button
			><Button
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={selected.managed || disabled}
				onclick={() => (deleteOpen = true)}
				><SpanWithIcon
					icon="icon-[mdi--delete]"
					size="h-4 w-4"
					gap="gap-2"
					title="Delete Keytab"
				/></Button
			>{/if}{#if loadFailed}<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={refreshing}
				onclick={refresh}>Retry</Button
			>{/if}
	</div>
	{#if loadFailed}<p class="text-destructive px-3 py-2 text-xs" role="alert">
			Unable to load current keytabs. Changes are disabled until refresh succeeds.
		</p>{/if}
	{#key data.node}<TreeTable
			data={table}
			name="kerberos-keytabs-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={true}
			borderTop={loadFailed}
			bind:query
		/>{/key}
</div>
{#if editorOpen}<Editor bind:open={editorOpen} node={data.node} keytab={editing} bind:reload />{/if}
<AlertDialog
	bind:open={deleteOpen}
	names={{ parent: 'Kerberos keytab', element: selected?.name ?? '' }}
	actions={{ onConfirm: remove, onCancel: () => (deleteOpen = false) }}
	keepOpenOnConfirm={true}
/>
