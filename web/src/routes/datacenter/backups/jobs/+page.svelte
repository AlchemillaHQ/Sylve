<script lang="ts">
	import {
		createBackupJob,
		deleteBackupJob,
		listBackupJobs,
		listBackupTargets,
		listBackupJobSnapshots,
		restoreBackupJob,
		runBackupJob,
		updateBackupJob,
		type BackupJobInput
	} from '$lib/api/cluster/backups';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { BackupJob, BackupTarget, SnapshotInfo } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime, cronToHuman } from '$lib/utils/time';
	import Icon from '@iconify/svelte';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';
	import { getJails } from '$lib/api/jail/jail';
	import { storage } from '$lib';

	interface Data {
		targets: BackupTarget[];
		jobs: BackupJob[];
		nodes: ClusterNode[];
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

	// svelte-ignore state_referenced_locally
	let jobs = resource(
		() => 'backup-jobs',
		async () => {
			const res = await listBackupJobs();
			updateCache('backup-jobs', res);
			return res;
		},
		{ initialValue: data.jobs }
	);

	let jails = $state<any[]>([]);
	let jailsLoading = $state(false);

	async function loadJails() {
		if (jails.length > 0 || jailsLoading) return;
		jailsLoading = true;
		try {
			const res = await getJails();
			updateCache('jail-list', res);
			jails = res;
		} finally {
			jailsLoading = false;
		}
	}

	// svelte-ignore state_referenced_locally
	let nodes = $state(data.nodes);
	let reload = $state(false);

	watch(
		() => jobs.current,
		(currentJobs) => {
			const hasJailJobs = currentJobs.some((job) => job.mode === 'jail');
			if (hasJailJobs && jails.length === 0 && !jailsLoading) {
				loadJails();
			}
		}
	);

	watch(
		() => reload,
		(value) => {
			if (value) {
				jobs.refetch();
				targets.refetch();
				if (jails.length > 0) {
					loadJails();
				}
				reload = false;
			}
		}
	);

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let selectedJobId = $derived.by(() => {
		if (activeRows !== null && activeRows.length === 1) {
			const row = activeRows[0];
			if ('id' in row && typeof row.id === 'number') {
				return row.id;
			}
		}
		return 0;
	});

	let jobModal = $state({
		open: false,
		edit: false,
		name: '',
		targetId: '',
		runnerNodeId: '',
		mode: 'dataset' as 'dataset' | 'jail',
		sourceDataset: '',
		selectedJailId: '',
		destSuffix: '',
		pruneKeepLast: '0',
		pruneTarget: false,
		cronExpr: '0 * * * *',
		enabled: true
	});

	watch(
		[() => jobModal.mode, () => jobModal.runnerNodeId, () => jobModal.open],
		([mode, runnerNodeId, open], [prevMode, prevRunnerNodeId, prevOpen]) => {
			if (open && mode === 'jail' && runnerNodeId !== '') {
				storage.hostname = nodes.find((n) => n.nodeUUID === runnerNodeId)?.hostname || '';
				loadJails();
			}
		}
	);

	let deleteModalOpen = $state(false);

	let restoreModal = $state({
		open: false,
		loading: false,
		restoring: false,
		snapshots: [] as SnapshotInfo[],
		selectedSnapshot: '',
		error: ''
	});

	let nodeNameById = $derived.by(() => {
		const out: Record<string, string> = {};
		for (const node of nodes) {
			out[node.nodeUUID] = node.hostname;
		}
		return out;
	});

	let targetNameById = $derived.by(() => {
		const out: Record<number, string> = {};
		for (const target of targets.current) {
			out[target.id] = target.name;
		}
		return out;
	});

	const jobColumns: Column[] = [
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'mode',
			title: 'Mode',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();

				let v = { name: '', icon: '' };

				switch (value) {
					case 'jail':
						v = { name: 'Jail', icon: 'hugeicons:prison' };
						break;
					case 'dataset':
						v = { name: 'Dataset', icon: 'mdi:database' };
						break;
					default:
						v = { name: String(value), icon: 'mdi:help' };
				}

				return renderWithIcon(v.icon, v.name);
			}
		},
		{
			field: 'enabled',
			title: 'Status',
			formatter: (cell: CellComponent) =>
				cell.getValue()
					? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500')
					: renderWithIcon('mdi:close-circle', 'Disabled', 'text-muted-foreground')
		},
		{ field: 'name', title: 'Name' },
		{
			field: 'targetId',
			title: 'Target',
			formatter: (cell: CellComponent) => {
				const value = Number(cell.getValue());
				return targetNameById[value] || String(value);
			}
		},
		{
			field: 'runnerNodeId',
			title: 'Node',
			formatter: (cell: CellComponent) => {
				const value = String(cell.getValue() || '');
				return nodeNameById[value] || value || '-';
			}
		},
		{ field: 'sourceDataset', title: 'Source' },
		{ field: 'destSuffix', title: 'Dest Suffix' },
		{
			field: 'pruneKeepLast',
			title: 'Prune',
			formatter: (cell: CellComponent) => {
				const value = Number(cell.getValue() || 0);
				return value > 0 ? `Keep ${value}` : 'Off';
			}
		},
		{ field: 'cronExpr', title: 'Schedule' },
		{
			field: 'lastRunAt',
			title: 'Last Run',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		},
		{
			field: 'lastStatus',
			title: 'Last Status',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (value === 'completed') {
					return '<span class="text-green-500">Completed</span>';
				} else if (value === 'failed') {
					return '<span class="text-red-500">Failed</span>';
				} else if (value === 'running') {
					return '<span class="text-yellow-500">Running</span>';
				}
				return value || '-';
			}
		}
	];

	function getSource(mode: string, dataset: string): string {
		// For jail mode, try to resolve the jail name if jails are loaded
		if (mode === 'jail' && jails.length > 0 && dataset) {
			const jail = jails.find((j: any) => {
				const baseStorage = j.storages?.find((s: any) => s.isBase);
				if (baseStorage) {
					const jailDataset = `${baseStorage.pool}/sylve/jails/${j.ctId}`;
					return jailDataset === dataset;
				}
				return false;
			});
			if (jail) {
				return jail.name;
			}
		}
		return dataset || '';
	}

	let tableData = $derived({
		rows: jobs.current.map((job) => ({
			id: job.id,
			name: job.name,
			targetId: job.targetId,
			runnerNodeId: job.runnerNodeId || '',
			mode: job.mode,
			sourceDataset:
				job.friendlySrc ||
				getSource(
					job.mode,
					job.mode === 'jail' ? job.jailRootDataset || '' : job.sourceDataset || ''
				),
			destSuffix: job.destSuffix || '',
			pruneKeepLast: job.pruneKeepLast || 0,
			pruneTarget: job.pruneTarget || false,
			cronExpr: job.cronExpr,
			enabled: job.enabled,
			lastRunAt: job.lastRunAt,
			lastStatus: job.lastStatus || ''
		})),
		columns: jobColumns
	});

	let targetOptions = $derived([
		...targets.current.map((target) => ({
			value: String(target.id),
			label: target.name
		}))
	]);

	let nodeOptions = $derived([
		{ value: '', label: 'Select a node' },
		...nodes.map((node) => ({
			value: node.nodeUUID,
			label: node.hostname
		}))
	]);

	let modeOptions = [
		{ value: 'dataset', label: 'Single Dataset' },
		{ value: 'jail', label: 'Jail' }
	];

	let jailOptions = $derived([
		...jails.map((jail) => ({
			value: String(jail.id),
			label: jail.name
		}))
	]);

	function resetJobModal() {
		jobModal.open = false;
		jobModal.edit = false;
		jobModal.name = '';
		jobModal.targetId = targets.current[0]?.id ? String(targets.current[0].id) : '';
		jobModal.runnerNodeId = nodes[0]?.nodeUUID ?? '';
		jobModal.mode = 'dataset';
		jobModal.sourceDataset = '';
		jobModal.selectedJailId = '';
		jobModal.destSuffix = '';
		jobModal.pruneKeepLast = '0';
		jobModal.pruneTarget = false;
		jobModal.cronExpr = '0 * * * *';
		jobModal.enabled = true;
	}

	function openCreateJob() {
		resetJobModal();
		jobModal.open = true;
	}

	async function openEditJob() {
		if (!selectedJobId) return;
		const job = jobs.current.find((j) => j.id === selectedJobId);
		if (!job) return;

		jobModal.open = true;
		jobModal.edit = true;
		jobModal.name = job.name;
		jobModal.targetId = String(job.targetId);
		jobModal.runnerNodeId = job.runnerNodeId || nodes[0]?.nodeUUID || '';
		jobModal.mode = (job.mode as 'dataset' | 'jail') || 'dataset';
		jobModal.sourceDataset = job.sourceDataset || '';

		// Load jails if in jail mode
		if (job.mode === 'jail') {
			await loadJails();
			// Try to find the jail by matching the jailRootDataset
			if (job.jailRootDataset) {
				const matchingJail = jails.find((j: any) => {
					const baseStorage = j.storages?.find((s: any) => s.isBase);
					if (baseStorage) {
						const jailDataset = `${baseStorage.pool}/sylve/jails/${j.ctId}`;
						return jailDataset === job.jailRootDataset;
					}
					return false;
				});
				jobModal.selectedJailId = matchingJail ? String(matchingJail.id) : '';
			}
		} else {
			jobModal.selectedJailId = '';
		}

		jobModal.destSuffix = job.destSuffix || '';
		jobModal.pruneKeepLast = String(job.pruneKeepLast ?? 0);
		jobModal.pruneTarget = !!job.pruneTarget;
		jobModal.cronExpr = job.cronExpr;
		jobModal.enabled = job.enabled;
	}

	async function saveJob() {
		if (!jobModal.name.trim()) {
			toast.error('Name is required', { position: 'bottom-center' });
			return;
		}
		if (!jobModal.targetId) {
			toast.error('Target is required', { position: 'bottom-center' });
			return;
		}
		if (!jobModal.destSuffix.trim() && jobModal.mode === 'dataset') {
			toast.error('Destination suffix is required', { position: 'bottom-center' });
			return;
		}
		if (jobModal.mode === 'dataset' && !jobModal.sourceDataset.trim()) {
			toast.error('Source dataset is required for dataset mode', { position: 'bottom-center' });
			return;
		}
		if (jobModal.mode === 'jail' && !jobModal.selectedJailId) {
			toast.error('Jail selection is required for jail mode', { position: 'bottom-center' });
			return;
		}

		const pruneKeepLast = Number.parseInt(jobModal.pruneKeepLast || '0', 10);
		if (Number.isNaN(pruneKeepLast) || pruneKeepLast < 0) {
			toast.error('Prune keep value must be 0 or greater', { position: 'bottom-center' });
			return;
		}

		// Get jail dataset if in jail mode
		let jailDataset = '';
		if (jobModal.mode === 'jail' && jobModal.selectedJailId) {
			const selectedJail = jails.find((j: any) => j.id === Number(jobModal.selectedJailId));
			if (selectedJail) {
				const baseStorage = selectedJail.storages?.find((s: any) => s.isBase);
				if (baseStorage) {
					jailDataset = `${baseStorage.pool}/sylve/jails/${selectedJail.ctId}`;
				}
			}
		}

		const payload: BackupJobInput = {
			name: jobModal.name,
			targetId: Number(jobModal.targetId),
			runnerNodeId: jobModal.runnerNodeId,
			mode: jobModal.mode,
			sourceDataset: jobModal.mode === 'dataset' ? jobModal.sourceDataset : '',
			jailRootDataset: jobModal.mode === 'jail' ? jailDataset : '',
			destSuffix: jobModal.destSuffix,
			pruneKeepLast,
			pruneTarget: jobModal.pruneTarget,
			cronExpr: jobModal.cronExpr,
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
			activeRows = null;
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
			reload = true;
			return;
		}

		handleAPIError(response);
		toast.error('Failed to run job', { position: 'bottom-center' });
	}

	async function openRestoreModal() {
		if (!selectedJobId) return;
		restoreModal.open = true;
		restoreModal.loading = true;
		restoreModal.snapshots = [];
		restoreModal.selectedSnapshot = '';
		restoreModal.error = '';
		restoreModal.restoring = false;

		try {
			const snaps = await listBackupJobSnapshots(selectedJobId);
			restoreModal.snapshots = snaps;
			if (snaps.length > 0) {
				restoreModal.selectedSnapshot = snaps[snaps.length - 1].shortName;
			}
		} catch (e: any) {
			restoreModal.error = e?.message || 'Failed to load snapshots';
		} finally {
			restoreModal.loading = false;
		}
	}

	async function triggerRestore() {
		if (!selectedJobId || !restoreModal.selectedSnapshot) return;
		restoreModal.restoring = true;

		try {
			const response = await restoreBackupJob(selectedJobId, restoreModal.selectedSnapshot);
			if (response.status === 'success') {
				toast.success('Restore job started — check events for progress', {
					position: 'bottom-center'
				});
				restoreModal.open = false;
				reload = true;
				return;
			}
			handleAPIError(response);
			toast.error('Failed to start restore', { position: 'bottom-center' });
		} catch (e: any) {
			toast.error(e?.message || 'Failed to start restore', { position: 'bottom-center' });
		} finally {
			restoreModal.restoring = false;
		}
	}
