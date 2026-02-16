<script lang="ts">
	import {
		createBackupJob,
		deleteBackupJob,
		getBackupEvents,
		listBackupJobs,
		listBackupTargets,
		runBackupJob,
		updateBackupJob,
		type BackupJobInput
	} from '$lib/api/cluster/backups';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { BackupEvent, BackupJob, BackupTarget } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
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
				jobs.refetch();
				targets.refetch();
				reload = false;
			}
		}
	);

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let selectedJobId = $derived(
		activeRows !== null && activeRows.length === 1 && typeof activeRows[0].id === 'number'
			? activeRows[0].id
			: 0
	);

	let jobModal = $state({
		open: false,
		edit: false,
		name: '',
		targetId: 0,
		mode: 'dataset' as 'dataset' | 'jails',
		sourceDataset: '',
		jailRootDataset: 'zroot/sylve/jails',
		destinationDataset: '',
		cronExpr: '0 * * * *',
		force: false,
		withIntermediates: true,
		enabled: true
	});
	let deleteModalOpen = $state(false);

	let events = $state<BackupEvent[]>([]);
	let loadingEvents = $state(false);

	const jobColumns: Column[] = [
		{ field: 'name', title: 'Name' },
		{ field: 'mode', title: 'Mode' },
		{ field: 'sourceDataset', title: 'Source' },
		{ field: 'destinationDataset', title: 'Destination' },
		{ field: 'cronExpr', title: 'Schedule' },
		{
			field: 'enabled',
			title: 'Enabled',
			formatter: (cell: CellComponent) => (cell.getValue() ? 'Yes' : 'No')
		},
		{
			field: 'lastRunAt',
			title: 'Last Run',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		},
		{ field: 'lastStatus', title: 'Last Status' }
	];

	const eventColumns: Column[] = [
		{ field: 'id', title: 'ID' },
		{ field: 'status', title: 'Status' },
		{ field: 'direction', title: 'Direction' },
		{ field: 'sourceDataset', title: 'Source' },
		{ field: 'destinationDataset', title: 'Destination' },
		{
			field: 'startedAt',
			title: 'Started',
			formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
		},
		{ field: 'error', title: 'Error' }
	];

	let jobsTableData = $derived.by(() => {
		const rows: Row[] = targets.current.map((target) => ({
			id: `target-${target.id}`,
			kind: 'target',
			name: target.name,
			mode: '',
			sourceDataset: '',
			destinationDataset: '',
			cronExpr: '',
			enabled: target.enabled,
			lastRunAt: '',
			lastStatus: '',
			children: jobs.current
				.filter((job) => job.targetId === target.id)
				.map((job) => ({
					id: job.id,
					kind: 'job',
					name: job.name,
					mode: job.mode,
					sourceDataset:
						job.mode === 'jails'
							? job.jailRootDataset || 'zroot/sylve/jails'
							: job.sourceDataset || '',
					destinationDataset: job.destinationDataset,
					cronExpr: job.cronExpr,
					enabled: job.enabled,
					lastRunAt: job.lastRunAt,
					lastStatus: job.lastStatus || ''
				}))
		}));

		return {
			rows,
			columns: jobColumns
		};
	});

	let eventsTableData = $derived({
		rows: events.map((event) => ({ id: event.id, ...event })),
		columns: eventColumns
	});

	function resetJobModal() {
		jobModal.open = false;
		jobModal.edit = false;
		jobModal.name = '';
		jobModal.targetId = targets.current[0]?.id ?? 0;
		jobModal.mode = 'dataset';
		jobModal.sourceDataset = '';
		jobModal.jailRootDataset = 'zroot/sylve/jails';
		jobModal.destinationDataset = '';
		jobModal.cronExpr = '0 * * * *';
		jobModal.force = false;
		jobModal.withIntermediates = true;
		jobModal.enabled = true;
	}

	function openCreateJob() {
		resetJobModal();
		jobModal.open = true;
	}

	function openEditJob() {
		if (!selectedJobId) return;
		const job = jobs.current.find((j) => j.id === selectedJobId);
		if (!job) return;

		jobModal.open = true;
		jobModal.edit = true;
		jobModal.name = job.name;
		jobModal.targetId = job.targetId;
		jobModal.mode = (job.mode as 'dataset' | 'jails') || 'dataset';
		jobModal.sourceDataset = job.sourceDataset || '';
		jobModal.jailRootDataset = job.jailRootDataset || 'zroot/sylve/jails';
		jobModal.destinationDataset = job.destinationDataset;
		jobModal.cronExpr = job.cronExpr;
		jobModal.force = job.force;
		jobModal.withIntermediates = job.withIntermediates;
		jobModal.enabled = job.enabled;
	}

	async function saveJob() {
		const payload: BackupJobInput = {
			name: jobModal.name,
			targetId: Number(jobModal.targetId),
			mode: jobModal.mode,
			sourceDataset: jobModal.mode === 'dataset' ? jobModal.sourceDataset : '',
			jailRootDataset: jobModal.mode === 'jails' ? jobModal.jailRootDataset : '',
			destinationDataset: jobModal.destinationDataset,
			cronExpr: jobModal.cronExpr,
			force: jobModal.force,
			withIntermediates: jobModal.withIntermediates,
			enabled: jobModal.enabled
		};

		const response = jobModal.edit
			? await updateBackupJob(selectedJobId, payload)
			: await createBackupJob(payload);

		if (response.status === 'success') {
			toast.success(jobModal.edit ? 'Backup job updated' : 'Backup job created', {
				position: 'bottom-center'
			});
			reload = true;
			resetJobModal();
			return;
		}

		handleAPIError(response);
		toast.error(jobModal.edit ? 'Failed to update job' : 'Failed to create job', {
			position: 'bottom-center'
		});
	}

	async function removeJob() {
		if (!selectedJobId) return;
		const response = await deleteBackupJob(selectedJobId);
		if (response.status === 'success') {
			toast.success('Backup job deleted', { position: 'bottom-center' });
			reload = true;
			deleteModalOpen = false;
			return;
		}

		handleAPIError(response);
		toast.error('Failed to delete job', { position: 'bottom-center' });
	}

	async function triggerJob() {
		if (!selectedJobId) return;
		const response = await runBackupJob(selectedJobId);
		if (response.status === 'success') {
			toast.success('Backup job started', { position: 'bottom-center' });
			await refreshEvents();
			return;
		}

		handleAPIError(response);
		toast.error('Failed to run job', { position: 'bottom-center' });
	}

	async function refreshEvents() {
		loadingEvents = true;
		try {
			events = await getBackupEvents(200, selectedJobId || undefined);
		} finally {
			loadingEvents = false;
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={openCreateJob} size="sm" class="h-6" disabled={targets.current.length === 0}>
			<div class="flex items-center">
				<Icon icon="gg:add" class="mr-1 h-4 w-4" />
				<span>New Job</span>
			</div>
		</Button>

		<Button onclick={openEditJob} size="sm" variant="outline" class="h-6" disabled={!selectedJobId}>
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
			disabled={!selectedJobId}
		>
			<div class="flex items-center">
				<Icon icon="mdi:delete" class="mr-1 h-4 w-4" />
				<span>Delete</span>
			</div>
		</Button>

		<Button onclick={triggerJob} size="sm" variant="outline" class="h-6" disabled={!selectedJobId}>
			<div class="flex items-center">
				<Icon icon="mdi:play" class="mr-1 h-4 w-4" />
				<span>Run Now</span>
			</div>
		</Button>

		<Button onclick={refreshEvents} size="sm" variant="outline" class="ml-auto h-6">
			{loadingEvents ? 'Loading Events...' : 'Refresh Events'}
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<div class="h-[50%] min-h-[240px] border-b">
			<TreeTable
				data={jobsTableData}
				name="backup-jobs-tree-tt"
				bind:query
				bind:parentActiveRow={activeRows}
				multipleSelect={false}
			/>
		</div>

		<div class="h-[50%] overflow-hidden p-2">
			<div class="flex h-full flex-col overflow-hidden rounded-md border">
				<div class="border-b px-2 py-1 text-sm font-medium">Job Runs / Replication Events</div>
				<TreeTable
					data={eventsTableData}
					name="backup-job-events-tt"
					multipleSelect={false}
					customPlaceholder="No backup events loaded"
				/>
			</div>
		</div>
	</div>
</div>

<Dialog.Root bind:open={jobModal.open}>
	<Dialog.Content class="w-[92%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>{jobModal.edit ? 'Edit Backup Job' : 'New Backup Job'}</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-2 gap-3 py-2">
			<div class="col-span-2 grid gap-1">
				<label class="text-sm font-medium">Name</label>
				<input class="border-input h-9 rounded-md border px-2" bind:value={jobModal.name} />
			</div>

			<div class="grid gap-1">
				<label class="text-sm font-medium">Target</label>
				<select class="border-input h-9 rounded-md border px-2" bind:value={jobModal.targetId}>
					{#each targets.current as target}
						<option value={target.id}>{target.name} ({target.endpoint})</option>
					{/each}
				</select>
			</div>

			<div class="grid gap-1">
				<label class="text-sm font-medium">Mode</label>
				<select class="border-input h-9 rounded-md border px-2" bind:value={jobModal.mode}>
					<option value="dataset">Dataset</option>
					<option value="jails">Jails</option>
				</select>
			</div>

			{#if jobModal.mode === 'dataset'}
				<div class="col-span-2 grid gap-1">
					<label class="text-sm font-medium">Source Dataset</label>
					<input
						class="border-input h-9 rounded-md border px-2"
						bind:value={jobModal.sourceDataset}
					/>
				</div>
			{:else}
				<div class="col-span-2 grid gap-1">
					<label class="text-sm font-medium">Jail Root Dataset</label>
					<input
						class="border-input h-9 rounded-md border px-2"
						bind:value={jobModal.jailRootDataset}
					/>
				</div>
			{/if}

			<div class="col-span-2 grid gap-1">
				<label class="text-sm font-medium">Destination Dataset (on target)</label>
				<input
					class="border-input h-9 rounded-md border px-2"
					bind:value={jobModal.destinationDataset}
				/>
			</div>

			<div class="grid gap-1">
				<label class="text-sm font-medium">Cron (5-field)</label>
				<input class="border-input h-9 rounded-md border px-2" bind:value={jobModal.cronExpr} />
			</div>
		</div>

		<div class="flex items-end gap-4 pb-1 text-sm">
			<label class="flex items-center gap-2">
				<input type="checkbox" bind:checked={jobModal.force} />
				Force recv
			</label>
			<label class="flex items-center gap-2">
				<input type="checkbox" bind:checked={jobModal.withIntermediates} />
				With intermediates
			</label>
			<label class="flex items-center gap-2">
				<input type="checkbox" bind:checked={jobModal.enabled} />
				Enabled
			</label>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={resetJobModal}>Cancel</Button>
			<Button onclick={saveJob}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={deleteModalOpen}
	customTitle="Delete selected backup job?"
	actions={{
		onConfirm: async () => {
			await removeJob();
		},
		onCancel: () => {
			deleteModalOpen = false;
		}
	}}
/>
