<script lang="ts">
	import { getBackupEventProgress, listBackupJobs } from '$lib/api/cluster/backups';
	import { getDetails, getNodes } from '$lib/api/cluster/cluster';
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { storage } from '$lib';
	import type { BackupEventProgress, BackupJob } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import { humanFormatBytes, sha256 } from '$lib/utils/string';
	import { convertDbTime } from '$lib/utils/time';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import Icon from '@iconify/svelte';
	import { resource, useInterval } from 'runed';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';
	import { getJails } from '$lib/api/jail/jail';

	let filterJobId = $state('');
	let reload = $state(false);
	let hash = $state('');

	let jobs = resource(
		() => 'backup-jobs-for-filter',
		async () => {
			const res = await listBackupJobs();
			return res;
		},
		{ initialValue: [] as BackupJob[] }
	);

	let clusterDetails = resource(
		() => 'cluster-details-events',
		async () => {
			const res = await getDetails();
			if (isAPIResponse(res)) {
				return null;
			}

			updateCache('cluster-details', res);
			return res;
		},
		{ initialValue: null as ClusterDetails | null }
	);

	let nodes = resource(
		() => 'cluster-nodes-events',
		async () => {
			const res = await getNodes();
			updateCache('cluster-nodes', res);
			return res;
		},
		{ initialValue: [] as ClusterNode[] }
	);

	let selectedNodeId = $state('');
	let initialNodeSelectionDone = $state(false);

	$effect(() => {
		if (initialNodeSelectionDone) return;

		const currentNodeId = clusterDetails.current?.nodeId?.trim() || '';
		const fallbackNodeId = nodes.current[0]?.nodeUUID?.trim() || '';
		const nextNodeId = currentNodeId || fallbackNodeId;
		if (!nextNodeId) return;

		selectedNodeId = nextNodeId;
		initialNodeSelectionDone = true;
		reload = true;
	});

	function handleNodeSelection(value: string) {
		if (value === selectedNodeId) {
			return;
		}

		selectedNodeId = value;
		activeRows = null;
		progressModal.open = false;
		reload = true;
	}

	let jails = $state<any[]>([]);
	let jailsLoading = $state(false);
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

	async function loadJails() {
		if (jails.length > 0 || jailsLoading) return;
		jailsLoading = true;
		try {
			const res = await getJails();
			updateCache('jail-list', res);
			jails = res;
			if (hash) {
				reload = true;
			}
		} finally {
			jailsLoading = false;
		}
	}

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);
		loadJails();
	});

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
		const value = snapshot.startsWith('zelta_') ? snapshot.slice(6) : snapshot;
		if (/^\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}$/.test(value)) {
			return value.replace('_', ' ').replace(/\./g, ':');
		}
		return value;
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

	function resolveJailName(dataset: string, currentJails: any[]): string {
		for (const jail of currentJails) {
			const baseStorage = jail.storages?.find((storage: any) => storage.isBase);
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
		currentJails: any[],
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

	// svelte-ignore state_referenced_locally
	const progressEvent = resource(
		[() => progressEventId, () => progressNodeId, () => progressModal.open],
		async ([eventId, nodeId, open]) => {
			if (!open || eventId <= 0) return null;

			try {
				const res = await getBackupEventProgress(eventId, nodeId);
				progressModal.error = '';
				return res;
			} catch (e: any) {
				progressModal.error = e?.message || 'Failed to load event progress';
				return null;
			}
		},
		{ initialValue: null as BackupEventProgress | null }
	);

	let progressNumber = $derived.by(() => {
		const current = progressEvent.current;
		if (!current) return 0;

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

	$effect(() => {
		if (!progressModal.open) {
			progressEventId = 0;
			progressNodeId = '';
			progressModal.error = '';
		}
	});

	$effect(() => {
		const status = progressEvent.current?.event?.status;
		if (progressModal.open && status && status !== 'running') {
			reload = true;
		}
	});

	let eventColumns = $derived.by((): Column[] => {
		const currentJails = jails;

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
					const value = cell.getValue();
					if (!value) return '';
					const compact = compactEventEndpoint(value, currentJails, true);
					return renderWithIcon(compact.icon, compact.label);
				}
			},
			{
				field: 'targetEndpoint',
				title: 'Target',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					if (!value) return '';
					const compact = compactEventEndpoint(value, currentJails, false);
					return renderWithIcon(compact.icon, compact.label);
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
		const currentNodeId = clusterDetails.current?.nodeId?.trim() || '';
		return nodes.current.map((node) => ({
			value: node.nodeUUID,
			label: node.nodeUUID === currentNodeId ? `${node.hostname} (Current)` : node.hostname
		}));
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center justify-between border-b p-2">
		<div class="flex items-center gap-2 flex-1">
			<Search bind:query />

			<SimpleSelect
				placeholder="Filter by job"
				options={jobOptions}
				bind:value={filterJobId}
				onChange={() => (reload = true)}
				classes={{
					trigger: '!h-6 text-sm' /* Adjusted to h-6 to match your buttons */
				}}
			/>

			<SimpleSelect
				placeholder="Select node"
				options={nodeOptions}
				bind:value={selectedNodeId}
				onChange={handleNodeSelection}
				disabled={nodeOptions.length === 0}
				classes={{
					trigger: '!h-6 text-sm'
				}}
			/>

			{#if selectedRunningEventId > 0}
				<Button onclick={openProgressModal} size="sm" variant="outline" class="h-6 shrink-0">
					<div class="flex items-center">
						<Icon icon="mdi:chart-line" class="mr-1 h-4 w-4" />
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
		{#if hash && jailsLoading === false}
			{#key `${jails.length}-${filterJobId}-${selectedNodeId}`}
				<TreeTable
					data={tableData}
					name="backup-events-tt"
					ajaxURL="/api/cluster/backups/events/remote?hash={hash}"
					bind:query
					bind:parentActiveRow={activeRows}
					bind:reload
					multipleSelect={false}
					{extraParams}
					initialSort={[{ column: 'startedAt', dir: 'desc' }]}
				/>
			{/key}
		{/if}
	</div>
</div>

<Dialog.Root bind:open={progressModal.open}>
	<Dialog.Content class="w-[min(640px,95vw)] p-5">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<div class="flex items-center gap-2">
						<Icon icon="mdi:chart-line" class="h-5 w-5" />
						<span>
							Event Progress
							{#if progressEvent.current?.event}
								#{progressEvent.current.event.id}
							{:else if progressEventId > 0}
								#{progressEventId}
							{/if}
						</span>
					</div>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							progressModal.open = false;
						}}
					>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if progressModal.error}
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
				{progressModal.error}
			</div>
		{:else if progressEvent.current?.event}
			{@const event = progressEvent.current.event}
			{@const source = compactEventEndpoint(event.sourceDataset, jails, true)}
			{@const target = compactEventEndpoint(event.targetEndpoint, jails, false)}
			{@const status = eventStatusMeta(event.status)}
			<div class="grid gap-4 py-2 text-sm">
				<div class="overflow-hidden rounded-md border bg-background">
					<table class="w-full text-sm">
						<tbody>
							<tr class="border-b">
								<td class="p-2 text-muted-foreground">Status</td>
								<td class="p-2 text-right">
									<span class={`inline-flex items-center gap-1 ${status.className}`}>
										<Icon icon={status.icon} class="h-4 w-4" />
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
								<td class="p-2 text-right">{source.label || '-'}</td>
							</tr>
							<tr class="border-b">
								<td class="p-2 text-muted-foreground">Target</td>
								<td class="p-2 text-right">{target.label || '-'}</td>
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
									<td class="p-2 text-muted-foreground">Moved</td>
									<td class="p-2 text-right">
										{#if progressEvent.current.movedBytes !== null && progressEvent.current.movedBytes !== undefined}
											{humanFormatBytes(progressEvent.current.movedBytes)}
										{:else}
											-
										{/if}
									</td>
								</tr>
								<tr class="border-b">
									<td class="p-2 text-muted-foreground">Total</td>
									<td class="p-2 text-right">
										{#if progressEvent.current.totalBytes !== null && progressEvent.current.totalBytes !== undefined}
											{humanFormatBytes(progressEvent.current.totalBytes)}
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
				</div>
			</div>
		{:else}
			<p class="py-4 text-sm text-muted-foreground">Loading progress...</p>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={errorModal.open}>
	<Dialog.Content class="w-[min(760px,95vw)] h-[min(60vh,95vh)] p-5">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<Icon icon="mdi:alert-circle-outline" class="h-5 w-5 text-red-500" />
					<span>#{errorModal.id} Event - Error Details</span>
				</div>
				<div class="flex items-center gap-2">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							errorModal.open = false;
						}}
					>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-3 max-h-[60vh] overflow-auto rounded-md border bg-muted/20 p-3">
			<pre class="whitespace-pre-wrap break-words text-sm">{errorModal.error || '-'}</pre>
		</div>

		<Dialog.Footer>
			<Button onclick={copyErrorFromModal}>
				<span>Copy</span>
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
