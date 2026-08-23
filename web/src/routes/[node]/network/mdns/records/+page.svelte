<script lang="ts">
	import {
		getMdnsRecords,
		createMdnsRecord,
		updateMdnsRecord,
		deleteMdnsRecord
	} from '$lib/api/network/mdns';
	import { getInterfaces } from '$lib/api/network/iface';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { MdnsRecordWithManaged } from '$lib/types/network/mdns';
	import type { Iface } from '$lib/types/network/iface';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';
	import { resource } from 'runed';

	const records = resource(
		() => 'mdns-records',
		async () => await getMdnsRecords()
	);
	const networkInterfaces = resource(
		() => 'network-interfaces',
		async () => {
			const result = await getInterfaces();
			return Array.isArray(result) ? result : ([] as Iface[]);
		}
	);

	let modalState = $state({
		name: '',
		type: '',
		port: 0,
		txt: '',
		interfaces: [] as string[],
		interfacesOpen: false,
		isOpen: false,
		isEditMode: false,
		isDeleteOpen: false
	});

	let editingId: number | null = $state(null);
	let isSaving = $state(false);

	function openCreate() {
		modalState.name = '';
		modalState.type = '_http._tcp';
		modalState.port = 80;
		modalState.txt = '';
		modalState.interfaces = [];
		modalState.isOpen = true;
		modalState.isEditMode = false;
		editingId = null;
	}

	function openEdit(rec: MdnsRecordWithManaged) {
		if (rec.managed) return;
		modalState.name = rec.name;
		modalState.type = rec.type;
		modalState.port = rec.port;
		modalState.txt = rec.txt
			? Object.entries(rec.txt)
					.map(([k, v]) => `${k}=${v}`)
					.join(',')
			: '';
		modalState.interfaces = rec.interfaces
			? rec.interfaces
					.split(',')
					.map((value) => value.trim())
					.filter(Boolean)
			: [];
		modalState.isOpen = true;
		modalState.isEditMode = true;
		editingId = rec.id ?? null;
	}

	function resetModal() {
		modalState.name = '';
		modalState.type = '';
		modalState.port = 0;
		modalState.txt = '';
		modalState.interfaces = [];
		modalState.interfacesOpen = false;
		modalState.isOpen = false;
		modalState.isEditMode = false;
		modalState.isDeleteOpen = false;
		editingId = null;
	}

	function confirmDelete(rec: MdnsRecordWithManaged) {
		if (rec.managed) return;
		modalState.name = rec.name;
		modalState.type = rec.type;
		modalState.isDeleteOpen = true;
		editingId = rec.id ?? null;
	}

	async function save() {
		if (isSaving) return;
		isSaving = true;

		try {
			const txtMap: Record<string, string> = {};
			const txtStr = modalState.txt.trim();
			if (txtStr) {
				for (const pair of txtStr.split(',')) {
					const eq = pair.indexOf('=');
					if (eq > 0) {
						txtMap[pair.substring(0, eq).trim()] = pair.substring(eq + 1).trim();
					}
				}
			}

			const payload = {
				name: modalState.name,
				type: modalState.type,
				port: modalState.port,
				txt: txtMap,
				interfaces: modalState.interfaces.join(',')
			};

			if (modalState.isEditMode && editingId !== null) {
				const response = await updateMdnsRecord(editingId, payload);
				if (response.status !== 'success') {
					handleAPIError(response);
					toast.error('Failed to update record', { position: 'bottom-center' });
					return;
				}
				toast.success('Record updated', { position: 'bottom-center' });
			} else {
				const response = await createMdnsRecord(payload);
				if (response.status !== 'success') {
					handleAPIError(response);
					toast.error('Failed to create record', { position: 'bottom-center' });
					return;
				}
				toast.success('Record created', { position: 'bottom-center' });
			}
			resetModal();
			records.refetch();
		} finally {
			isSaving = false;
		}
	}

	async function deleteCurrent() {
		if (editingId === null) return;
		const response = await deleteMdnsRecord(editingId);
		records.refetch();
		if (isAPIResponse(response) && response.status === 'success') {
			toast.success('Record deleted', { position: 'bottom-center' });
			resetModal();
		} else {
			handleAPIError(response);
			toast.error('Failed to delete record', { position: 'bottom-center' });
		}
	}

	let columns: Column[] = $derived([
		{ field: 'name', title: 'Name' },
		{ field: 'type', title: 'Type' },
		{ field: 'port', title: 'Port' },
		{ field: 'txt_display', title: 'TXT' },
		{ field: 'interfaces_display', title: 'Interfaces' },
		{ field: 'source_display', title: 'Source' },
		{ field: 'active_display', title: 'Active' }
	]);

	let tableData = $derived.by(() => {
		const rows: Row[] = (records.current ?? []).map((r: MdnsRecordWithManaged) => {
			const txtStr = r.txt
				? Object.entries(r.txt)
						.map(([k, v]) => `${k}=${v}`)
						.join(', ')
				: '';
			return {
				id: r.managed ? generateNanoId(`managed-${r.type}`) : generateNanoId(`${r.id}`),
				name: r.name,
				type: r.type,
				port: r.port,
				txt_display: txtStr,
				interfaces_display: r.interfaces || 'Global setting',
				source_display: r.managed ? 'Managed by Samba' : 'User',
				active_display: r.active ? 'Yes' : 'No',
				_managed: r.managed,
				_record: r
			};
		});
		return { rows, columns };
	});

	let activeRow: Row[] | null = $state(null);
	let query: string = $state('');

	let selectedRecord = $derived.by(() => {
		if (!activeRow || activeRow.length !== 1) return null;
		const row = activeRow[0];
		return (row._record as MdnsRecordWithManaged | undefined) ?? null;
	});

	let canEdit = $derived(selectedRecord !== null && !selectedRecord.managed);
