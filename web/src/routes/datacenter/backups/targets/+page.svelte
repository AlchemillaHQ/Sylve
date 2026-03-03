<script lang="ts">
	import {
		deleteBackupTarget,
		listBackupTargets,
		validateBackupTarget
	} from '$lib/api/cluster/backups';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { BackupTarget } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';
	import Form from '$lib/components/custom/DataCenter/Backups/Targets/Form.svelte';

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

	let selectedTarget = $derived.by(() => {
		if (!selectedTargetId) return null;
		return targets.current.find((t) => t.id === selectedTargetId) || null;
	});

	let targetModal = $state({
		open: false,
		edit: false,
		name: '',
		sshHost: '',
		sshPort: 22,
		sshKey: '',
		backupRoot: '',
		createBackupRoot: false,
		description: '',
		enabled: true
	});

	let deleteModalOpen = $state(false);
	let validating = $state(false);

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
		{ field: 'sshHost', title: 'SSH Host', visible: false },
		{ field: 'sshPort', title: 'Port', visible: false },
		{ field: 'target', title: 'Target' },
		{ field: 'backupRoot', title: 'Backup Root' },
		{
			field: 'description',
			title: 'Description',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (typeof value === 'string' && value.length > 32) {
					const truncated = value.slice(0, 32) + '...';
					return `<span title="${value}">${truncated}</span>`;
				}

				return value || '-';
			}
		}
	];

	let tableData = $derived({
		rows: targets.current.map((target) => ({
			id: target.id,
			name: target.name,
			sshHost: target.sshHost,
			sshPort: target.sshPort || 22,
			target: `${target.sshHost}:${target.sshPort || 22}`,
			backupRoot: target.backupRoot,
			description: target.description || '-',
			enabled: target.enabled,
			createdAt: target.createdAt
		})),
		columns: targetColumns
	});

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

	async function validateTarget() {
		if (!selectedTargetId) return;
		validating = true;
		try {
			const response = await validateBackupTarget(selectedTargetId);
			if (response.status === 'success') {
				toast.success('Target connectivity validated', { position: 'bottom-center' });
			} else {
				handleAPIError(response);
				toast.error('Validation failed', { position: 'bottom-center' });
			}
		} catch {
			toast.error('Validation failed', { position: 'bottom-center' });
		} finally {
			validating = false;
		}
	}
</script>

{#snippet button(type: string)}
	{#if type === 'validate' && activeRows !== null && activeRows.length === 1}
		<Button onclick={validateTarget} size="sm" variant="outline" class="h-6" disabled={validating}>
			<div class="flex items-center">
				<span
					class="mr-1 h-4 w-4 {validating
						? 'icon-[mdi--loading] animate-spin'
						: 'icon-[mdi--connection]'}"
				></span>
				<span>{validating ? 'Validating' : 'Validate'}</span>
			</div>
		</Button>
	{/if}

	{#if type === 'edit' && activeRows !== null && activeRows.length === 1}
		<Button
			onclick={() => {
				targetModal.edit = true;
				targetModal.open = true;
			}}
			size="sm"
			variant="outline"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--note-edit] mr-1 h-4 w-4"></span>
				<span>Edit</span>
			</div>
		</Button>
	{/if}

	{#if type === 'delete' && activeRows !== null && activeRows.length === 1}
		<Button
			onclick={() => {
				targetModal.name = targets.current.find((t) => t.id === selectedTargetId)?.name || '';
				deleteModalOpen = true;
			}}
			size="sm"
			variant="outline"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
				<span>Delete</span>
			</div>
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button
			onclick={() => {
				targetModal.edit = false;
				targetModal.open = true;
				activeRows = [];
			}}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}
		{@render button('validate')}

		<Button onclick={() => (reload = true)} size="sm" variant="outline" class="ml-auto h-6 hidden">
			<div class="flex items-center">
				<span class="icon-[mdi--refresh] mr-1 h-4 w-4"></span>
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

<Form
	bind:open={targetModal.open}
	bind:edit={targetModal.edit}
	bind:name={targetModal.name}
	bind:sshHost={targetModal.sshHost}
	bind:sshPort={targetModal.sshPort}
	bind:sshKey={targetModal.sshKey}
	bind:backupRoot={targetModal.backupRoot}
	bind:createBackupRoot={targetModal.createBackupRoot}
	bind:description={targetModal.description}
	bind:enabled={targetModal.enabled}
	bind:reload
	{selectedTarget}
/>

<AlertDialog
	open={deleteModalOpen}
	names={{ parent: 'backup target', element: targetModal.name || '' }}
	actions={{
		onConfirm: async () => {
			await removeTarget();
		},
		onCancel: () => {
			deleteModalOpen = false;
		}
	}}
/>
