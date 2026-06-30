<script lang="ts">
	import { resource } from 'runed';
	import { getMdnsSettings, setMdnsSettings } from '$lib/api/network/mdns';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { generateNanoId } from '$lib/utils/string';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	const settings = resource(
		() => 'mdns-settings',
		async () => await getMdnsSettings()
	);

	let query = $state('');

	let tableData = $derived.by(() => {
		const columns: Column[] = [
			{ field: 'property', title: 'Property' },
			{ field: 'value', title: 'Value' }
		];

		const s = settings.current;
		if (!s) return { columns, rows: [] as Row[] };

		const rows: Row[] = [
			{ id: generateNanoId('interfaces'), property: 'Interfaces', value: s.interfaces || '(all)' },
			{ id: generateNanoId('hostname'), property: 'Hostname', value: s.hostname || '(system hostname)' }
		];

		return { columns, rows };
	});

	let modalOpen = $state(false);
	let form = $state({ interfaces: '', hostname: '' });

	function openEdit() {
		const s = settings.current;
		if (!s) return;
		form = { interfaces: s.interfaces, hostname: s.hostname };
		modalOpen = true;
	}

	async function save() {
		const response = await setMdnsSettings({
			interfaces: form.interfaces,
			hostname: form.hostname
		});
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to save settings', { position: 'bottom-center' });
			return;
		}
		toast.success('mDNS settings updated', { position: 'bottom-center' });
		modalOpen = false;
		await settings.refetch();
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<div class="ml-auto">
			<Button onclick={openEdit} size="sm">Edit</Button>
		</div>
	</div>

	<div class="flex-1 overflow-hidden">
		<TreeTable name="mdns-settings" data={tableData} bind:query />
	</div>
</div>

<Dialog.Root bind:open={modalOpen}>
	<Dialog.Content showCloseButton={true} onClose={() => (modalOpen = false)}>
		<Dialog.Header>
			<Dialog.Title>mDNS Settings</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput label="Interfaces (comma-separated, empty=all)" placeholder="bridge0" bind:value={form.interfaces} classes="space-y-1.5" type="text" />
		<CustomValueInput label="Hostname (empty=system hostname)" placeholder="myserver" bind:value={form.hostname} classes="space-y-1.5 mt-4" type="text" />

		<Dialog.Footer class="flex justify-end mt-4">
			<Button onclick={save} type="submit" size="sm">Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
