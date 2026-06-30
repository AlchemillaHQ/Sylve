<script lang="ts">
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { MdnsRecordWithManaged } from '$lib/types/network/mdns';
	import { generateNanoId } from '$lib/utils/string';
	import { resource } from 'runed';
	import { getMdnsRecords, createMdnsRecord, updateMdnsRecord, deleteMdnsRecord } from '$lib/api/network/mdns';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	const records = resource(
		() => 'mdns-records',
		async () => await getMdnsRecords()
	);

	let query = $state('');
	let parentActiveRow: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(parentActiveRow ? parentActiveRow[0] ?? null : null);

	let tableData = $derived.by(() => {
		const columns: Column[] = [
			{ field: 'name', title: 'Name' },
			{ field: 'type', title: 'Type' },
			{ field: 'port', title: 'Port' },
			{ field: 'txt_display', title: 'TXT' },
			{ field: 'source_display', title: 'Source' }
		];

		const rows: Row[] = (records.current ?? []).map((r: MdnsRecordWithManaged) => {
			const txtStr = r.txt ? Object.entries(r.txt).map(([k, v]) => `${k}=${v}`).join(', ') : '';
			return {
				id: r.managed ? generateNanoId(`managed-${r.type}`) : generateNanoId(`${r.id}`),
				name: r.name,
				type: r.type,
				port: r.port,
				txt_display: txtStr,
				source_display: r.managed ? 'Managed by Samba' : 'User',
				_managed: r.managed,
				_record: r
			};
		});

		return { columns, rows };
	});

	let modalOpen = $state(false);
	let editingRecord: MdnsRecordWithManaged | null = $state(null);

	let form = $state({
		name: '',
		type: '_http._tcp',
		port: 80,
		txt: ''
	});

	function openCreate() {
		form = { name: '', type: '_http._tcp', port: 80, txt: '' };
		editingRecord = null;
		modalOpen = true;
	}

	function openEdit(row: Row) {
		const rec = row._record as MdnsRecordWithManaged;
		if (rec.managed) return;
		form.name = rec.name;
		form.type = rec.type;
		form.port = rec.port;
		form.txt = rec.txt ? Object.entries(rec.txt).map(([k, v]) => `${k}=${v}`).join(',') : '';
		editingRecord = rec;
		modalOpen = true;
	}

	async function saveRecord() {
		const txtMap: Record<string, string> = {};
		const txtStr = form.txt.trim();
		if (txtStr) {
			for (const pair of txtStr.split(',')) {
				const eq = pair.indexOf('=');
				if (eq > 0) {
					txtMap[pair.substring(0, eq).trim()] = pair.substring(eq + 1).trim();
				}
			}
		}

		const payload = { name: form.name, type: form.type, port: form.port, txt: txtMap, interfaces: '' };

		let response;
		if (editingRecord) {
			response = await updateMdnsRecord(editingRecord.id, payload);
		} else {
			response = await createMdnsRecord(payload);
		}

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to save mDNS record', { position: 'bottom-center' });
			return;
		}

		toast.success(editingRecord ? 'Record updated' : 'Record created', { position: 'bottom-center' });
		modalOpen = false;
		await records.refetch();
	}

	async function deleteCurrent() {
		if (!activeRow) return;
		const rec = activeRow._record as MdnsRecordWithManaged;
		if (rec.managed) return;
		const response = await deleteMdnsRecord(rec.id);
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to delete record', { position: 'bottom-center' });
			return;
		}
		toast.success('Record deleted', { position: 'bottom-center' });
		await records.refetch();
	}

	function canEdit(): boolean {
		if (!activeRow) return false;
		return !(activeRow._managed as boolean);
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<div class="ml-auto flex gap-2">
			<Button onclick={openCreate} size="sm">Add</Button>
			<Button onclick={async () => { if (activeRow) openEdit(activeRow); }} disabled={!canEdit()} size="sm" variant="outline">Edit</Button>
			<Button onclick={deleteCurrent} disabled={!canEdit()} size="sm" variant="destructive">Delete</Button>
		</div>
	</div>

	<div class="flex-1 overflow-hidden">
		<TreeTable
			name="mdns-records"
			data={tableData}
			bind:parentActiveRow
			bind:query
		/>
	</div>
</div>

<Dialog.Root bind:open={modalOpen}>
	<Dialog.Content showCloseButton={true} onClose={() => (modalOpen = false)}>
		<Dialog.Header>
			<Dialog.Title>{editingRecord ? 'Edit Record' : 'Add mDNS Record'}</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-2 gap-4">
			<CustomValueInput label="Name" placeholder="myservice" bind:value={form.name} classes="space-y-1.5" type="text" />
			<CustomValueInput label="Type" placeholder="_http._tcp" bind:value={form.type} classes="space-y-1.5" type="text" />
			<CustomValueInput label="Port" placeholder="80" bind:value={form.port} classes="space-y-1.5" type="number" />
			<CustomValueInput label="TXT (key=val, key2=val2)" placeholder="path=/" bind:value={form.txt} classes="space-y-1.5" type="text" />
		</div>

		<Dialog.Footer class="flex justify-end">
			<Button onclick={saveRecord} type="submit" size="sm">Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
