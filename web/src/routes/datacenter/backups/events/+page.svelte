<script lang="ts">
	import { getBackupEventProgress, listBackupJobs } from '$lib/api/cluster/backups';
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import { storage } from '$lib';
	import type { BackupEventProgress, BackupJob } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { humanFormatBytes, sha256 } from '$lib/utils/string';
	import { convertDbTime } from '$lib/utils/time';
	import { updateCache } from '$lib/utils/http';
	import Icon from '@iconify/svelte';
	import { resource, useInterval } from 'runed';
	import { onMount } from 'svelte';
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

	let jails = $state<any[]>([]);
	let jailsLoading = $state(false);
	let progressEventId = $state(0);
	let progressModal = $state({
		open: false,
		error: ''
	});

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
			icon = 'mdi:desktop-tower-monitor';
		}

		if (includeSnapshot && endpoint.snapshot) {
			label = `${label} @ ${formatSnapshotLabel(endpoint.snapshot)}`;
		}

		return { icon, label };
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
		[() => progressEventId, () => progressModal.open],
		async ([eventId, open]) => {
			if (!open || eventId <= 0) return null;

			try {
				const res = await getBackupEventProgress(eventId);
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
			moved !== null &&
			moved !== undefined &&
			total !== null &&
			total !== undefined &&
			total > 0
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
		progressModal.open = true;
		progressModal.error = '';
		await progressEvent.refetch();
	}

	$effect(() => {
		if (!progressModal.open) {
			progressEventId = 0;
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
			{ field: 'id', title: 'ID', visible: false },
			{
				field: 'status',
				title: 'Status',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					switch (value) {
						case 'success':
							return renderWithIcon('mdi:check-circle-outline', 'Success', 'text-green-500');
						case 'failed':
							return renderWithIcon('mdi:close-circle-outline', 'Failed', 'text-red-500');
						case 'interrupted':
							return renderWithIcon('mdi:alert-circle-outline', 'Interrupted', 'text-orange-500');
						case 'running':
							return renderWithIcon('mdi:progress-clock', 'Running', 'text-yellow-500');
						default:
							return value || '-';
					}
				}
			},
			{
				field: 'sourceDataset',
				title: 'Source',
				copyOnClick: true,
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
				copyOnClick: true,
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					if (!value) return '';
					const compact = compactEventEndpoint(value, currentJails, false);
					return renderWithIcon(compact.icon, compact.label);
				}
			},
			{ field: 'mode', title: 'Mode' },
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
				copyOnClick: true,
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
		if (filterJobId) {
			return { jobId: parseInt(filterJobId) };
		}
		return {};
	});

	let jobOptions = $derived([
		{ value: '', label: 'All Jobs' },
		...jobs.current.map((job) => ({
			value: String(job.id),
			label: job.name
		}))
	]);
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center border-b p-2">
		<div class="flex items-center gap-2">
			<Search bind:query />

			<div class="w-48 shrink-0">
				<SimpleSelect
					placeholder="Filter by job"
					options={jobOptions}
					bind:value={filterJobId}
					onChange={() => (reload = true)}
					classes={{
						parent: 'w-full',
						trigger: '!h-6.5 text-sm'
					}}
				/>
			</div>

			{#if selectedRunningEventId > 0}
				<Button onclick={openProgressModal} size="sm" variant="outline" class="h-6 shrink-0">
					<div class="flex items-center">
						<Icon icon="mdi:chart-line" class="mr-1 h-4 w-4" />
						<span>View Progress</span>
					</div>
				</Button>
			{/if}
		</div>

		<Button onclick={() => (reload = true)} size="sm" variant="outline" class="ml-auto h-6">
			<div class="flex items-center">
				<span class="icon-[mdi--refresh] h-4 w-4"></span>
			</div>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		{#if hash && jailsLoading === false}
			{#key `${jails.length}-${filterJobId}`}
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
			<Dialog.Title>
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
			<div class="grid gap-4 py-2 text-sm">
				<div class="grid gap-1.5">
					<p>
						<span class="font-medium">Status:</span>
						<code class="ml-1 rounded bg-background px-1 py-0.5">{event.status || '-'}</code>
					</p>
					<p>
						<span class="font-medium">Mode:</span>
						<code class="ml-1 rounded bg-background px-1 py-0.5">{event.mode || '-'}</code>
					</p>
					<p><span class="font-medium">Source:</span> {source.label || '-'}</p>
					<p><span class="font-medium">Target:</span> {target.label || '-'}</p>
					<p>
						<span class="font-medium">Started:</span>
						{convertDbTime(event.startedAt)}
					</p>
					<p>
						<span class="font-medium">Completed:</span>
						{event.completedAt ? convertDbTime(event.completedAt) : '-'}
					</p>
				</div>

				<div class="rounded-md border bg-muted/20 p-3">
					<div class="mb-2 flex items-center justify-between text-sm">
						<p class="font-medium">Transfer</p>
						<code class="rounded bg-background px-1 py-0.5">{progressPercentLabel}</code>
					</div>

					<Progress value={progressNumber} max={100} class="h-2 w-full" />

					<div class="mt-2 flex items-center justify-between gap-3 text-xs text-muted-foreground">
						<p>
							Moved:
							{#if progressEvent.current.movedBytes !== null &&
								progressEvent.current.movedBytes !== undefined}
								{humanFormatBytes(progressEvent.current.movedBytes)}
							{:else}
								-
							{/if}
						</p>
						<p>
							Total:
							{#if progressEvent.current.totalBytes !== null &&
								progressEvent.current.totalBytes !== undefined}
								{humanFormatBytes(progressEvent.current.totalBytes)}
							{:else}
								-
							{/if}
						</p>
					</div>
				</div>
			</div>

			<div class="mt-4 flex items-center justify-end gap-2">
				<Button variant="outline" onclick={() => progressEvent.refetch()}>Refresh</Button>
				<Button variant="outline" onclick={() => (progressModal.open = false)}>Close</Button>
			</div>
		{:else}
			<p class="py-4 text-sm text-muted-foreground">Loading progress...</p>
		{/if}
	</Dialog.Content>
</Dialog.Root>
