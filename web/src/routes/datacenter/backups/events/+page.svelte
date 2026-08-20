<script lang="ts">
	import { getBackupEventProgress, listBackupJobs } from '$lib/api/cluster/backups';
	import { getDetails, getNodesResult } from '$lib/api/cluster/cluster';
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { storage } from '$lib';
	import type { BackupEvent, BackupEventProgress, BackupJob } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { convertDbTime } from '$lib/utils/time';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';
	import { getJailsResult } from '$lib/api/jail/jail';
	import type { Jail, JailStorage } from '$lib/types/jail/jail';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	let filterJobId = $state('');
	let reload = $state(false);

	let jobs = resource(
		() => 'backup-jobs-for-filter',
		async () => {
			const res = await listBackupJobs();
			return res;
		},
		{ initialValue: [] as BackupJob[] }
	);
	let jobsById = $derived.by(() => {
		const currentJobs: Record<number, BackupJob> = {};
		for (const job of jobs.current) currentJobs[job.id] = job;
		return currentJobs;
	});
	let jobsLoading = $derived(jobs.loading);

	type NodeContext = {
		details: ClusterDetails | null;
		nodes: ClusterNode[];
	};
	let nodeContext = resource(
		() => 'backup-events-node-context',
		async (): Promise<NodeContext | null> => {
			const [detailsResult, nodesResult] = await Promise.all([getDetails(), getNodesResult()]);
			const details = isAPIResponse(detailsResult) ? null : detailsResult;
			const currentNodes = isAPIResponse(nodesResult) ? [] : nodesResult;

			if (details) updateCache('cluster-details', details);
			if (!isAPIResponse(nodesResult)) updateCache('cluster-nodes', currentNodes);

			return { details, nodes: currentNodes };
		},
		{ initialValue: null as NodeContext | null }
	);

	let selectedNodeId = $state('');
	let initialNodeSelectionDone = $state(false);

	watch([() => initialNodeSelectionDone, () => nodeContext.current], ([done, context]) => {
		if (done || !context) return;

		const currentNodeId = (context.details?.nodeId || '').trim();
		const fallbackNodeId = context.nodes[0]?.nodeUUID?.trim() || '';
		const nextNodeId = currentNodeId || fallbackNodeId;
		if (nextNodeId) selectedNodeId = nextNodeId;
		initialNodeSelectionDone = true;
	});

	function handleNodeSelection(value: string) {
		if (value === selectedNodeId) {
			return;
		}

		selectedNodeId = value;
		initialNodeSelectionDone = true;
		activeRows = null;
		progressModal.open = false;
	}

	let selectedNodeHostname = $derived(
		nodeContext.current?.nodes.find((node) => node.nodeUUID === selectedNodeId)?.hostname.trim() ||
			''
	);
	type NodeJails = {
		nodeId: string;
		hostname: string;
		jails: Jail[];
	};
	let jailsResource = resource(
		[() => selectedNodeId, () => selectedNodeHostname],
		async ([nodeId, hostname], _, { signal }): Promise<NodeJails> => {
			if (!nodeId || !hostname) return { nodeId, hostname, jails: [] };

			const result = await getJailsResult({ hostname, signal });
			if (isAPIResponse(result)) return { nodeId, hostname, jails: [] };

			await updateCache('jail-list', result, hostname);
			return { nodeId, hostname, jails: result };
		},
		{ initialValue: { nodeId: '', hostname: '', jails: [] } as NodeJails }
	);
	let jails = $derived(jailsResource.current.jails);
	let jailsLoading = $derived(jailsResource.loading);
	// Tabulator fetches on mount, so wait for the formatter data it will capture.
	let eventsTableReady = $derived(
		initialNodeSelectionDone &&
			!jobsLoading &&
			(!selectedNodeId ||
				(!jailsLoading &&
					jailsResource.current.nodeId === selectedNodeId &&
					jailsResource.current.hostname === selectedNodeHostname))
	);
	let progressEventId = $state(0);
	let progressNodeId = $state('');
	let progressModal = $state({
		open: false,
		error: ''
	});

	let errorModal = $state({
		id: 0,
		open: false,
		error: ''
	});

	function openErrorModal(id: number, error: string) {
		errorModal.id = id;
		errorModal.error = error;
		errorModal.open = true;
	}

	async function copyErrorFromModal() {
		if (!errorModal.error) return;
		try {
			await navigator.clipboard.writeText(errorModal.error);
			toast.success('Error copied to clipboard', { duration: 2000, position: 'bottom-center' });
		} catch (_err) {
			toast.error('Failed to copy error', { duration: 2000, position: 'bottom-center' });
		}
	}

	function parseEndpoint(raw: string): { host: string; dataset: string; snapshot: string } {
		const value = (raw || '').trim();
		if (!value) {
			return { host: '', dataset: '', snapshot: '' };
		}

		let host = '';
		let datasetWithSnapshot = value;
		const colonIndex = value.indexOf(':');
		if (colonIndex > 0) {
			host = value.slice(0, colonIndex);
			datasetWithSnapshot = value.slice(colonIndex + 1);
		}

		let dataset = datasetWithSnapshot;
		let snapshot = '';
		const snapshotIndex = datasetWithSnapshot.lastIndexOf('@');
		if (snapshotIndex > 0) {
			dataset = datasetWithSnapshot.slice(0, snapshotIndex);
			snapshot = datasetWithSnapshot.slice(snapshotIndex + 1);
		}

		return { host, dataset, snapshot };
	}

	function formatSnapshotLabel(snapshot: string): string {
		const raw = (snapshot || '').trim();
		if (!raw) return '';

		const legacy = raw.startsWith('zelta_') ? raw.slice(6) : raw;
		if (/^\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}$/.test(legacy)) {
			return legacy.replace('_', ' ').replace(/\./g, ':');
		}

		const normalized = raw.toLowerCase();
		const token = normalized.startsWith('gen-')
			? normalized.slice(4)
			: normalized.includes('_')
				? normalized.split('_').pop() || ''
				: '';

		if (!token || !/^[0-9a-z]+$/.test(token)) {
			return legacy;
		}

		const parsed = Number.parseInt(token, 36);
		if (!Number.isFinite(parsed) || Number.isNaN(parsed) || parsed <= 0) {
			return legacy;
		}

		const timestamp = new Date(parsed);
		if (Number.isNaN(timestamp.getTime())) {
			return legacy;
		}

		return convertDbTime(timestamp.toISOString());
	}

	function compactDatasetLabel(dataset: string): string {
		const trimmed = (dataset || '').trim();
		if (!trimmed) return '';

		const jailMatch = trimmed.match(/\/jails\/(\d+)(?:$|\/)/);
		if (jailMatch) return `Jail ${jailMatch[1]}`;

		const vmMatch = trimmed.match(/\/virtual-machines\/(\d+)(?:$|\/)/);
		if (vmMatch) return `VM ${vmMatch[1]}`;

		const segments = trimmed.split('/').filter(Boolean);
		if (segments.length <= 2) return trimmed;
		return segments.slice(-2).join('/');
	}

	function stripBackupLineage(dataset: string): string {
		const segments = (dataset || '').trim().split('/').filter(Boolean);
		const lineageIndex = segments.findIndex(
			(segment, index) =>
				/^(?:j|job)-[^/]+$/i.test(segment) && segments[index + 1]?.toLowerCase() === 'active'
		);
		if (lineageIndex >= 0) segments.splice(lineageIndex, 2);
		return segments.join('/');
	}

	function resolveJailName(dataset: string, currentJails: Jail[]): string {
		for (const jail of currentJails) {
			const baseStorage = jail.storages?.find((storage: JailStorage) => storage.isBase);
			if (!baseStorage) continue;

			const jailDataset = `${baseStorage.pool}/sylve/jails/${jail.ctId}`;
			if (jailDataset === dataset) {
				return jail.name || '';
			}
		}
		return '';
	}

	function compactEventEndpoint(
		raw: string,
		currentJails: Jail[],
		includeSnapshot: boolean
	): { icon: string; label: string } {
		const endpoint = parseEndpoint(raw);
		if (!endpoint.dataset) {
			return { icon: 'material-symbols:files', label: '' };
		}

		const jailName = resolveJailName(endpoint.dataset, currentJails);
		let icon = 'material-symbols:files';
		let label = jailName || compactDatasetLabel(endpoint.dataset);

		if (jailName || /\/jails\/\d+(?:$|\/)/.test(endpoint.dataset)) {
			icon = 'hugeicons:prison';
		} else if (/\/virtual-machines\/\d+(?:$|\/)/.test(endpoint.dataset)) {
			icon = 'material-symbols:monitor-outline';
		}

		if (includeSnapshot && endpoint.snapshot) {
			label = `${label} @ ${formatSnapshotLabel(endpoint.snapshot)}`;
		}

		return { icon, label };
	}

	type EventEndpointDisplay = {
		icon: string;
		label: string;
		title: string;
	};

	function appendSnapshotLabel(label: string, snapshot: string): string {
		const formatted = formatSnapshotLabel(snapshot);
		if (!formatted) return label;
		return label ? `${label} @ ${formatted}` : `@ ${formatted}`;
	}

	function backupJobGuestId(job: BackupJob): number {
		const dataset =
			job.mode === 'jail'
				? job.jailRootDataset || job.sourceDataset
				: job.sourceDataset || job.jailRootDataset;
		const pattern =
			job.mode === 'jail'
				? /(?:^|\/)jails\/(\d+)(?:$|[/.])/
				: /(?:^|\/)virtual-machines\/(\d+)(?:$|[/.])/;
		const match = dataset.match(pattern);
		if (!match) return 0;

		const id = Number.parseInt(match[1], 10);
		return Number.isNaN(id) ? 0 : id;
	}

	function backupJobSourceDisplay(
		job: BackupJob,
		raw: string,
		currentJails: Jail[]
	): EventEndpointDisplay {
		const endpoint = parseEndpoint(raw);
		if (job.mode === 'dataset') {
			return {
				icon: 'material-symbols:files',
				label: appendSnapshotLabel(endpoint.dataset || job.sourceDataset, endpoint.snapshot),
				title: raw
			};
		}

		const guestId = backupJobGuestId(job);
		const idPrefix = job.mode === 'jail' ? 'CT' : 'RID';
		const fallback = compactEventEndpoint(raw, currentJails, false);
		const friendlySource = (job.friendlySrc || '').trim();
		let label = friendlySource || (guestId > 0 ? String(guestId) : fallback.label);
		if (guestId > 0) {
			label =
				label && label !== String(guestId)
					? `${label} (${idPrefix} ${guestId})`
					: `${idPrefix} ${guestId}`;
		}

		return {
			icon: job.mode === 'jail' ? 'hugeicons:prison' : 'material-symbols:monitor-outline',
			label: appendSnapshotLabel(label, endpoint.snapshot),
			title: raw
		};
	}

	function backupJobRemoteEndpoint(job: BackupJob): string {
		const target = job.target;
		if (!target) return '';

		const root = (target.backupRoot || '').trim().replace(/\/+$/, '');
		const suffix = (job.destSuffix || '').trim().replace(/^\/+/, '');
		const dataset = [root, suffix].filter(Boolean).join('/');
		if (!dataset) return '';

		const host = (target.sshHost || '').trim();
		return host ? `${host}:${dataset}` : dataset;
	}

	function backupTargetDisplay(
		job: BackupJob,
		rawTarget: string,
		workloadRaw: string,
		currentJails: Jail[]
	): EventEndpointDisplay {
		const endpoint = parseEndpoint(rawTarget);
		const targetName = (job.target?.name || '').trim();

		if (job.mode === 'dataset') {
			const dataset = stripBackupLineage(endpoint.dataset);
			return {
				icon: 'material-symbols:files',
				label: appendSnapshotLabel(
					[targetName, dataset].filter(Boolean).join(' · '),
					endpoint.snapshot
				),
				title: rawTarget
			};
		}

		const workload = backupJobSourceDisplay(job, workloadRaw, currentJails);
		return {
			icon: workload.icon,
			label: appendSnapshotLabel(
				[targetName, workload.label].filter(Boolean).join(' · '),
				endpoint.snapshot
			),
			title: rawTarget
		};
	}

	function rawBackupTargetDisplay(raw: string): EventEndpointDisplay {
		const endpoint = parseEndpoint(raw);
		const dataset = stripBackupLineage(endpoint.dataset);
		const label = endpoint.host && dataset ? `${endpoint.host}:${dataset}` : dataset;
		let icon = 'material-symbols:files';
		if (/\/jails\/\d+(?:$|\/)/.test(dataset)) {
			icon = 'hugeicons:prison';
		} else if (/\/virtual-machines\/\d+(?:$|\/)/.test(dataset)) {
			icon = 'material-symbols:monitor-outline';
		}

		return { icon, label: appendSnapshotLabel(label, endpoint.snapshot), title: raw };
	}

	function resolveEventEndpoints(
		event: BackupEvent,
		currentJobs: Record<number, BackupJob>,
		currentJails: Jail[]
	): { source: EventEndpointDisplay; target: EventEndpointDisplay } {
		const job = event.jobId ? currentJobs[event.jobId] : undefined;
		if (!job) {
			const source =
				event.mode === 'restore'
					? rawBackupTargetDisplay(event.sourceDataset)
					: compactEventEndpoint(event.sourceDataset, currentJails, true);
			const target =
				event.mode === 'restore'
					? compactEventEndpoint(event.targetEndpoint, currentJails, false)
					: rawBackupTargetDisplay(event.targetEndpoint);
			return {
				source: { ...source, title: event.sourceDataset },
				target: { ...target, title: event.targetEndpoint }
			};
		}

		if (event.mode === 'restore') {
			const snapshot = parseEndpoint(event.sourceDataset).snapshot;
			const remoteEndpoint = backupJobRemoteEndpoint(job);
			const restoreSource = remoteEndpoint
				? `${remoteEndpoint}${snapshot ? `@${snapshot}` : ''}`
				: event.sourceDataset;
			return {
				source: backupTargetDisplay(job, restoreSource, event.targetEndpoint, currentJails),
				target: backupJobSourceDisplay(job, event.targetEndpoint, currentJails)
			};
		}

		return {
			source: backupJobSourceDisplay(job, event.sourceDataset, currentJails),
			target: backupTargetDisplay(job, event.targetEndpoint, event.sourceDataset, currentJails)
		};
	}

	function renderEventEndpoint(cell: CellComponent, endpoint: EventEndpointDisplay): string {
		cell.getElement().title = endpoint.title;
		return renderWithIcon(endpoint.icon, endpoint.label);
	}

	function eventStatusMeta(status: string | null | undefined): {
		icon: string;
		label: string;
		className: string;
	} {
		switch ((status || '').toLowerCase()) {
			case 'success':
				return {
					icon: 'mdi:check-circle-outline',
					label: 'Success',
					className: 'text-green-500'
				};
			case 'failed':
				return {
					icon: 'mdi:close-circle-outline',
					label: 'Failed',
					className: 'text-red-500'
				};
			case 'interrupted':
				return {
					icon: 'mdi:alert-circle-outline',
					label: 'Interrupted',
					className: 'text-orange-500'
				};
			case 'queued':
				return {
					icon: 'mdi:clock-outline',
					label: 'Queued',
					className: 'text-blue-500'
				};
			case 'running':
				return {
					icon: 'mdi:progress-clock',
					label: 'Running',
					className: 'text-yellow-500'
				};
			default:
				return {
					icon: 'mdi:help-circle-outline',
					label: status || '-',
					className: 'text-muted-foreground'
				};
		}
	}

	function iconClass(icon: string): string {
		const [set, name] = icon.split(':');
		return set && name ? `icon-[${set}--${name}]` : '';
	}

	function selectedRowId(): number {
		if (!activeRows || activeRows.length !== 1) return 0;
		const parsed = Number(activeRows[0].id);
		if (!Number.isFinite(parsed) || parsed <= 0) return 0;
		return parsed;
	}

	let query = $state('');
	let activeRows: Row[] | null = $state(null);

	let selectedRunningEventId = $derived.by(() => {
		if (!activeRows || activeRows.length !== 1) return 0;
		const row = activeRows[0];
		if (row.status !== 'running') return 0;
		const parsed = Number(row.id);
		if (!Number.isFinite(parsed) || parsed <= 0) return 0;
		return parsed;
	});

	const progressEvent = resource(
		[() => progressEventId, () => progressNodeId],
		async ([eventId, nodeId]) => {
			if (eventId <= 0) return null;

			try {
				const res = await getBackupEventProgress(eventId, nodeId);
				progressModal.error = '';
				return res;
			} catch (e: unknown) {
				progressModal.error =
					(e as { message?: string })?.message || 'Failed to load event progress';
				return null;
			}
		},
		{ initialValue: null as BackupEventProgress | null }
	);

	let progressNumber = $derived.by(() => {
		const current = progressEvent.current;
		if (!current) return 0;

		const status = (current.event?.status || '').toLowerCase();
		if (status === 'success') {
			return 100;
		}

		const percent = current.progressPercent;
		if (percent !== null && percent !== undefined && Number.isFinite(percent)) {
			return Math.max(0, Math.min(100, percent));
		}

		const moved = current.movedBytes;
		const total = current.totalBytes;
		if (
			moved !== null &&
			moved !== undefined &&
			total !== null &&
			total !== undefined &&
			total > 0
		) {
			return Math.max(0, Math.min(100, (moved / total) * 100));
		}

		return 0;
	});

	let progressHasData = $derived.by(() => {
		const current = progressEvent.current;
		if (!current) return false;

		const status = (current.event?.status || '').toLowerCase();
		if (status === 'success') {
			return true;
		}

		const percent = current.progressPercent;
		if (percent !== null && percent !== undefined && Number.isFinite(percent)) {
			return true;
		}

		const moved = current.movedBytes;
		const total = current.totalBytes;
		return (
			moved !== null && moved !== undefined && total !== null && total !== undefined && total > 0
		);
	});

	let progressPercentLabel = $derived.by(() =>
		progressHasData ? `${Math.round(progressNumber)}%` : '-'
	);

	useInterval(2000, {
		callback: () => {
			if (!storage.visible || !progressModal.open || progressEventId <= 0) return;

			const status = progressEvent.current?.event?.status;
			if (!status || status === 'running') {
				progressEvent.refetch();
			}
		}
	});

	async function openProgressModal() {
		const eventId = selectedRowId();
		if (eventId <= 0) return;

		progressEventId = eventId;
		progressNodeId = selectedNodeId;
		progressModal.open = true;
		progressModal.error = '';
		await progressEvent.refetch();
	}

	let closeTimeout: ReturnType<typeof setTimeout> | null = null;

	watch(
		() => progressModal.open,
		(open) => {
			if (!open) {
				activeRows = null;
				closeTimeout = setTimeout(() => {
					progressEventId = 0;
					progressNodeId = '';
					progressModal.error = '';
					closeTimeout = null;
				}, 200);
			} else if (closeTimeout) {
				clearTimeout(closeTimeout);
				closeTimeout = null;
			}
		}
	);

	watch(
		() => reload,
		(isReloading) => {
			if (isReloading) {
				activeRows = null;
			}
		}
	);

	watch(
		[() => progressModal.open, () => progressEvent.current?.event?.status],
		([open, status]) => {
			if (open && status && status !== 'running') {
				reload = true;
			}
		}
	);

	let eventColumns = $derived.by((): Column[] => {
		const currentJails = jails;
		const currentJobs = jobsById;

		return [
			{ field: 'id', title: 'ID' },
			{
				field: 'status',
				title: 'Status',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					const meta = eventStatusMeta(value);
					return renderWithIcon(meta.icon, meta.label, meta.className);
				}
			},
			{
				field: 'sourceDataset',
				title: 'Source',
				formatter: (cell: CellComponent) => {
					const event = cell.getRow().getData() as BackupEvent;
					if (!event.sourceDataset) return '';
					const endpoints = resolveEventEndpoints(event, currentJobs, currentJails);
					return renderEventEndpoint(cell, endpoints.source);
				}
			},
			{
				field: 'targetEndpoint',
				title: 'Target',
				formatter: (cell: CellComponent) => {
					const event = cell.getRow().getData() as BackupEvent;
					if (!event.targetEndpoint) return '';
					const endpoints = resolveEventEndpoints(event, currentJobs, currentJails);
					return renderEventEndpoint(cell, endpoints.target);
				}
			},
			{
				field: 'mode',
				title: 'Mode',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();

					if (value === 'restore') {
						return renderWithIcon('ic:baseline-restore', 'Restore');
					} else if (value === 'jail' || value === 'vm' || value === 'dataset') {
						return renderWithIcon('material-symbols:backup-outline', `Backup`);
					}

					return value;
				}
			},
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
			},
			{
				field: 'error',
				title: 'Error',
				cellClick: (_event: UIEvent, cell: CellComponent) => {
					const row = cell.getRow().getData();
					const id = row.id;
					const value = String(cell.getValue() || '');
					if (!value) return;
					openErrorModal(id, value);
				},
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					let v = '';
					let icon = '';
					if (value) {
						switch (value) {
							case 'backup_target_diverged':
								v = 'Backup Target Diverged';
								icon = 'game-icons:divergence';
								break;
							default:
								v = value;
								icon = 'mdi:alert-circle-outline';
						}
					} else {
						return '-';
					}

					return renderWithIcon(icon, v, value ? 'text-red-500' : 'text-green-500');
				}
			}
		];
	});

	let tableData = $derived({
		rows: [],
		columns: eventColumns
	});

	let extraParams = $derived.by((): Record<string, string | number> => {
		const params: Record<string, string | number> = {};
		if (filterJobId) {
			params.jobId = Number.parseInt(filterJobId, 10);
		}
		if (selectedNodeId) {
			params.nodeId = selectedNodeId;
		}
		return params;
	});

	let jobOptions = $derived([
		{ value: '', label: 'All Jobs' },
		...jobs.current.map((job) => ({
			value: String(job.id),
			label: job.name
		}))
	]);

	let nodeOptions = $derived.by(() => {
		const currentNodeId = nodeContext.current?.details?.nodeId?.trim() || '';
		return (nodeContext.current?.nodes || []).map((node) => ({
			value: node.nodeUUID,
			label: node.nodeUUID === currentNodeId ? `${node.hostname} (Current)` : node.hostname
		}));
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center justify-between border-b p-2">
		<div class="flex items-center gap-2 flex-1">
			<Search bind:query />

			{#if nodeOptions.length > 0}
				<SimpleSelect
					placeholder="Select node"
					options={nodeOptions}
					value={selectedNodeId}
					onChange={handleNodeSelection}
					disabled={nodeOptions.length === 0}
					classes={{
						trigger: '!h-6 text-sm'
					}}
				/>
			{/if}

			<SimpleSelect
				placeholder="Filter by job"
				options={jobOptions}
				bind:value={filterJobId}
				onChange={() => (reload = true)}
				classes={{
					trigger: '!h-6 text-sm'
				}}
			/>

			{#if selectedRunningEventId > 0}
				<Button onclick={openProgressModal} size="sm" variant="outline" class="h-6 shrink-0">
					<div class="flex items-center">
						<span class="icon-[mdi--chart-line] mr-1 h-4 w-4"></span>
						<span>View Progress</span>
					</div>
				</Button>
			{/if}
		</div>

		<Button
			onclick={() => (reload = true)}
			size="sm"
			variant="outline"
			class="ml-auto h-6 shrink-0"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--refresh] h-4 w-4"></span>
			</div>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		{#if eventsTableReady}
			<TreeTable
				data={tableData}
				name="backup-events-tt"
				ajaxURL="/api/cluster/backups/events/remote"
				bind:query
				bind:parentActiveRow={activeRows}
				bind:reload
				multipleSelect={false}
				{extraParams}
				initialSort={[{ column: 'startedAt', dir: 'desc' }]}
			/>
		{/if}
	</div>
</div>

<Dialog.Root bind:open={progressModal.open}>
	<Dialog.Content class="w-[min(640px,95vw)] p-5" showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2">
				<span class="icon-[mdi--chart-line] h-5 w-5"></span>
				<span>
					Event Progress
					{#if progressEvent.current?.event}
						#{progressEvent.current.event.id}
					{:else if progressEventId > 0}
						#{progressEventId}
					{/if}
				</span>
			</Dialog.Title>
		</Dialog.Header>

		{#if progressModal.error}
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
				{progressModal.error}
			</div>
		{:else if progressEvent.current?.event}
			{@const event = progressEvent.current.event}
			{@const endpoints = resolveEventEndpoints(event, jobsById, jails)}
			{@const source = endpoints.source}
			{@const target = endpoints.target}
			{@const finalizing = progressEvent.current.phase === 'finalizing'}
			{@const status = finalizing
				? { icon: 'mdi:sync', label: 'Finalizing', className: 'text-blue-500' }
				: eventStatusMeta(event.status)}
			<div class="grid gap-4 py-2 text-sm">
				<div class="overflow-hidden rounded-md border bg-background">
					<table class="w-full text-sm">
						<tbody>
							<tr class="border-b">
								<td class="p-2 text-muted-foreground">Status</td>
								<td class="p-2 text-right">
									<span class={`inline-flex items-center gap-1 ${status.className}`}>
										<span class={iconClass(status.icon) + ' h-4 w-4'}></span>
										<span>{status.label}</span>
									</span>
								</td>
							</tr>
							<tr class="border-b">
								<td class="p-2 text-muted-foreground">Mode</td>
								<td class="p-2 text-right">{event.mode || '-'}</td>
							</tr>
							<tr class="border-b">
								<td class="p-2 text-muted-foreground">Source</td>
								<td class="p-2 text-right" title={source.title}>{source.label || '-'}</td>
							</tr>
							<tr class="border-b">
								<td class="p-2 text-muted-foreground">Target</td>
								<td class="p-2 text-right" title={target.title}>{target.label || '-'}</td>
							</tr>
							<tr class="border-b">
								<td class="p-2 text-muted-foreground">Started</td>
								<td class="p-2 text-right">{convertDbTime(event.startedAt)}</td>
							</tr>
							<tr>
								<td class="p-2 text-muted-foreground">Completed</td>
								<td class="p-2 text-right">
									{event.completedAt ? convertDbTime(event.completedAt) : '-'}
								</td>
							</tr>
						</tbody>
					</table>
				</div>

				<div class="rounded-md border bg-muted/20 p-3">
					<div class="mb-2 flex items-center justify-between text-sm">
						<p class="font-medium">Transfer</p>
					</div>

					<div class="mb-3 overflow-hidden rounded-md border bg-background">
						<table class="w-full text-sm">
							<tbody>
								<tr class="border-b">
									<td class="p-2 text-muted-foreground">Transferred</td>
									<td class="p-2 text-right">
										{#if progressEvent.current.movedBytes !== null && progressEvent.current.movedBytes !== undefined}
											{formatBytesBinary(progressEvent.current.movedBytes)}
										{:else}
											-
										{/if}
									</td>
								</tr>
								<tr class="border-b">
									<td class="p-2 text-muted-foreground">Stream estimate</td>
									<td class="p-2 text-right">
										{#if progressEvent.current.totalBytes !== null && progressEvent.current.totalBytes !== undefined}
											{formatBytesBinary(progressEvent.current.totalBytes)}
										{:else}
											-
										{/if}
									</td>
								</tr>
								<tr>
									<td class="p-2 text-muted-foreground">Progress</td>
									<td class="p-2 text-right">
										<code class="rounded bg-muted px-1 py-0.5">{progressPercentLabel}</code>
									</td>
								</tr>
							</tbody>
						</table>
					</div>

					<Progress value={progressNumber} max={100} class="h-2 w-full" />
					{#if finalizing}
						<p class="mt-2 text-xs text-muted-foreground">
							Transfer complete. Verifying and committing the backup.
						</p>
					{/if}
				</div>
			</div>
		{:else}
			<p class="py-4 text-sm text-muted-foreground">Loading progress...</p>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={errorModal.open}>
	<Dialog.Content class="p-5" showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--alert-circle-outline]"
					size="h-5 w-5"
					title={`#${errorModal.id} Event - Error Details`}
					gap="gap-2 mt-1"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="max-h-[60vh] overflow-auto rounded-md border bg-muted/20 p-3">
			<pre class="m-0 whitespace-pre-wrap wrap-break-word text-sm">{errorModal.error || '-'}</pre>
		</div>

		<Dialog.Footer>
			<Button onclick={copyErrorFromModal}>
				<span>Copy</span>
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
