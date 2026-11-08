<script lang="ts">
	import { getBackupEventProgress, listBackupJobsResult } from '$lib/api/cluster/backups';
	import { getDetails, getNodesResult } from '$lib/api/cluster/cluster';
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { storage } from '$lib';
	import type {
		BackupEvent,
		BackupEventProgress,
		BackupGuestKind,
		BackupJob,
		BackupTarget
	} from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { convertDbTime } from '$lib/utils/time';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { escapeHTML } from '$lib/utils/string';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';

	let filterJobId = $state('');
	let reload = $state(false);

	let jobs = resource(
		() => 'backup-jobs-for-filter',
		async () => {
			const res = await listBackupJobsResult();
			return isAPIResponse(res) ? [] : res;
		},
		{ initialValue: [] as BackupJob[] }
	);
	let jobsById = $derived.by(() => {
		const currentJobs: Record<number, BackupJob> = {};
		for (const job of jobs.current) currentJobs[job.id] = job;
		return currentJobs;
	});

	// Jobs carry their targets; the targets list itself waits on a cluster-wide
	// barrier, which is too slow to hold the table for.
	let jobTargets = $derived(
		jobs.current.flatMap((job): BackupTarget[] => (job.target ? [job.target] : []))
	);

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
		details.open = false;
	}

	// Events live on the node that ran them, which for a job is its runner.
	function handleJobSelection(value: string) {
		const runner = jobsById[Number(value)]?.runnerNodeId || '';
		if (runner && nodeContext.current?.nodes.some((node) => node.nodeUUID === runner)) {
			handleNodeSelection(runner);
		}
		reload = true;
	}

	// Tabulator fetches on mount, so wait for the data its formatters read.
	let eventsTableReady = $derived(initialNodeSelectionDone && !jobs.loading);

	const kindIcons: Record<BackupGuestKind, string> = {
		jail: 'hugeicons:prison',
		vm: 'material-symbols:monitor-outline-rounded',
		dataset: 'mdi:database-outline'
	};

	function parseEndpoint(raw: string): { host: string; dataset: string; snapshot: string } {
		const value = (raw || '').trim();
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

	// Backup snapshots end in their creation time in base 36 (bk_..._<time>).
	function formatSnapshotLabel(snapshot: string): string {
		const raw = (snapshot || '').trim();
		if (!raw) return '';

		const legacy = raw.startsWith('zelta_') ? raw.slice(6) : raw;
		if (/^\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}$/.test(legacy)) {
			return legacy.replace('_', ' ').replace(/\./g, ':');
		}

		const token = raw.toLowerCase().split('_').pop() || '';
		const parsed = /^[0-9a-z]+$/.test(token) ? Number.parseInt(token, 36) : NaN;
		const timestamp = new Date(parsed);
		if (!raw.includes('_') || Number.isNaN(timestamp.getTime()) || parsed <= 0) {
			return legacy;
		}
		return convertDbTime(timestamp.toISOString());
	}

	// Backups read the local dataset and write to the target; restores the reverse.
	function eventEnds(event: BackupEvent): { local: string; remote: string } {
		return event.mode === 'restore'
			? { local: event.targetEndpoint, remote: event.sourceDataset }
			: { local: event.sourceDataset, remote: event.targetEndpoint };
	}

	function eventJob(event: BackupEvent): BackupJob | undefined {
		return event.jobId ? jobsById[event.jobId] : undefined;
	}

	function eventGuest(event: BackupEvent): { icon: string; label: string } {
		const job = eventJob(event);
		const local = parseEndpoint(eventEnds(event).local).dataset;
		const match = local.match(/(?:^|\/)(jails|virtual-machines)\/(\d+)(?:$|\/)/);
		const kind: BackupGuestKind =
			job?.mode ?? (match ? (match[1] === 'jails' ? 'jail' : 'vm') : 'dataset');
		if (kind !== 'dataset' && match) {
			const name = job?.friendlySrc || (kind === 'jail' ? 'Jail' : 'VM');
			return { icon: kindIcons[kind], label: `${name} · ${match[2]}` };
		}
		return { icon: kindIcons.dataset, label: job?.friendlySrc || job?.sourceDataset || local };
	}

	function eventTargetName(event: BackupEvent): string {
		const job = eventJob(event);
		if (job?.target?.name) return job.target.name;
		const { host, dataset } = parseEndpoint(eventEnds(event).remote);
		const target = jobTargets.find(
			(candidate) =>
				(!host || candidate.sshHost === host) &&
				(dataset === candidate.backupRoot || dataset.startsWith(`${candidate.backupRoot}/`))
		);
		return target?.name || host || '-';
	}

	function eventJobName(event: BackupEvent): string {
		if (!event.jobId) return '-';
		return eventJob(event)?.name || 'Deleted job';
	}

	function eventRestorePoint(event: BackupEvent): string {
		if (event.mode !== 'restore') return '';
		return formatSnapshotLabel(parseEndpoint(event.sourceDataset).snapshot);
	}

	function formatDuration(event: BackupEvent): string {
		if (!event.completedAt) return '-';
		const elapsed = (Date.parse(event.completedAt) - Date.parse(event.startedAt)) / 1000;
		if (!Number.isFinite(elapsed)) return '-';
		const seconds = Math.max(0, Math.round(elapsed));
		if (seconds < 60) return `${seconds} s`;
		const minutes = Math.floor(seconds / 60);
		if (minutes < 60) return `${minutes} m ${seconds % 60} s`;
		return `${Math.floor(minutes / 60)} h ${minutes % 60} m`;
	}

	// Errors start with a code (backup_target_diverged: ...); the code alone reads better.
	function errorSummary(error: string): string {
		const code = error.split(':')[0].trim();
		if (!/^[a-z0-9_]+$/.test(code)) return error;
		const words = code.replace(/_/g, ' ');
		return words.charAt(0).toUpperCase() + words.slice(1);
	}

	function eventStatusMeta(status: string | null | undefined): {
		icon: string;
		label: string;
		className: string;
	} {
		switch ((status || '').toLowerCase()) {
			case 'success':
				return { icon: 'mdi:check-circle-outline', label: 'Success', className: 'text-green-500' };
			case 'failed':
				return { icon: 'mdi:close-circle-outline', label: 'Failed', className: 'text-red-500' };
			case 'interrupted':
				return {
					icon: 'mdi:alert-circle-outline',
					label: 'Interrupted',
					className: 'text-orange-500'
				};
			case 'queued':
				return { icon: 'mdi:clock-outline', label: 'Queued', className: 'text-blue-500' };
			case 'running':
				return { icon: 'mdi:progress-clock', label: 'Running', className: 'text-yellow-500' };
			default:
				return {
					icon: 'mdi:help-circle-outline',
					label: status || '-',
					className: 'text-muted-foreground'
				};
		}
	}

	function actionMeta(mode: string): { icon: string; label: string } {
		return mode === 'restore'
			? { icon: 'ic:baseline-restore', label: 'Restore' }
			: { icon: 'material-symbols:backup-outline', label: 'Backup' };
	}

	function iconClass(icon: string): string {
		const [set, name] = icon.split(':');
		return set && name ? `icon-[${set}--${name}]` : '';
	}

	function rowEvent(cell: CellComponent): BackupEvent {
		return cell.getRow().getData() as BackupEvent;
	}

	const eventColumns: Column[] = [
		{
			field: 'status',
			title: 'Status',
			formatter: (cell: CellComponent) => {
				const meta = eventStatusMeta(cell.getValue());
				return renderWithIcon(meta.icon, meta.label, meta.className);
			}
		},
		{
			field: 'mode',
			title: 'Action',
			formatter: (cell: CellComponent) => {
				const meta = actionMeta(cell.getValue());
				return renderWithIcon(meta.icon, meta.label);
			}
		},
		{
			field: 'sourceDataset',
			title: 'Guest / Dataset',
			headerSort: false,
			formatter: (cell: CellComponent) => {
				const event = rowEvent(cell);
				const guest = eventGuest(event);
				cell.getElement().title = parseEndpoint(eventEnds(event).local).dataset;
				return renderWithIcon(guest.icon, escapeHTML(guest.label));
			}
		},
		{
			field: 'targetEndpoint',
			title: 'Target',
			headerSort: false,
			formatter: (cell: CellComponent) => {
				const event = rowEvent(cell);
				cell.getElement().title = eventEnds(event).remote;
				return escapeHTML(eventTargetName(event));
			}
		},
		{
			field: 'jobId',
			title: 'Job',
			headerSort: false,
			formatter: (cell: CellComponent) => escapeHTML(eventJobName(rowEvent(cell)))
		},
		{
			field: 'startedAt',
			title: 'Started',
			formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
		},
		{
			field: 'completedAt',
			title: 'Duration',
			headerSort: false,
			formatter: (cell: CellComponent) => formatDuration(rowEvent(cell))
		},
		{
			field: 'error',
			title: 'Error',
			formatter: (cell: CellComponent) => {
				const value = String(cell.getValue() || '');
				if (!value) return '';
				cell.getElement().title = value;
				return renderWithIcon(
					'mdi:alert-circle-outline',
					escapeHTML(errorSummary(value)),
					'text-red-500'
				);
			}
		}
	];

	const tableData = { rows: [], columns: eventColumns };

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

	let query = $state('');
	let activeRows: Row[] | null = $state(null);

	let details = $state({ open: false, eventId: 0, nodeId: '', error: '' });

	const eventDetails = resource(
		[() => details.eventId, () => details.nodeId],
		async ([eventId, nodeId]) => {
			if (eventId <= 0) return null;
			try {
				const res = await getBackupEventProgress(eventId, nodeId);
				details.error = '';
				return res;
			} catch (e: unknown) {
				details.error = (e as { message?: string })?.message || 'Failed to load the event';
				return null;
			}
		},
		{ initialValue: null as BackupEventProgress | null }
	);

	watch(
		() => activeRows,
		(rows) => {
			if (!rows || rows.length !== 1) return;
			const eventId = Number(rows[0].id);
			if (!Number.isFinite(eventId) || eventId <= 0) return;
			details.eventId = eventId;
			details.nodeId = selectedNodeId;
			details.error = '';
			details.open = true;
		}
	);

	let closeTimeout: ReturnType<typeof setTimeout> | null = null;

	watch(
		() => details.open,
		(open) => {
			if (open) {
				if (closeTimeout) clearTimeout(closeTimeout);
				closeTimeout = null;
				return;
			}
			activeRows = null;
			closeTimeout = setTimeout(() => {
				details.eventId = 0;
				details.nodeId = '';
				details.error = '';
				closeTimeout = null;
			}, 200);
		}
	);

	function eventActive(status: string | undefined): boolean {
		return status === 'running' || status === 'queued';
	}

	useInterval(2000, {
		callback: () => {
			if (!storage.visible || !details.open || details.eventId <= 0) return;
			if (eventActive(eventDetails.current?.event?.status)) eventDetails.refetch();
		}
	});

	// A run that finishes while it's open shows its outcome in the table too.
	let watchedStatus = '';
	watch(
		() => eventDetails.current?.event?.status,
		(status) => {
			if (details.open && eventActive(watchedStatus) && status && !eventActive(status)) {
				reload = true;
			}
			watchedStatus = status || '';
		}
	);

	watch(
		() => reload,
		(isReloading) => {
			if (isReloading) activeRows = null;
		}
	);

	let progressPercent = $derived.by((): number | null => {
		const current = eventDetails.current;
		if (!current) return null;
		if (current.event?.status === 'success') return 100;
		const percent = current.progressPercent;
		if (percent !== null && percent !== undefined && Number.isFinite(percent)) {
			return Math.max(0, Math.min(100, percent));
		}
		const { movedBytes: moved, totalBytes: total } = current;
		if (
			moved !== null &&
			moved !== undefined &&
			total !== null &&
			total !== undefined &&
			total > 0
		) {
			return Math.max(0, Math.min(100, (moved / total) * 100));
		}
		return null;
	});

	async function copyText(text: string, what: string) {
		try {
			await navigator.clipboard.writeText(text);
			toast.success(`${what} copied to clipboard`, { duration: 2000, position: 'bottom-center' });
		} catch (_err) {
			toast.error(`Failed to copy ${what.toLowerCase()}`, {
				duration: 2000,
				position: 'bottom-center'
			});
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center justify-between border-b p-2">
		<div class="flex flex-1 items-center gap-2">
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
				onChange={handleJobSelection}
				classes={{
					trigger: '!h-6 text-sm'
				}}
			/>
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

<Dialog.Root bind:open={details.open}>
	<Dialog.Content class="w-[min(760px,95vw)] max-w-none! p-5" showCloseButton={true}>
		{#if details.error}
			<Dialog.Header>
				<Dialog.Title>Event #{details.eventId}</Dialog.Title>
			</Dialog.Header>
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
				{details.error}
			</div>
		{:else if eventDetails.current?.event}
			{@const event = eventDetails.current.event}
			{@const action = actionMeta(event.mode)}
			{@const guest = eventGuest(event)}
			{@const restorePoint = eventRestorePoint(event)}
			{@const finalizing = eventDetails.current.phase === 'finalizing'}
			{@const status = finalizing
				? { icon: 'mdi:sync', label: 'Finalizing', className: 'text-blue-500' }
				: eventStatusMeta(event.status)}
			<Dialog.Header>
				<Dialog.Title class="flex items-center gap-2">
					<span class={iconClass(action.icon) + ' h-5 w-5'}></span>
					<span>{action.label} #{event.id}</span>
					<span class={`ml-2 inline-flex items-center gap-1 text-sm ${status.className}`}>
						<span class={iconClass(status.icon) + ' h-4 w-4'}></span>
						<span>{status.label}</span>
					</span>
				</Dialog.Title>
			</Dialog.Header>

			<div class="grid max-h-[70vh] gap-4 overflow-y-auto py-1 text-sm">
				<div class="overflow-hidden rounded-md border">
					<table class="w-full text-sm">
						<tbody class="[&>tr:not(:last-child)]:border-b">
							<tr>
								<td class="w-40 p-2 text-muted-foreground">Guest / Dataset</td>
								<td class="p-2">
									<span class="inline-flex items-center gap-1.5">
										<span class={iconClass(guest.icon) + ' h-4 w-4 shrink-0'}></span>
										<span>{guest.label}</span>
									</span>
								</td>
							</tr>
							<tr>
								<td class="p-2 text-muted-foreground">Target</td>
								<td class="p-2">{eventTargetName(event)}</td>
							</tr>
							<tr>
								<td class="p-2 text-muted-foreground">Job</td>
								<td class="p-2">{eventJobName(event)}</td>
							</tr>
							{#if restorePoint}
								<tr>
									<td class="p-2 text-muted-foreground">Restore point</td>
									<td class="p-2">{restorePoint}</td>
								</tr>
							{/if}
							<tr>
								<td class="p-2 text-muted-foreground">Started</td>
								<td class="p-2">{convertDbTime(event.startedAt)}</td>
							</tr>
							<tr>
								<td class="p-2 text-muted-foreground">Completed</td>
								<td class="p-2">
									{event.completedAt ? convertDbTime(event.completedAt) : '-'}
									{#if event.completedAt}
										<span class="text-muted-foreground">({formatDuration(event)})</span>
									{/if}
								</td>
							</tr>
							<tr>
								<td class="p-2 text-muted-foreground">From</td>
								<td class="break-all p-2 font-mono text-xs">{event.sourceDataset || '-'}</td>
							</tr>
							<tr>
								<td class="p-2 text-muted-foreground">To</td>
								<td class="break-all p-2 font-mono text-xs">{event.targetEndpoint || '-'}</td>
							</tr>
						</tbody>
					</table>
				</div>

				{#if eventActive(event.status) || eventDetails.current.movedBytes}
					<div class="rounded-md border p-3">
						<div class="mb-2 flex items-center justify-between">
							<span class="font-medium">Transfer</span>
							<span class="text-muted-foreground">
								{eventDetails.current.movedBytes
									? formatBytesBinary(eventDetails.current.movedBytes)
									: '-'}
								{#if eventDetails.current.totalBytes}
									of about {formatBytesBinary(eventDetails.current.totalBytes)}
								{/if}
								{#if progressPercent !== null}
									· {Math.round(progressPercent)}%
								{/if}
							</span>
						</div>
						<Progress value={progressPercent ?? 0} max={100} class="h-2 w-full" />
						{#if finalizing}
							<p class="mt-2 text-xs text-muted-foreground">
								Transfer complete. Verifying and committing the backup.
							</p>
						{/if}
					</div>
				{/if}

				{#if event.error}
					<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3">
						<div class="mb-2 flex items-center justify-between">
							<span class="font-medium text-red-500">Error</span>
							<Button
								size="sm"
								variant="outline"
								class="h-6"
								onclick={() => copyText(event.error || '', 'Error')}
							>
								Copy
							</Button>
						</div>
						<pre class="m-0 whitespace-pre-wrap break-all text-xs text-red-500">{event.error}</pre>
					</div>
				{/if}

				<div class="rounded-md border">
					<div class="flex items-center justify-between border-b p-2">
						<span class="font-medium">Log</span>
						<Button
							size="sm"
							variant="outline"
							class="h-6"
							disabled={!event.output}
							onclick={() => copyText(event.output || '', 'Log')}
						>
							Copy
						</Button>
					</div>
					<pre
						class="m-0 max-h-72 overflow-auto whitespace-pre-wrap break-all bg-muted/30 p-2 font-mono text-xs">{event.output ||
							'No output recorded.'}</pre>
				</div>
			</div>
		{:else}
			<Dialog.Header>
				<Dialog.Title>Event #{details.eventId}</Dialog.Title>
			</Dialog.Header>
			<p class="py-4 text-sm text-muted-foreground">Loading...</p>
		{/if}
	</Dialog.Content>
</Dialog.Root>
