<script lang="ts">
	import { getKerberosRealms, deleteKerberosRealm } from '$lib/api/directory-services';
	import Editor from '$lib/components/custom/DirectoryServices/Realm.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { KerberosRealm } from '$lib/types/directory-services';
	import { handleAPIError } from '$lib/utils/http';
	import { watch } from 'runed';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';

	let { data }: { data: { node: string; realms: KerberosRealm[]; loadFailed: boolean } } = $props();
	const initial = untrack(() => data);
	let realms = $state(initial.realms);
	let loadFailed = $state(initial.loadFailed);
	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	let selected = $derived(
		activeRows?.length === 1
			? realms.find((realm) => realm.id === Number(activeRows?.[0].id))
			: undefined
	);
	let editorOpen = $state(false);
	let editing = $state<KerberosRealm | null>(null);
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
			realms = next.realms;
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
			const result = await getKerberosRealms({ hostname: node });
			if (generation !== requestGeneration) return;
			realms = result;
			loadFailed = false;
		} catch {
			if (generation === requestGeneration) loadFailed = true;
		} finally {
			if (generation === requestGeneration) refreshing = false;
		}
	}
	async function remove() {
		if (!selected || disabled) return;
		const node = data.node;
		const requestGeneration = generation;
		removing = true;
		try {
			const result = await deleteKerberosRealm(selected.id, { hostname: node });
			if (generation !== requestGeneration) return;
			if (result.status === 'error') {
				handleAPIError(result);
				return;
			}
			toast.success('Kerberos realm deleted', { position: 'bottom-center' });
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
				field: 'realm',
				title: 'Realm',
				formatter: 'plaintext'
			},
			{
				field: 'kdc',
				title: 'KDC Servers',
				formatter: 'plaintext'
			},
			{
				field: 'adminServer',
				title: 'Administration Servers',
				formatter: 'plaintext'
			},
			{
				field: 'kpasswdServer',
				title: 'Password Servers',
				formatter: 'plaintext'
			}
		],
		rows: realms.map((realm) => ({
			id: realm.id,
			realm: realm.realm,
			kdc: realm.kdc.join(', ') || 'DNS Discovery',
			adminServer: realm.adminServer.join(', ') || 'DNS Discovery',
			kpasswdServer: realm.kpasswdServer.join(', ') || 'DNS Discovery'
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
				{disabled}
				onclick={() => {
					editing = selected ?? null;
					editorOpen = true;
				}}
				><SpanWithIcon
					icon="icon-[mdi--pencil]"
					size="h-4 w-4"
					gap="gap-2"
					title="Edit Realm"
				/></Button
			><Button
				size="sm"
				variant="outline"
				class="h-6.5"
				{disabled}
				onclick={() => (deleteOpen = true)}
				><SpanWithIcon
					icon="icon-[mdi--delete]"
					size="h-4 w-4"
					gap="gap-2"
					title="Delete Realm"
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
			Unable to load current Kerberos realms. Changes are disabled until refresh succeeds.
		</p>{/if}
	{#key data.node}<TreeTable
			data={table}
			name="kerberos-realms-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={true}
			borderTop={loadFailed}
			bind:query
		/>{/key}
</div>
{#if editorOpen}<Editor bind:open={editorOpen} node={data.node} realm={editing} bind:reload />{/if}
<AlertDialog
	bind:open={deleteOpen}
	names={{ parent: 'Kerberos realm', element: selected?.realm ?? '' }}
	actions={{ onConfirm: remove, onCancel: () => (deleteOpen = false) }}
	keepOpenOnConfirm={true}
/>
