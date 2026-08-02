<script lang="ts">
	import {
		deleteBackupJob,
		listBackupJobsResult,
		listBackupTargetsResult,
		runBackupJob
	} from '$lib/api/cluster/backups';
	import Form from '$lib/components/custom/DataCenter/Backups/Jobs/Form.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import OOBRestore from '$lib/components/custom/DataCenter/Backups/Jobs/OOBRestore.svelte';
	import Restore from '$lib/components/custom/DataCenter/Backups/Jobs/Restore.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import ValueViewer from '$lib/components/custom/Dialog/ValueViewer.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { BackupGuestRef } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import {
		backupJobsUnavailableSources,
		type BackupJobsPageData
	} from '$lib/utils/backup-jobs-page-state';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime, cronToHuman } from '$lib/utils/time';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	let {
		data,
		vmRid = 0,
		vmHostname = ''
	}: {
		data: BackupJobsPageData;
		vmRid?: number;
		vmHostname?: string;
	} = $props();

	// svelte-ignore state_referenced_locally
	let targetsAvailable = $state(data.availability.targets);
	// svelte-ignore state_referenced_locally
	let jobsAvailable = $state(data.availability.jobs);

	// svelte-ignore state_referenced_locally
	let targets = resource(
		() => 'backup-targets',
		async () => {
			const res = await listBackupTargetsResult();
			if (isAPIResponse(res)) {
				targetsAvailable = false;
				return [];
			}
			targetsAvailable = true;
			updateCache('backup-targets', res);
			return res;
		},
		{ initialValue: data.targets }
	);

	// svelte-ignore state_referenced_locally
	let jobs = resource(
		() => (vmRid > 0 ? `backup-jobs-vm-${vmRid}` : 'backup-jobs'),
		async (key: string) => {
			const res = await listBackupJobsResult(undefined, vmRid);
			if (isAPIResponse(res)) {
				jobsAvailable = false;
				return [];
			}
			jobsAvailable = true;
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.jobs }
	);

	// svelte-ignore state_referenced_locally
	let nodes = $state(data.nodes);
	let reload = $state(false);
	let standaloneMode = $derived(data.clusterMode === 'standalone');
	let unavailableSources = $derived(
		backupJobsUnavailableSources(data.clusterMode, {
			...data.availability,
			targets: targetsAvailable,
			jobs: jobsAvailable
		})
	);
	let mutationsAvailable = $derived(unavailableSources.length === 0);

	watch(
		() => reload,
		(value) => {
			if (value) {
				setTimeout(() => {
					jobs.refetch();
					targets.refetch();
					reload = false;
				}, 500);
			}
		}
	);

	let query = $state('');
	let activeRows: Row[] = $state([]);

	let selectedJobId = $derived(
		activeRows.length === 1 && typeof activeRows[0].id === 'number' ? Number(activeRows[0].id) : 0
	);

	let selectedJob = $derived.by(() => {
		if (!selectedJobId) return null;
		return jobs.current.find((job) => job.id === selectedJobId) || null;
	});

	let jobModal = $state({ open: false, edit: false });
	let restoreModalOpen = $state(false);
	let restoreTargetModalOpen = $state(false);
	let deleteModalOpen = $state(false);
	let errorModal = $state({ open: false, title: '', value: '' });
	let canViewError = $derived(Boolean(selectedJob?.lastError.trim()));
	let selectedJobExecutionBlocked = $derived(
		selectedJob?.lastStatus === 'runner_rebind_pending' ||
			selectedJob?.lastStatus === 'repair_required'
	);

	function parseGuestFromDatasetPath(dataset: string): BackupGuestRef {
		const jailMatch = dataset.match(/(?:^|\/)jails\/(\d+)(?:$|[/.])/);
		if (jailMatch) {
			const parsed = Number.parseInt(jailMatch[1], 10);
			if (!Number.isNaN(parsed) && parsed > 0) {
				return { kind: 'jail', id: parsed };
			}
		}

		const vmMatch = dataset.match(/(?:^|\/)virtual-machines\/(\d+)(?:$|[/.])/);
		if (vmMatch) {
			const parsed = Number.parseInt(vmMatch[1], 10);
			if (!Number.isNaN(parsed) && parsed > 0) {
				return { kind: 'vm', id: parsed };
			}
		}

		return { kind: 'dataset', id: 0 };
	}

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
			field: 'status',
			title: 'Status',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData() as { enabled: boolean; lastStatus: string };
				const enabled = row.enabled;
				const lastStatus = row.lastStatus;
				const icons = [];

				if (!enabled) {
					icons.push(renderWithIcon('mdi:close-circle', 'Disabled', 'text-red-500'));
				} else {
					icons.push(renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500'));
				}

				if (lastStatus === 'success') {
					icons.push(renderWithIcon('mdi:check-circle', 'Success', 'text-green-500'));
				} else if (lastStatus === 'failed') {
					icons.push(renderWithIcon('mdi:close-circle', 'Failed', 'text-red-500'));
				} else if (lastStatus === 'running') {
					icons.push(renderWithIcon('mdi:progress-clock', 'Running', 'text-yellow-500'));
				} else if (lastStatus === 'runner_rebind_pending') {
					icons.push(
						renderWithIcon('mdi:server-network', 'Runner rebind pending', 'text-amber-500')
					);
				} else if (lastStatus === 'repair_required') {
					icons.push(renderWithIcon('mdi:wrench-clock', 'Repair required', 'text-red-500'));
				}

				return `<div class="flex flex-col gap-1">${icons.join(' ')}</div>`;
			}
		},
		{ field: 'name', title: 'Name' },
		{
			field: 'sourceDataset',
			title: 'Source',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData() as {
					mode?: 'dataset' | 'jail' | 'vm';
					sourceGuestId?: number;
					encrypted?: boolean;
				};
				const value = String(cell.getValue() || '');
				const lockIcon = row.encrypted
					? renderWithIcon('mdi:lock', 'Encrypted', 'text-green-500')
					: renderWithIcon('mdi:lock-open-variant', '', 'text-muted-foreground');

				if (row.mode === 'jail') {
					const label =
						row.sourceGuestId && row.sourceGuestId > 0 ? String(row.sourceGuestId) : value;
					return `${lockIcon} ${renderWithIcon('hugeicons:prison', label || '-')}`;
				}

				if (row.mode === 'vm') {
					const label =
						row.sourceGuestId && row.sourceGuestId > 0 ? String(row.sourceGuestId) : value;
					return `${lockIcon} ${renderWithIcon('material-symbols:monitor-outline', label || '-')}`;
				}

				if (row.mode === 'dataset') {
					return `${lockIcon} ${renderWithIcon('material-symbols:files', value || '-')}`;
				}

				return `${lockIcon} ${value || '-'}`;
			}
		},
		{
			field: 'target',
			title: 'Target',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData() as { runnerNodeId?: string; targetId?: number };
				const nodeId = row.runnerNodeId || '';
				const targetId = row.targetId || 0;
				const nodeName = nodeNameById[nodeId] || nodeId || '-';
				const targetName = targetNameById[targetId] || String(targetId);
				return `${nodeName} → ${targetName}`;
			}
		},
		{
			field: 'destSuffix',
			title: 'Dest Suffix',
			formatter: (cell: CellComponent) => cell.getValue() || '-',
			visible: false
		},
		{
			field: 'pruneKeepLast',
			title: 'Prune',
			formatter: (cell: CellComponent) => {
				const value = Number(cell.getValue() || 0);
				return value > 0 ? `Keep ${value}` : 'Off';
			}
		},
		{
			field: 'cronExpr',
			title: 'Schedule',
			formatter: (cell: CellComponent) => cronToHuman(String(cell.getValue() || ''))
		},
		{
			field: 'lastRunAt',
			title: 'Last Run',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		}
	];

	let tableData = $derived({
		rows: jobs.current.map((job) => {
			const primaryDataset =
				job.mode === 'jail'
					? job.jailRootDataset || job.sourceDataset || ''
					: job.sourceDataset || job.jailRootDataset || '';
			const parsedSource = parseGuestFromDatasetPath(primaryDataset);
			const sourceLabel =
				job.friendlySrc ||
				(parsedSource.id > 0 ? String(parsedSource.id) : primaryDataset || job.sourceDataset || '');

			return {
				sourceGuestId: parsedSource.id,
				id: job.id,
				status: job.id,
				target: job.id,
				name: job.name,
				targetId: job.targetId,
				runnerNodeId: job.runnerNodeId || '',
				mode: job.mode,
				sourceDataset: sourceLabel,
				destSuffix: job.destSuffix || '',
				pruneKeepLast: job.pruneKeepLast || 0,
				pruneTarget: job.pruneTarget || false,
				cronExpr: job.cronExpr,
				enabled: job.enabled,
				encrypted: job.encrypted || false,
				lastRunAt: job.lastRunAt,
				lastStatus: job.lastStatus || '',
				lastError: job.lastError || ''
			};
		}),
		columns: jobColumns
	});

	function openCreateJob() {
		if (!mutationsAvailable) return;
		jobModal.edit = false;
		jobModal.open = true;
		activeRows = [];
	}

	function openEditJob() {
		if (!mutationsAvailable || !selectedJobId) return;
		jobModal.edit = true;
		jobModal.open = true;
	}

	function openRestoreModal() {
		if (!mutationsAvailable || !selectedJobId) return;
		restoreModalOpen = true;
	}

	function openErrorModal() {
		if (!selectedJob) return;
		const error = selectedJob.lastError.trim();
		if (!error) return;

		errorModal.title = `${selectedJob.name} Error`;
		errorModal.value = error;
		errorModal.open = true;
	}

	async function removeJob() {
		if (!mutationsAvailable || !selectedJobId) return;
		const response = await deleteBackupJob(selectedJobId);
		if (response.status === 'success') {
			toast.success('Backup job deleted', { position: 'bottom-center' });
			reload = true;
			deleteModalOpen = false;
			activeRows = [];
			return;
		}

		handleAPIError(response);
		toast.error('Failed to delete job', { position: 'bottom-center' });
	}

	async function triggerJob() {
		if (!mutationsAvailable || !selectedJobId) return;
		const response = await runBackupJob(selectedJobId);
		if (response.status === 'success') {
			toast.success('Backup job started', { position: 'bottom-center' });
			reload = true;
			return;
		}

		handleAPIError(response);
		toast.error('Failed to run job', { position: 'bottom-center' });
	}
