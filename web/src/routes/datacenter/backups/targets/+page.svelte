<script lang="ts">
	import {
		createBackupTarget,
		deleteBackupTarget,
		listBackupTargets,
		updateBackupTarget,
		type BackupTargetInput
	} from '$lib/api/cluster/backups';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { BackupTarget } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import Icon from '@iconify/svelte';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';

	interface Data {
		targets: BackupTarget[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let targets = resource(
		() => 'backup-targets',
		async () => {
			const res = await listBackupTargets();
			updateCache('backup-targets', res);
			return res;
		},
		{ initialValue: data.targets }
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				targets.refetch();
				reload = false;
			}
		}
	);

	let query = $state('');
	let activeRows: Row[] = $state([]);
	let selectedTargetId = $derived(
		activeRows !== null && activeRows.length === 1 && typeof activeRows[0].id === 'number'
			? Number(activeRows[0].id)
			: 0
	);

	let targetModal = $state({
		open: false,
		edit: false,
		name: '',
		endpoint: '',
		description: '',
		enabled: true
	});
	let deleteModalOpen = $state(false);

	const targetColumns: Column[] = [
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'enabled',
			title: 'Status',
			formatter: (cell: CellComponent) =>
				cell.getValue()
					? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500')
					: renderWithIcon('mdi:close-circle', 'Disabled', 'text-muted-foreground')
		},
		{ field: 'name', title: 'Name' },
		{ field: 'endpoint', title: 'Endpoint' },
		{ field: 'description', title: 'Description' },
		{
			field: 'createdAt',
			title: 'Created',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		}
	];

	let tableData = $derived({
		rows: targets.current.map((target) => ({
			id: target.id,
			name: target.name,
			endpoint: target.endpoint,
			description: target.description || '',
			enabled: target.enabled,
			createdAt: target.createdAt
		})),
		columns: targetColumns
	});

	function resetTargetModal() {
		targetModal.open = false;
		targetModal.edit = false;
		targetModal.name = '';
		targetModal.endpoint = '';
		targetModal.description = '';
		targetModal.enabled = true;
	}

	function openCreateTarget() {
		resetTargetModal();
		targetModal.open = true;
	}

	function openEditTarget() {
		if (selectedTargetId === 0) return;
		const target = targets.current.find((t) => t.id === selectedTargetId);
		if (!target) return;

		targetModal.open = true;
		targetModal.edit = true;
		targetModal.name = target.name;
		targetModal.endpoint = target.endpoint;
		targetModal.description = target.description || '';
		targetModal.enabled = target.enabled;
	}

	async function saveTarget() {
		if (!targetModal.name.trim()) {
			toast.error('Name is required', { position: 'bottom-center' });
			return;
		}
		if (!targetModal.endpoint.trim()) {
			toast.error('Endpoint is required', { position: 'bottom-center' });
			return;
		}

		const payload: BackupTargetInput = {
			name: targetModal.name,
			endpoint: targetModal.endpoint,
			description: targetModal.description,
			enabled: targetModal.enabled
		};

		const response = targetModal.edit
			? await updateBackupTarget(selectedTargetId, payload)
			: await createBackupTarget(payload);

		if (response.status === 'success') {
			toast.success(targetModal.edit ? 'Backup target updated' : 'Backup target created', {
				position: 'bottom-center'
			});
			reload = true;
			resetTargetModal();
			return;
		}

		handleAPIError(response);
		toast.error(targetModal.edit ? 'Failed to update target' : 'Failed to create target', {
			position: 'bottom-center'
		});
	}

	async function removeTarget() {
		if (!selectedTargetId) return;
		const response = await deleteBackupTarget(selectedTargetId);
		if (response.status === 'success') {
			toast.success('Backup target deleted', { position: 'bottom-center' });
			reload = true;
			deleteModalOpen = false;
			activeRows = [];
			return;
		}

		handleAPIError(response);
		toast.error('Failed to delete target', { position: 'bottom-center' });
	}
</script>

{#snippet button(type: string)}
	{#if type === 'edit' && activeRows !== null && activeRows.length === 1}
		<Button onclick={openEditTarget} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<Icon icon="mdi:note-edit" class="mr-1 h-4 w-4" />
				<span>Edit</span>
			</div>
		</Button>
	{/if}

	{#if type === 'delete' && activeRows !== null && activeRows.length === 1}
		<Button onclick={() => (deleteModalOpen = true)} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<Icon icon="mdi:delete" class="mr-1 h-4 w-4" />
				<span>Delete</span>
			</div>
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={openCreateTarget} size="sm" class="h-6">
			<div class="flex items-center">
				<Icon icon="gg:add" class="mr-1 h-4 w-4" />
				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}

		<Button onclick={() => (reload = true)} size="sm" variant="outline" class="ml-auto h-6 hidden">
			<div class="flex items-center">
				<Icon icon="mdi:refresh" class="mr-1 h-4 w-4" />
				<span>Refresh</span>
			</div>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={tableData}
			name="backup-targets-tt"
			bind:query
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
		/>
	</div>
</div>

<Dialog.Root bind:open={targetModal.open}>
	<Dialog.Content class="w-[90%] max-w-xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>
				<div class="flex items-center gap-2">
					<Icon icon={targetModal.edit ? 'mdi:note-edit' : 'mdi:server-network'} class="h-5 w-5" />
					<span>{targetModal.edit ? 'Edit Backup Target' : 'New Backup Target'}</span>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<CustomValueInput
				label="Name"
				placeholder="offsite-backup"
				bind:value={targetModal.name}
				classes="space-y-1"
			/>

			<CustomValueInput
				label="Endpoint"
				placeholder="192.168.1.100:7444"
				bind:value={targetModal.endpoint}
				classes="space-y-1"
			/>

			<CustomValueInput
				label="Description"
				placeholder="Offsite backup server in datacenter"
				bind:value={targetModal.description}
				classes="space-y-1"
			/>

			<CustomCheckbox
				label="Enabled"
				bind:checked={targetModal.enabled}
				classes="flex items-center gap-2"
			/>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={resetTargetModal}>Cancel</Button>
			<Button onclick={saveTarget}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={deleteModalOpen}
	customTitle="Delete selected backup target?"
	actions={{
		onConfirm: async () => {
			await removeTarget();
		},
		onCancel: () => {
			deleteModalOpen = false;
		}
	}}
/>
