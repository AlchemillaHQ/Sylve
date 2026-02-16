<script lang="ts">
	import {
		createBackupTarget,
		deleteBackupTarget,
		getBackupTargetDatasets,
		getBackupTargetStatus,
		listBackupJobs,
		listBackupTargets,
		pullFromBackupTarget,
		updateBackupTarget,
		type BackupPullInput,
		type BackupTargetInput
	} from '$lib/api/cluster/backups';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { BackupDataset, BackupEvent, BackupJob, BackupTarget } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import humanFormat from 'human-format';
	import Icon from '@iconify/svelte';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		targets: BackupTarget[];
		jobs: BackupJob[];
	}

	let { data }: { data: Data } = $props();

	let targets = resource(
		() => 'backup-targets',
		async () => {
			const res = await listBackupTargets();
			updateCache('backup-targets', res);
			return res;
		},
		{ initialValue: data.targets }
	);

	let jobs = resource(
		() => 'backup-jobs',
		async () => {
			const res = await listBackupJobs();
			updateCache('backup-jobs', res);
			return res;
		},
		{ initialValue: data.jobs }
	);

	let reload = $state(false);
	watch(
		() => reload,
		(value) => {
			if (value) {
				targets.refetch();
				jobs.refetch();
				reload = false;
			}
		}
	);

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let selectedTargetId = $derived(
		activeRows !== null &&
		activeRows.length === 1 &&
		typeof activeRows[0].id === 'number'
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

	let datasets = $state<BackupDataset[]>([]);
	let events = $state<BackupEvent[]>([]);
	let datasetPrefix = $state('');
	let loadingDatasets = $state(false);
	let loadingStatus = $state(false);
	let pullLoading = $state(false);

	let pullModal = $state({
		open: false,
		sourceDataset: '',
		destinationDataset: '',
		snapshot: '',
		force: false,
		withIntermediates: true
	});

	const targetColumns: Column[] = [
		{ field: 'id', title: 'ID' },
		{ field: 'name', title: 'Name' },
		{ field: 'endpoint', title: 'Endpoint' },
		{ field: 'description', title: 'Description' },
		{
			field: 'enabled',
			title: 'Enabled',
			formatter: (cell: CellComponent) => (cell.getValue() ? 'Yes' : 'No')
		}
	];

	const datasetColumns: Column[] = [
		{ field: 'name', title: 'Dataset' },
		{ field: 'type', title: 'Type' },
		{
			field: 'usedBytes',
			title: 'Used',
			formatter: (cell: CellComponent) => humanFormat(Number(cell.getValue() || 0))
		},
		{
			field: 'availableBytes',
			title: 'Available',
			formatter: (cell: CellComponent) => humanFormat(Number(cell.getValue() || 0))
		}
	];

	const eventColumns: Column[] = [
		{ field: 'id', title: 'ID' },
		{ field: 'direction', title: 'Direction' },
		{ field: 'status', title: 'Status' },
		{ field: 'sourceDataset', title: 'Source Dataset' },
		{ field: 'destinationDataset', title: 'Destination Dataset' },
		{
			field: 'startedAt',
			title: 'Started',
			formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
		},
		{
			field: 'completedAt',
			title: 'Completed',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		}
	];

	let targetTableData = $derived({
		rows: targets.current.map((target) => {
			const children = jobs.current
				.filter((job) => job.targetId === target.id)
				.map((job) => ({
					id: `job-${job.id}`,
					name: `Job: ${job.name}`,
					endpoint: '',
					description: `${job.mode} | ${job.destinationDataset}`,
					enabled: job.enabled
				}));

			return {
				id: target.id,
				name: target.name,
				endpoint: target.endpoint,
				description: target.description || '',
				enabled: target.enabled,
				children
			};
		}),
		columns: targetColumns
	});

	let datasetTableData = $derived({
		rows: datasets.map((item, i) => ({
			id: `${item.guid}-${i}`,
			...item
		})),
		columns: datasetColumns
	});

	let statusTableData = $derived({
		rows: events.map((event) => ({
			id: event.id,
			...event
		})),
		columns: eventColumns
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
			return;
		}

		handleAPIError(response);
		toast.error('Failed to delete target', { position: 'bottom-center' });
	}

	async function refreshDatasets() {
		if (!selectedTargetId) return;
		loadingDatasets = true;
		try {
			datasets = await getBackupTargetDatasets(selectedTargetId, datasetPrefix);
		} finally {
			loadingDatasets = false;
		}
	}

	async function refreshStatus() {
		if (!selectedTargetId) return;
		loadingStatus = true;
		try {
			events = await getBackupTargetStatus(selectedTargetId, 100);
		} finally {
			loadingStatus = false;
		}
	}

	function resetPullModal() {
		pullModal.open = false;
		pullModal.sourceDataset = '';
		pullModal.destinationDataset = '';
		pullModal.snapshot = '';
		pullModal.force = false;
		pullModal.withIntermediates = true;
	}

	function openPullModal() {
		if (!selectedTargetId) return;
		pullModal.open = true;
	}

	async function pullDataset() {
		if (!selectedTargetId) return;

		const payload: BackupPullInput = {
			targetId: selectedTargetId,
			sourceDataset: pullModal.sourceDataset,
			destinationDataset: pullModal.destinationDataset,
			snapshot: pullModal.snapshot,
			force: pullModal.force,
			withIntermediates: pullModal.withIntermediates
		};

		pullLoading = true;
		try {
			const result = await pullFromBackupTarget(payload);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				toast.error('Failed to pull dataset from backup target', { position: 'bottom-center' });
				return;
			}

			toast.success(`Backup pull complete (${result.mode})`, { position: 'bottom-center' });
			resetPullModal();
			await refreshStatus();
		} finally {
			pullLoading = false;
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={openCreateTarget} size="sm" class="h-6">
			<div class="flex items-center">
				<Icon icon="gg:add" class="mr-1 h-4 w-4" />
				<span>New Target</span>
			</div>
		</Button>

		<Button onclick={openEditTarget} size="sm" variant="outline" class="h-6" disabled={selectedTargetId === 0}>
			<div class="flex items-center">
				<Icon icon="mdi:note-edit" class="mr-1 h-4 w-4" />
				<span>Edit</span>
			</div>
		</Button>

		<Button
			onclick={() => (deleteModalOpen = true)}
			size="sm"
			variant="outline"
			class="h-6"
			disabled={selectedTargetId === 0}
		>
			<div class="flex items-center">
				<Icon icon="mdi:delete" class="mr-1 h-4 w-4" />
				<span>Delete</span>
			</div>
		</Button>

		<div class="ml-auto flex items-center gap-2">
			<input
				class="border-input bg-background ring-offset-background placeholder:text-muted-foreground focus-visible:ring-ring h-7 w-48 rounded-md border px-2 text-sm"
				placeholder="Dataset prefix"
				bind:value={datasetPrefix}
			/>
			<Button onclick={refreshDatasets} size="sm" variant="outline" class="h-6" disabled={selectedTargetId === 0}>
				{loadingDatasets ? 'Loading Datasets...' : 'Datasets'}
			</Button>
			<Button onclick={refreshStatus} size="sm" variant="outline" class="h-6" disabled={selectedTargetId === 0}>
				{loadingStatus ? 'Loading Status...' : 'Status'}
			</Button>
			<Button onclick={openPullModal} size="sm" variant="outline" class="h-6" disabled={selectedTargetId === 0}>
				Pull State
			</Button>
		</div>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<div class="h-[45%] min-h-[220px] border-b">
			<TreeTable
				data={targetTableData}
				name="backup-targets-tt"
				bind:query
				bind:parentActiveRow={activeRows}
				multipleSelect={false}
			/>
		</div>

		<div class="grid h-[55%] grid-cols-2 gap-2 p-2">
			<div class="flex h-full flex-col overflow-hidden rounded-md border">
				<div class="border-b px-2 py-1 text-sm font-medium">Target Datasets</div>
				<TreeTable
					data={datasetTableData}
					name="backup-target-datasets-tt"
					multipleSelect={false}
					customPlaceholder="No datasets loaded"
				/>
			</div>

			<div class="flex h-full flex-col overflow-hidden rounded-md border">
				<div class="border-b px-2 py-1 text-sm font-medium">Target Replication Status</div>
				<TreeTable
					data={statusTableData}
					name="backup-target-status-tt"
					multipleSelect={false}
					customPlaceholder="No status loaded"
				/>
			</div>
		</div>
	</div>
</div>

<Dialog.Root bind:open={targetModal.open}>
	<Dialog.Content class="w-[90%] max-w-xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>{targetModal.edit ? 'Edit Backup Target' : 'New Backup Target'}</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-3 py-2">
			<div class="grid gap-1">
				<label class="text-sm font-medium">Name</label>
				<input class="border-input h-9 rounded-md border px-2" bind:value={targetModal.name} />
			</div>
			<div class="grid gap-1">
				<label class="text-sm font-medium">Endpoint (host:port)</label>
				<input class="border-input h-9 rounded-md border px-2" bind:value={targetModal.endpoint} />
			</div>
			<div class="grid gap-1">
				<label class="text-sm font-medium">Description</label>
				<input class="border-input h-9 rounded-md border px-2" bind:value={targetModal.description} />
			</div>
			<label class="flex items-center gap-2 text-sm">
				<input type="checkbox" bind:checked={targetModal.enabled} />
				Enabled
			</label>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={resetTargetModal}>Cancel</Button>
			<Button onclick={saveTarget}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={pullModal.open}>
	<Dialog.Content class="w-[92%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>Pull Dataset From Backup Target</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-3 py-2">
			<div class="grid gap-1">
				<label class="text-sm font-medium">Source Dataset (on target)</label>
				<input class="border-input h-9 rounded-md border px-2" bind:value={pullModal.sourceDataset} />
			</div>

			<div class="grid gap-1">
				<label class="text-sm font-medium">Destination Dataset (local)</label>
				<input class="border-input h-9 rounded-md border px-2" bind:value={pullModal.destinationDataset} />
			</div>

			<div class="grid gap-1">
				<label class="text-sm font-medium">Snapshot (optional)</label>
				<input
					class="border-input h-9 rounded-md border px-2"
					placeholder="dataset@snapshot or snapshot"
					bind:value={pullModal.snapshot}
				/>
			</div>

			<div class="flex items-center gap-4 pb-1 text-sm">
				<label class="flex items-center gap-2">
					<input type="checkbox" bind:checked={pullModal.force} />
					Force recv
				</label>
				<label class="flex items-center gap-2">
					<input type="checkbox" bind:checked={pullModal.withIntermediates} />
					With intermediates
				</label>
			</div>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={resetPullModal}>Cancel</Button>
			<Button onclick={pullDataset} disabled={pullLoading}>
				{pullLoading ? 'Pulling...' : 'Pull'}
			</Button>
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
