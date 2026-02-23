<script lang="ts">
	import {
		createBackupJob,
		deleteBackupJob,
		getBackupTargetJailMetadata,
		listBackupJobs,
		listBackupTargets,
		listBackupJobSnapshots,
		listBackupTargetDatasets,
		listBackupTargetDatasetSnapshots,
		restoreBackupJob,
		restoreBackupFromTarget,
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
	import type {
		BackupJailMetadataInfo,
		BackupJob,
		BackupTarget,
		BackupTargetDatasetInfo,
		SnapshotInfo
	} from '$lib/types/cluster/backups';
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
	import { humanFormatBytes } from '$lib/utils/string';

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

	watch(
		[() => restoreTargetModal.datasets, () => restoreTargetModal.open],
		([datasets, isOpen]) => {
			if (!isOpen) return;
			if (datasets.length === 0) {
				restoreTargetModal.dataset = '';
				return;
			}

			const hasCurrentSelection = datasets.some(
				(dataset) => dataset.name === restoreTargetModal.dataset
			);
			if (hasCurrentSelection) return;

			restoreTargetModal.dataset = datasets[0].name;
			void onRestoreTargetDatasetChange();
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
		enabled: true,
		stopBeforeBackup: false
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

	let restoreTargetModal = $state({
		open: false,
		loadingDatasets: false,
		loadingSnapshots: false,
		loadingMetadata: false,
		restoring: false,
		targetId: '',
		dataset: '',
		snapshot: '',
		destinationDataset: '',
		datasets: [] as BackupTargetDatasetInfo[],
		snapshots: [] as SnapshotInfo[],
		jailMetadata: null as BackupJailMetadataInfo | null,
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
		{
			field: 'destSuffix',
			title: 'Dest Suffix',
			formatter: (cell: CellComponent) => cell.getValue() || '-'
		},
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

	function formatLineageLabel(lineage: string, outOfBand: boolean): string {
		switch (lineage) {
			case 'active':
				return 'Current';
			case 'rotated':
				return 'OOB lineage';
			case 'preserved':
				return 'System preserved';
			default:
				return outOfBand ? 'Out of band' : 'Current';
		}
	}

	function formatSnapshotCount(count: number): string {
		return `${count} ${count === 1 ? 'snap' : 'snaps'}`;
	}

	function formatLineageMarker(dataset: BackupTargetDatasetInfo): string {
		const suffix = dataset.suffix || dataset.name;
		const leaf = suffix.split('/').pop() || suffix;

		if ((dataset.lineage || 'active') === 'rotated') {
			const marker = leaf.split('_zelta_')[1] || '';
			return marker ? marker.replace(/_/g, ' ') : '';
		}

		if ((dataset.lineage || 'active') === 'preserved') {
			const marker = leaf.split('.pre_sylve_')[1] || '';
			return marker ? `id ${marker.slice(-8)}` : '';
		}

		return '';
	}

	function formatRestoreTargetDatasetLabel(dataset: BackupTargetDatasetInfo): string {
		const base = dataset.baseSuffix || dataset.suffix || dataset.name;
		const lineage = formatLineageLabel(dataset.lineage || 'active', !!dataset.outOfBand);
		const lineageMarker = formatLineageMarker(dataset);
		const lineageSummary = lineageMarker ? `${lineage} ${lineageMarker}` : lineage;

		if (dataset.kind === 'jail' && dataset.jailCtId) {
			return `jails/${dataset.jailCtId} (${lineageSummary}, ${formatSnapshotCount(dataset.snapshotCount)})`;
		}

		return `${base} (${lineageSummary}, ${formatSnapshotCount(dataset.snapshotCount)})`;
	}

	let visibleRestoreTargetDatasets = $derived.by(() => restoreTargetModal.datasets);

	let restoreTargetDatasetOptions = $derived(
		visibleRestoreTargetDatasets.map((dataset) => ({
			value: dataset.name,
			label: formatRestoreTargetDatasetLabel(dataset)
		}))
	);

	let restoreTargetSnapshotOptions = $derived(
		[...restoreTargetModal.snapshots].reverse().map((snapshot) => ({
			value: snapshot.name || snapshot.shortName,
			label: `${formatRestoreSnapshotDate(snapshot)}${snapshot.outOfBand || (snapshot.lineage || 'active') !== 'active' ? ` (${snapshotLineageLabel(snapshot)})` : ''}`
		}))
	);

	function formatRestoreSnapshotDate(snapshot: SnapshotInfo): string {
		if (!snapshot.creation) return '-';
		const date = new Date(snapshot.creation);
		if (Number.isNaN(date.getTime())) {
			return snapshot.creation;
		}
		return date.toLocaleString();
	}

	let selectedRestoreSnapshotDate = $derived.by(() => {
		if (!restoreModal.selectedSnapshot) return '';
		const selected = restoreModal.snapshots.find(
			(snapshot) => snapshot.name === restoreModal.selectedSnapshot
		);
		if (!selected) return '';
		return formatRestoreSnapshotDate(selected);
	});

	let selectedRestoreSnapshot = $derived.by(
		() =>
			restoreModal.snapshots.find((snapshot) => snapshot.name === restoreModal.selectedSnapshot) ||
			null
	);

	let restoreModalHasOutOfBandSnapshots = $derived.by(() =>
		restoreModal.snapshots.some(
			(snapshot) => !!snapshot.outOfBand || (snapshot.lineage || 'active') !== 'active'
		)
	);

	function snapshotLineageLabel(snapshot: SnapshotInfo): string {
		return formatLineageLabel(snapshot.lineage || 'active', !!snapshot.outOfBand);
	}

	function snapshotLineageClasses(snapshot: SnapshotInfo): string {
		const lineage = snapshot.lineage || 'active';
		if (lineage === 'active' && !snapshot.outOfBand) {
			return 'border-green-500/20 bg-green-500/10 text-green-600';
		}
		if (lineage === 'preserved') {
			return 'border-orange-500/20 bg-orange-500/10 text-orange-600';
		}
		return 'border-blue-500/20 bg-blue-500/10 text-blue-600';
	}

	let selectedRestoreTargetSnapshot = $derived.by(
		() =>
			restoreTargetModal.snapshots.find(
				(snapshot) => (snapshot.name || snapshot.shortName) === restoreTargetModal.snapshot
			) || null
	);

	let restoreTargetModalHasOutOfBandSnapshots = $derived.by(() =>
		restoreTargetModal.snapshots.some(
			(snapshot) => !!snapshot.outOfBand || (snapshot.lineage || 'active') !== 'active'
		)
	);

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
		jobModal.stopBeforeBackup = false;
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
		jobModal.stopBeforeBackup = !!job.stopBeforeBackup;
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
			enabled: jobModal.enabled,
			stopBeforeBackup: jobModal.stopBeforeBackup
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
				restoreModal.selectedSnapshot = snaps[snaps.length - 1].name;
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

	function resetRestoreTargetModal() {
		restoreTargetModal.open = false;
		restoreTargetModal.loadingDatasets = false;
		restoreTargetModal.loadingSnapshots = false;
		restoreTargetModal.loadingMetadata = false;
		restoreTargetModal.restoring = false;
		restoreTargetModal.targetId = '';
		restoreTargetModal.dataset = '';
		restoreTargetModal.snapshot = '';
		restoreTargetModal.destinationDataset = '';
		restoreTargetModal.datasets = [];
		restoreTargetModal.snapshots = [];
		restoreTargetModal.jailMetadata = null;
		restoreTargetModal.error = '';
	}

	function inferJailDestinationDataset(target: BackupTarget | undefined, dataset: string): string {
		if (!target) return '';
		const jailMatch = dataset.match(/\/jails\/(\d+)(?:$|\/)/);
		if (!jailMatch) return '';
		const ctid = jailMatch[1];
		const pool = target.backupRoot.split('/')[0] || '';
		if (!pool) return '';
		return `${pool}/sylve/jails/${ctid}`;
	}

	async function openRestoreFromTargetModal() {
		restoreTargetModal.open = true;
		restoreTargetModal.error = '';
		restoreTargetModal.targetId = targetOptions[0]?.value || '';
		restoreTargetModal.dataset = '';
		restoreTargetModal.snapshot = '';
		restoreTargetModal.destinationDataset = '';
		restoreTargetModal.datasets = [];
		restoreTargetModal.snapshots = [];
		restoreTargetModal.jailMetadata = null;

		if (!restoreTargetModal.targetId) {
			restoreTargetModal.error = 'No backup targets available';
			return;
		}

		await onRestoreTargetTargetChange();
	}

	async function onRestoreTargetTargetChange() {
		const targetId = Number.parseInt(restoreTargetModal.targetId || '0', 10);
		if (!targetId) return;

		restoreTargetModal.loadingDatasets = true;
		restoreTargetModal.error = '';
		restoreTargetModal.dataset = '';
		restoreTargetModal.snapshot = '';
		restoreTargetModal.destinationDataset = '';
		restoreTargetModal.datasets = [];
		restoreTargetModal.snapshots = [];
		restoreTargetModal.jailMetadata = null;

		try {
			const datasets = await listBackupTargetDatasets(targetId);
			restoreTargetModal.datasets = datasets;
			let preferredDatasets = datasets.filter(
				(dataset) => (dataset.lineage || 'active') === 'active'
			);

			if (preferredDatasets.length === 0) {
				preferredDatasets = datasets;
			}

			if (preferredDatasets.length > 0) {
				restoreTargetModal.dataset = preferredDatasets[0].name;
				await onRestoreTargetDatasetChange();
			}
		} catch (e: any) {
			restoreTargetModal.error = e?.message || 'Failed to load target datasets';
		} finally {
			restoreTargetModal.loadingDatasets = false;
		}
	}

	async function onRestoreTargetDatasetChange() {
		const targetId = Number.parseInt(restoreTargetModal.targetId || '0', 10);
		const dataset = restoreTargetModal.dataset;
		if (!targetId || !dataset) return;

		restoreTargetModal.loadingSnapshots = true;
		restoreTargetModal.loadingMetadata = true;
		restoreTargetModal.error = '';
		restoreTargetModal.snapshot = '';
		restoreTargetModal.snapshots = [];
		restoreTargetModal.jailMetadata = null;

		const selectedTarget = targets.current.find((t) => t.id === targetId);
		restoreTargetModal.destinationDataset = inferJailDestinationDataset(selectedTarget, dataset);

		try {
			const [snapshots, metadata] = await Promise.all([
				listBackupTargetDatasetSnapshots(targetId, dataset),
				getBackupTargetJailMetadata(targetId, dataset)
			]);
			restoreTargetModal.snapshots = snapshots;
			if (snapshots.length > 0) {
				const latest = snapshots[snapshots.length - 1];
				restoreTargetModal.snapshot = latest.name || latest.shortName;
			} else {
				restoreTargetModal.snapshot = '';
			}

			restoreTargetModal.jailMetadata = metadata;
			if (metadata?.basePool && metadata?.ctId) {
				restoreTargetModal.destinationDataset = `${metadata.basePool}/sylve/jails/${metadata.ctId}`;
			}
		} catch (e: any) {
			restoreTargetModal.error = e?.message || 'Failed to load dataset details';
		} finally {
			restoreTargetModal.loadingSnapshots = false;
			restoreTargetModal.loadingMetadata = false;
		}
	}

	async function triggerRestoreFromTarget() {
		const targetId = Number.parseInt(restoreTargetModal.targetId || '0', 10);
		if (!targetId || !restoreTargetModal.dataset || !restoreTargetModal.snapshot) return;
		if (!restoreTargetModal.destinationDataset.trim()) {
			toast.error('Destination dataset is required', { position: 'bottom-center' });
			return;
		}

		restoreTargetModal.restoring = true;
		try {
			const response = await restoreBackupFromTarget(targetId, {
				remoteDataset: restoreTargetModal.dataset,
				snapshot: restoreTargetModal.snapshot,
				destinationDataset: restoreTargetModal.destinationDataset.trim()
			});
			if (response.status === 'success') {
				toast.success('Restore job started — check events for progress', {
					position: 'bottom-center'
				});
				restoreTargetModal.open = false;
				reload = true;
				return;
			}
			handleAPIError(response);
			toast.error('Failed to start restore', { position: 'bottom-center' });
		} catch (e: any) {
			toast.error(e?.message || 'Failed to start restore', { position: 'bottom-center' });
		} finally {
			restoreTargetModal.restoring = false;
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

		<Button
			onclick={openRestoreFromTargetModal}
			size="sm"
			variant="outline"
			class="h-6 ml-auto"
			disabled={targets.current.length === 0}
		>
			<div class="flex items-center">
				<Icon icon="mdi:database-sync-outline" class="mr-1 h-4 w-4" />
				<span>OOB Restore</span>
			</div>
		</Button>

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
	<Dialog.Content class="max-h-[90vh] min-w-1/2 overflow-y-auto p-5">
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

			<div class="grid grid-cols-2 gap-4">
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
			</div>

			<div class="flex flex-row gap-4">
				<CustomCheckbox
					label="Enabled"
					bind:checked={jobModal.enabled}
					classes="flex items-center gap-2"
				/>

				<CustomCheckbox
					label="Prune on target"
					bind:checked={jobModal.pruneTarget}
					classes="flex items-center gap-2"
				/>

				<CustomCheckbox
					label="Stop before backup"
					bind:checked={jobModal.stopBeforeBackup}
					classes="flex items-center gap-2"
				/>
			</div>

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
					<li>
						Stop before backup: <code class="rounded bg-background px-1"
							>{jobModal.stopBeforeBackup ? 'Enabled' : 'Disabled'}</code
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
				{#if restoreModalHasOutOfBandSnapshots}
					<div
						class="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-sm text-blue-700"
					>
						Some backups are from out-of-band lineages. Regular prune count applies to the current
						lineage only.
					</div>
				{/if}

				<div class="max-h-72 overflow-auto rounded-md border">
					<table class="w-full text-sm">
						<thead class="sticky top-0 bg-muted">
							<tr>
								<th class="w-8 p-2"></th>
								<th class="p-2 text-left font-medium">Backup Date</th>
								<th class="p-2 text-left font-medium">Lineage</th>
								<th class="p-2 text-right font-medium">Used</th>
								<th class="p-2 text-right font-medium">Refer</th>
							</tr>
						</thead>
						<tbody>
							{#each [...restoreModal.snapshots].reverse() as snap}
								<tr
									class="cursor-pointer border-t transition-colors hover:bg-accent {restoreModal.selectedSnapshot ===
									snap.name
										? 'bg-accent'
										: ''}"
									onclick={() => (restoreModal.selectedSnapshot = snap.name)}
									title={snap.name}
								>
									<td class="p-2 text-center">
										{#if restoreModal.selectedSnapshot === snap.name}
											<Icon icon="mdi:radiobox-marked" class="h-4 w-4 text-primary" />
										{:else}
											<Icon icon="mdi:radiobox-blank" class="h-4 w-4 text-muted-foreground" />
										{/if}
									</td>
									<td class="p-2 text-xs text-muted-foreground"
										>{formatRestoreSnapshotDate(snap)}</td
									>
									<td class="p-2 text-xs">
										<span
											class={`inline-flex items-center rounded-md border px-2 py-0.5 ${snapshotLineageClasses(
												snap
											)}`}>{snapshotLineageLabel(snap)}</span
										>
									</td>
									<td class="p-2 text-right text-xs text-muted-foreground"
										>{humanFormatBytes(snap.used)}</td
									>
									<td class="p-2 text-right text-xs text-muted-foreground"
										>{humanFormatBytes(snap.refer)}</td
									>
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
								>{selectedRestoreSnapshotDate || restoreModal.selectedSnapshot}</code
							> will be restored
						</li>
						{#if selectedRestoreSnapshot && (selectedRestoreSnapshot.outOfBand || selectedRestoreSnapshot.lineage !== 'active')}
							<li>
								This snapshot is from <code class="rounded bg-background px-1"
									>{snapshotLineageLabel(selectedRestoreSnapshot)}</code
								> and may not be counted by active-lineage prune.
							</li>
						{/if}
						<li>No deletion on target, all snapshots remain available</li>
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

<Dialog.Root bind:open={restoreTargetModal.open}>
	<Dialog.Content class="w-[92%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>
				<div class="flex items-center gap-2">
					<Icon icon="mdi:database-sync-outline" class="h-5 w-5" />
					<span>Restore From Target Dataset</span>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
				<SimpleSelect
					label="Target"
					placeholder="Select target"
					options={targetOptions}
					bind:value={restoreTargetModal.targetId}
					onChange={onRestoreTargetTargetChange}
				/>

				<SimpleSelect
					label="Dataset on Target"
					placeholder={restoreTargetModal.loadingDatasets
						? 'Loading datasets...'
						: visibleRestoreTargetDatasets.length === 0
							? 'No restorable datasets found'
							: 'Select dataset'}
					options={restoreTargetDatasetOptions}
					bind:value={restoreTargetModal.dataset}
					onChange={onRestoreTargetDatasetChange}
					disabled={restoreTargetModal.loadingDatasets || visibleRestoreTargetDatasets.length === 0}
				/>
			</div>

			{#if restoreTargetModalHasOutOfBandSnapshots}
				<div class="rounded-md border border-blue-500/30 bg-blue-500/10 p-3 text-sm text-blue-700">
					Selected dataset is from a non-active lineage. Prune counts on active jobs apply to the
					active lineage only.
				</div>
			{/if}

			<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
				<SimpleSelect
					label="Snapshot"
					placeholder={restoreTargetModal.loadingSnapshots
						? 'Loading snapshots...'
						: restoreTargetModal.snapshots.length === 0
							? 'No snapshots found'
							: 'Select snapshot'}
					options={restoreTargetSnapshotOptions}
					bind:value={restoreTargetModal.snapshot}
					onChange={() => {}}
					disabled={restoreTargetModal.loadingSnapshots ||
						restoreTargetModal.snapshots.length === 0}
				/>

				<CustomValueInput
					label="Destination Dataset"
					placeholder="zroot/sylve/jails/105"
					bind:value={restoreTargetModal.destinationDataset}
					classes="space-y-1"
				/>
			</div>

			{#if restoreTargetModal.jailMetadata}
				<div class="rounded-md border bg-muted/40 p-3 text-sm">
					<p class="font-medium">Detected Jail Metadata</p>
					<div class="mt-2 grid grid-cols-1 gap-1 text-muted-foreground md:grid-cols-3">
						<div>
							Name:
							<code class="rounded bg-background px-1"
								>{restoreTargetModal.jailMetadata.name || '-'}</code
							>
						</div>
						<div>
							CT ID:
							<code class="rounded bg-background px-1">{restoreTargetModal.jailMetadata.ctId}</code>
						</div>
						<div>
							Base Pool:
							<code class="rounded bg-background px-1"
								>{restoreTargetModal.jailMetadata.basePool || '-'}</code
							>
						</div>
					</div>
				</div>
			{/if}

			{#if restoreTargetModal.error}
				<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
					{restoreTargetModal.error}
				</div>
			{/if}

			<div class="rounded-md border border-yellow-500/30 bg-yellow-500/10 p-3 text-sm">
				<p class="font-medium text-yellow-600 dark:text-yellow-400">
					<Icon icon="mdi:alert" class="mr-1 inline h-4 w-4" />
					Restore Warning
				</p>
				<ul class="mt-2 list-inside list-disc space-y-1 text-muted-foreground">
					<li>The destination dataset will be replaced if it already exists.</li>
					{#if selectedRestoreTargetSnapshot}
						<li>
							Selected backup date:
							<code class="rounded bg-background px-1"
								>{formatRestoreSnapshotDate(selectedRestoreTargetSnapshot)}</code
							>
						</li>
						{#if selectedRestoreTargetSnapshot.outOfBand || selectedRestoreTargetSnapshot.lineage !== 'active'}
							<li>
								Selected snapshot lineage:
								<code class="rounded bg-background px-1"
									>{snapshotLineageLabel(selectedRestoreTargetSnapshot)}</code
								>
							</li>
						{/if}
					{/if}
				</ul>
			</div>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={resetRestoreTargetModal}>Cancel</Button>
			<Button
				onclick={triggerRestoreFromTarget}
				disabled={restoreTargetModal.restoring ||
					restoreTargetModal.loadingDatasets ||
					restoreTargetModal.loadingSnapshots ||
					!restoreTargetModal.targetId ||
					!restoreTargetModal.dataset ||
					!restoreTargetModal.snapshot ||
					!restoreTargetModal.destinationDataset.trim()}
				variant="destructive"
			>
				{#if restoreTargetModal.restoring}
					<Icon icon="mdi:loading" class="mr-1 h-4 w-4 animate-spin" />
					Restoring...
				{:else}
					<Icon icon="mdi:database-sync-outline" class="mr-1 h-4 w-4" />
					Restore
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