</script>

{#snippet button(type: string)}
	{#if activeRow !== null && activeRow.length === 1}
		{#if type === 'edit-record'}
			<Button
				onclick={() => selectedRecord && openEdit(selectedRecord)}
				disabled={!canEdit}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{/if}

		{#if type === 'delete-record'}
			<Button
				onclick={() => selectedRecord && confirmDelete(selectedRecord)}
				disabled={!canEdit}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={openCreate} size="sm" class="h-6.5">
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>Add</span>
			</div>
		</Button>

		{@render button('edit-record')}
		{@render button('delete-record')}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={tableData}
			name="tt-mdns-records"
			dataTree={false}
			bind:parentActiveRow={activeRow}
			bind:query
		/>
	</div>
</div>

<Dialog.Root bind:open={modalState.isOpen}>
	<Dialog.Content showCloseButton={true} onClose={resetModal}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon={modalState.isEditMode ? 'icon-[mdi--pencil]' : 'icon-[gg--add]'}
					size="h-5 w-5"
					gap="gap-2"
					title="{modalState.isEditMode ? 'Edit' : 'Add'} mDNS Record"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-2 gap-4">
			<CustomValueInput
				label="Name"
				placeholder="myservice"
				bind:value={modalState.name}
				classes="flex-1 space-y-1"
				type="text"
			/>
			<ComboBox
				bind:open={modalState.interfacesOpen}
				label="Interfaces"
				bind:value={modalState.interfaces}
				data={(networkInterfaces.current ?? []).map((iface) => ({
					label: iface.description || iface.name,
					value: iface.name
				}))}
				classes="flex-1 space-y-1"
				placeholder="Use global setting"
				width="w-full"
				multiple={true}
			/>
			<CustomValueInput
				label="Type"
				placeholder="_http._tcp"
				bind:value={modalState.type}
				classes="flex-1 space-y-1"
				type="text"
			/>
			<CustomValueInput
				label="Port"
				placeholder="80"
				bind:value={modalState.port}
				classes="flex-1 space-y-1"
				type="number"
			/>
			<CustomValueInput
				label="TXT (key=val,key2=val2)"
				placeholder="path=/"
				bind:value={modalState.txt}
				classes="flex-1 space-y-1"
				type="text"
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button
					onclick={save}
					type="submit"
					size="sm"
					class="w-full lg:w-28"
					disabled={isSaving}
					aria-busy={isSaving}
				>
					{#if isSaving}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{/if}
					{isSaving
						? modalState.isEditMode
							? 'Saving…'
							: 'Creating…'
						: modalState.isEditMode
							? 'Save'
							: 'Create'}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={modalState.isDeleteOpen}
	names={{ parent: 'mDNS record', element: modalState.name || '' }}
	actions={{
		onConfirm: async () => {
			await deleteCurrent();
		},
		onCancel: () => {
			modalState.isDeleteOpen = false;
		}
	}}
/>