</script>

{#snippet button(type: string)}
	{#if activeRows.length === 1}
		{#if type === 'edit'}
			<Button
				onclick={openEditJob}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={!mutationsAvailable}
			>
				<SpanWithIcon icon="icon-[mdi--note-edit]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{/if}

		{#if type === 'delete'}
			<Button
				onclick={() => (deleteModalOpen = true)}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={!mutationsAvailable}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}

		{#if type === 'run'}
			<Button
				onclick={triggerJob}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={!mutationsAvailable || selectedJobExecutionBlocked}
			>
				<SpanWithIcon icon="icon-[mdi--play]" size="h-4 w-4" gap="gap-2" title="Run Now" />
			</Button>
		{/if}

		{#if type === 'restore'}
			<Button
				onclick={openRestoreModal}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={!mutationsAvailable || selectedJobExecutionBlocked}
			>
				<SpanWithIcon
					icon="icon-[mdi--backup-restore]"
					size="h-4 w-4"
					gap="gap-2"
					title="Restore"
				/>
			</Button>
		{/if}

		{#if type === 'error' && canViewError}
			<Button onclick={openErrorModal} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon
					icon="icon-[mdi--alert-circle-outline]"
					size="h-4 w-4"
					gap="gap-2"
					title="View Error"
				/>
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	{#if unavailableSources.length > 0}
		<div class="border-b border-destructive/40 bg-destructive/10 px-3 py-2 text-sm">
			<p class="font-medium text-destructive">Backup job data is unavailable</p>
			<p class="text-muted-foreground">
				Could not load {unavailableSources.join(', ')}. Changes are disabled until the page is
				refreshed successfully.
			</p>
		</div>
	{/if}

	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button
			onclick={openCreateJob}
			size="sm"
			class="h-6"
			disabled={!mutationsAvailable || targets.current.length === 0 || nodes.length === 0}
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}
		{@render button('run')}
		{@render button('restore')}
		{@render button('error')}

		{#if vmRid === 0}
			<Button
				onclick={() => (restoreTargetModalOpen = true)}
				size="sm"
				variant="outline"
				class="ml-auto h-6"
				disabled={!mutationsAvailable || targets.current.length === 0}
			>
				<div class="flex items-center">
					<span class="icon-[mdi--database-sync-outline] mr-1 h-4 w-4"></span>
					<span>OOB Restore</span>
				</div>
			</Button>
		{/if}

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
			name="backup-jobs-tt"
			bind:query
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
		/>
	</div>
</div>

<Form
	bind:open={jobModal.open}
	bind:edit={jobModal.edit}
	bind:reload
	{selectedJob}
	targets={targets.current}
	{nodes}
	localNodeId={data.localNodeId}
	{standaloneMode}
	scopedVmRid={vmRid}
	scopedVmHostname={vmHostname}
/>

<Restore bind:open={restoreModalOpen} bind:reload {selectedJob} {nodes} />
<OOBRestore bind:open={restoreTargetModalOpen} bind:reload targets={targets.current} {nodes} />

<AlertDialog
	open={deleteModalOpen}
	names={{ parent: 'job', element: selectedJob?.name || '' }}
	customTitle="This removes only the job configuration and schedule. Existing backup snapshots on the source and backup target are preserved and will no longer be managed by this job."
	actions={{
		onConfirm: removeJob,
		onCancel: () => {
			deleteModalOpen = false;
		}
	}}
/>

<ValueViewer bind:open={errorModal.open} title={errorModal.title} value={errorModal.value} />