</script>

{#snippet button(type: string)}
	{#if type === 'edit' && activeRows !== null && activeRows.length === 1}
		<Button onclick={openEditJob} size="sm" variant="outline" class="h-6">
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

	{#if type === 'run' && activeRows !== null && activeRows.length === 1}
		<Button onclick={triggerJob} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<Icon icon="mdi:play" class="mr-1 h-4 w-4" />
				<span>Run Now</span>
			</div>
		</Button>
	{/if}

	{#if type === 'restore' && activeRows !== null && activeRows.length === 1}
		<Button onclick={openRestoreModal} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<Icon icon="mdi:backup-restore" class="mr-1 h-4 w-4" />
				<span>Restore</span>
			</div>
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button
			onclick={openCreateJob}
			size="sm"
			class="h-6"
			disabled={targets.current.length === 0 || nodes.length === 0}
		>
			<div class="flex items-center">
				<Icon icon="gg:add" class="mr-1 h-4 w-4" />
				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}
		{@render button('run')}
		{@render button('restore')}

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
			name="backup-jobs-tt"
			bind:query
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
		/>
	</div>
</div>

<Dialog.Root bind:open={jobModal.open}>
	<Dialog.Content class="w-[92%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>
				<div class="flex items-center gap-2">
					<Icon icon={jobModal.edit ? 'mdi:note-edit' : 'mdi:calendar-sync'} class="h-5 w-5" />
					<span>{jobModal.edit ? 'Edit Backup Job' : 'New Backup Job'}</span>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<CustomValueInput
				label="Name"
				placeholder="daily-backup"
				bind:value={jobModal.name}
				classes="space-y-1"
			/>

			<div class="grid grid-cols-3 gap-4">
				<SimpleSelect
					label="Target"
					placeholder="Select target"
					options={targetOptions}
					bind:value={jobModal.targetId}
					onChange={() => {}}
				/>

				<SimpleSelect
					label="Run On Node"
					placeholder="Select node"
					options={nodeOptions}
					bind:value={jobModal.runnerNodeId}
					onChange={() => {}}
				/>

				<SimpleSelect
					label="Mode"
					placeholder="Select mode"
					options={modeOptions}
					bind:value={jobModal.mode}
					onChange={() => {}}
				/>
			</div>

			<div class="grid grid-cols-2 gap-4">
				{#if jobModal.mode === 'dataset'}
					<CustomValueInput
						label="Source Dataset"
						placeholder="zroot/data"
						bind:value={jobModal.sourceDataset}
						classes="space-y-1"
					/>
				{:else}
					<SimpleSelect
						label="Jail"
						placeholder={jails.length === 0 ? 'No jails available' : 'Select jail'}
						options={jailOptions}
						bind:value={jobModal.selectedJailId}
						onChange={() => {}}
						disabled={jails.length === 0}
					/>
				{/if}

				<CustomValueInput
					label="Destination Suffix"
					placeholder="server1/data (appended to target's backup root)"
					bind:value={jobModal.destSuffix}
					classes="space-y-1"
				/>
			</div>

			<CustomValueInput
				label="Schedule (Cron, 5-field)"
				placeholder="0 * * * *"
				bind:value={jobModal.cronExpr}
				classes="space-y-1"
			/>

			<CustomValueInput
				label="Keep Last Snapshots (0 disables pruning)"
				placeholder="20"
				type="number"
				bind:value={jobModal.pruneKeepLast}
				classes="space-y-1"
			/>

			<CustomCheckbox
				label="Also prune on target"
				bind:checked={jobModal.pruneTarget}
				classes="flex items-center gap-2"
			/>

			<CustomCheckbox
				label="Enabled"
				bind:checked={jobModal.enabled}
				classes="flex items-center gap-2"
			/>

			<div class="rounded-md bg-muted p-3 text-sm">
				<p class="font-medium">Job Summary:</p>
				<ul class="mt-2 list-inside list-disc space-y-1 text-muted-foreground">
					{#if jobModal.mode === 'jail'}
						{@const selectedJail = jails.find((j: any) => j.id === Number(jobModal.selectedJailId))}
						<li>
							Jail <code class="rounded bg-background px-1"
								>{selectedJail?.name || '(not selected)'}</code
							> will be backed up
						</li>
					{:else}
						<li>
							Dataset <code class="rounded bg-background px-1"
								>{jobModal.sourceDataset || '(not set)'}</code
							> will be backed up
						</li>
					{/if}
					<li>
						Destination suffix: <code class="rounded bg-background px-1"
							>{jobModal.destSuffix || '(auto)'}</code
						>
					</li>
					<li>
						Schedule: <code class="rounded bg-background px-1"
							>{cronToHuman(jobModal.cronExpr) || '(not set)'}</code
						>
					</li>
					<li>
						Pruning: <code class="rounded bg-background px-1"
							>{Number.parseInt(jobModal.pruneKeepLast || '0', 10) > 0
								? `Keep last ${Number.parseInt(jobModal.pruneKeepLast || '0', 10)} snapshots`
								: 'Disabled'}</code
						>
					</li>
					<li>
						Target prune: <code class="rounded bg-background px-1"
							>{jobModal.pruneTarget ? 'Enabled' : 'Disabled'}</code
						>
					</li>
				</ul>
			</div>
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

<Dialog.Root bind:open={restoreModal.open}>
	<Dialog.Content class="w-[92%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>
				<div class="flex items-center gap-2">
					<Icon icon="mdi:backup-restore" class="h-5 w-5" />
					<span>Restore from Backup</span>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			{#if restoreModal.loading}
				<div class="flex items-center justify-center py-8">
					<Icon icon="mdi:loading" class="h-6 w-6 animate-spin text-muted-foreground" />
					<span class="ml-2 text-muted-foreground">Loading snapshots from remote target...</span>
				</div>
			{:else if restoreModal.error}
				<div class="rounded-md border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-500">
					<p class="font-medium">Failed to load snapshots</p>
					<p class="mt-1">{restoreModal.error}</p>
				</div>
			{:else if restoreModal.snapshots.length === 0}
				<div class="rounded-md bg-muted p-4 text-center text-sm text-muted-foreground">
					No snapshots found on the backup target. Run a backup first.
				</div>
			{:else}
				<div class="max-h-72 overflow-auto rounded-md border">
					<table class="w-full text-sm">
						<thead class="sticky top-0 bg-muted">
							<tr>
								<th class="w-8 p-2"></th>
								<th class="p-2 text-left font-medium">Snapshot</th>
								<th class="p-2 text-left font-medium">Date</th>
								<th class="p-2 text-right font-medium">Used</th>
								<th class="p-2 text-right font-medium">Refer</th>
							</tr>
						</thead>
						<tbody>
							{#each [...restoreModal.snapshots].reverse() as snap}
								<tr
									class="cursor-pointer border-t transition-colors hover:bg-accent {restoreModal.selectedSnapshot ===
									snap.shortName
										? 'bg-accent'
										: ''}"
									onclick={() => (restoreModal.selectedSnapshot = snap.shortName)}
								>
									<td class="p-2 text-center">
										{#if restoreModal.selectedSnapshot === snap.shortName}
											<Icon icon="mdi:radiobox-marked" class="h-4 w-4 text-primary" />
										{:else}
											<Icon icon="mdi:radiobox-blank" class="h-4 w-4 text-muted-foreground" />
										{/if}
									</td>
									<td class="p-2 font-mono text-xs">{snap.shortName}</td>
									<td class="p-2 text-xs text-muted-foreground">
										{snap.creation ? new Date(snap.creation).toLocaleString() : '-'}
									</td>
									<td class="p-2 text-right text-xs">{snap.used}</td>
									<td class="p-2 text-right text-xs">{snap.refer}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>

				<div class="rounded-md border border-yellow-500/30 bg-yellow-500/10 p-3 text-sm">
					<p class="font-medium text-yellow-600 dark:text-yellow-400">
						<Icon icon="mdi:alert" class="mr-1 inline h-4 w-4" />
						Restore Warning
					</p>
					<ul class="mt-2 list-inside list-disc space-y-1 text-muted-foreground">
						<li>
							The current dataset will be renamed to <code class="rounded bg-background px-1"
								>.pre-restore</code
							>
						</li>
						<li>
							Data from <code class="rounded bg-background px-1"
								>{restoreModal.selectedSnapshot}</code
							> will be restored in its place
						</li>
						<li>No data is deleted on the backup target — all snapshots remain available</li>
						<li>
							You can delete the <code class="rounded bg-background px-1">.pre-restore</code> dataset
							later to reclaim space
						</li>
					</ul>
				</div>
			{/if}
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={() => (restoreModal.open = false)}>Cancel</Button>
			<Button
				onclick={triggerRestore}
				disabled={!restoreModal.selectedSnapshot || restoreModal.restoring || restoreModal.loading}
				variant="destructive"
			>
				{#if restoreModal.restoring}
					<Icon icon="mdi:loading" class="mr-1 h-4 w-4 animate-spin" />
					Restoring...
				{:else}
					<Icon icon="mdi:backup-restore" class="mr-1 h-4 w-4" />
					Restore
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
