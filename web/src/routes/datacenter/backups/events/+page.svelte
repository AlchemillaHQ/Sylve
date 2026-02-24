<script lang="ts">
	import { getBackupEventProgress, listBackupJobs } from '$lib/api/cluster/backups';
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { storage } from '$lib';
	import type { BackupEvent, BackupEventProgress, BackupJob } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { humanFormatBytes, sha256 } from '$lib/utils/string';
	import { convertDbTime } from '$lib/utils/time';
	import { updateCache } from '$lib/utils/http';
	import Icon from '@iconify/svelte';
	import { resource } from 'runed';
	import { onDestroy, onMount } from 'svelte';
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
	let progressPollInterval: ReturnType<typeof setInterval> | null = null;
	let progressModal = $state({
		open: false,
		loading: false,
		event: null as BackupEvent | null,
		progress: null as BackupEventProgress | null,
		error: ''
	});

	type TransferProgress = {
		movedBytes: number | null;
		totalBytes: number | null;
		percent: number | null;
		lastMessage: string;
	};

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

	onDestroy(() => {
		stopProgressPolling();
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

	function parseHumanBytes(value: string): number | null {
		const match = value.trim().match(/^([0-9]+(?:\.[0-9]+)?)\s*([KMGTPE]?)(i?B?)$/i);
		if (!match) return null;

		const amount = Number(match[1]);
		if (!Number.isFinite(amount)) return null;

		const unit = match[2].toUpperCase();
		const isBinary = match[3] === 'i' || /IB$/i.test(match[0]);
		const base = isBinary ? 1024 : 1000;

		const rank: Record<string, number> = {
			'': 0,
			K: 1,
			M: 2,
			G: 3,
			T: 4,
			P: 5,
			E: 6
		};

		const power = rank[unit] ?? 0;
		return Math.round(amount * Math.pow(base, power));
	}

	function parseTransferProgress(output: string): TransferProgress {
		const lines = output
			.split(/\r?\n/g)
			.map((line) => line.trim())
			.filter((line) => line.length > 0);

		let totalBytes: number | null = null;
		let movedBytes: number | null = null;

		const jsonTotalRegex = /"replicationSize"\s*:\s*"?([0-9]+)"?/g;
		for (const match of output.matchAll(jsonTotalRegex)) {
			const parsed = Number(match[1]);
			if (Number.isFinite(parsed) && parsed >= 0) {
				totalBytes = parsed;
			}
		}

		for (const line of lines) {
			const syncingMatch = line.match(/syncing:\s*([0-9]+(?:\.[0-9]+)?\s*[KMGTPE]?i?B?)/i);
			if (syncingMatch) {
				const parsed = parseHumanBytes(syncingMatch[1]);
				if (parsed !== null) {
					totalBytes = parsed;
				}
			}

			const bytesTransferredMatch = line.match(/([0-9]+)\s+bytes transferred/i);
			if (bytesTransferredMatch) {
				const parsed = Number(bytesTransferredMatch[1]);
				if (Number.isFinite(parsed) && parsed >= 0) {
					movedBytes = movedBytes === null ? parsed : Math.max(movedBytes, parsed);
				}
			}

			const receivedMatch = line.match(/received\s+([0-9]+(?:\.[0-9]+)?\s*[KMGTPE]?i?B?)\s+stream/i);
			if (receivedMatch) {
				const parsed = parseHumanBytes(receivedMatch[1]);
				if (parsed !== null) {
					movedBytes = movedBytes === null ? parsed : Math.max(movedBytes, parsed);
				}
			}
		}

		let percent: number | null = null;
		if (movedBytes !== null && totalBytes !== null && totalBytes > 0) {
			percent = Math.max(0, Math.min(100, Math.round((movedBytes / totalBytes) * 100)));
		}

		const lastMessage =
			[...lines]
				.reverse()
				.find(
					(line) =>
						!line.startsWith('{') &&
						!line.startsWith('}') &&
						!line.startsWith('"') &&
						!line.startsWith('sentStreams') &&
						!line.startsWith('errorMessages')
				) || '';

		return {
			movedBytes,
			totalBytes,
			percent,
			lastMessage
		};
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

	let progress = $derived.by(() => {
		const parsed = parseTransferProgress(progressModal.event?.output || '');
		const backend = progressModal.progress;

		return {
			movedBytes: backend?.movedBytes ?? parsed.movedBytes,
			totalBytes: backend?.totalBytes ?? parsed.totalBytes,
			percent: backend?.progressPercent ?? parsed.percent,
			lastMessage: parsed.lastMessage
		};
	});

	function stopProgressPolling() {
		if (progressPollInterval !== null) {
			clearInterval(progressPollInterval);
			progressPollInterval = null;
		}
	}

	async function loadProgressEvent(eventId: number, silent = false) {
		if (eventId <= 0) return;

		if (!silent) {
			progressModal.loading = true;
		}

		try {
			const progressData = await getBackupEventProgress(eventId);
			progressModal.progress = progressData;
			progressModal.event = progressData.event;
			progressModal.error = '';

			if (progressModal.event.status !== 'running') {
				stopProgressPolling();
				reload = true;
			}
		} catch (e: any) {
			progressModal.error = e?.message || 'Failed to load event progress';
			progressModal.progress = null;
			stopProgressPolling();
		} finally {
			if (!silent) {
				progressModal.loading = false;
			}
		}
	}

	function startProgressPolling() {
		stopProgressPolling();
		progressPollInterval = setInterval(() => {
			if (!progressModal.open || !progressModal.event?.id) {
				stopProgressPolling();
				return;
			}
			loadProgressEvent(progressModal.event.id, true);
		}, 2000);
	}

	async function openProgressModal() {
		const eventId = selectedRowId();
		if (eventId <= 0) return;

		progressModal.open = true;
		progressModal.error = '';
		progressModal.progress = null;
		await loadProgressEvent(eventId);

		if (progressModal.event?.status === 'running') {
			startProgressPolling();
		}
	}

	$effect(() => {
		if (!progressModal.open) {
			stopProgressPolling();
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
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<div class="w-48">
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
			<Button onclick={openProgressModal} size="sm" variant="outline" class="h-6">
				<div class="flex items-center">
					<Icon icon="mdi:chart-line" class="mr-1 h-4 w-4" />
					<span>View Progress</span>
				</div>
			</Button>
		{/if}

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
	<Dialog.Content class="max-h-[90vh] min-w-[min(900px,95vw)] overflow-y-auto p-5">
		<Dialog.Header>
			<Dialog.Title>
				<div class="flex items-center gap-2">
					<Icon icon="mdi:chart-line" class="h-5 w-5" />
					<span>
						Event Progress
						{#if progressModal.event}
							#{progressModal.event.id}
						{/if}
					</span>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if progressModal.loading}
			<p class="py-4 text-sm text-muted-foreground">Loading progress...</p>
		{:else if progressModal.error}
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
				{progressModal.error}
			</div>
		{:else if progressModal.event}
			{@const event = progressModal.event}
			{@const source = compactEventEndpoint(event.sourceDataset, jails, true)}
			{@const target = compactEventEndpoint(event.targetEndpoint, jails, false)}
			<div class="grid gap-3 py-2 text-sm">
				<div class="grid gap-1">
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
						<p class="mb-1 font-medium">Transfer</p>
						{#if progressModal.progress?.progressDataset}
							<p>
								<span class="font-medium">Tracking:</span>
								<code class="ml-1 rounded bg-background px-1 py-0.5"
									>{progressModal.progress.progressDataset}</code
								>
							</p>
						{/if}
						{#if progress.movedBytes !== null || progress.totalBytes !== null}
						<p>
							<span class="font-medium">Moved:</span>
							{#if progress.movedBytes !== null}
								<code class="ml-1 rounded bg-background px-1 py-0.5"
									>{humanFormatBytes(progress.movedBytes)}</code
								>
							{:else}
								-
							{/if}
						</p>
						<p>
							<span class="font-medium">Total:</span>
							{#if progress.totalBytes !== null}
								<code class="ml-1 rounded bg-background px-1 py-0.5"
									>{humanFormatBytes(progress.totalBytes)}</code
								>
							{:else}
								-
							{/if}
						</p>
							{#if progress.percent !== null}
								<p>
									<span class="font-medium">Progress:</span>
									<code class="ml-1 rounded bg-background px-1 py-0.5"
										>{progress.percent.toFixed(2)}%</code
									>
								</p>
							{/if}
					{:else}
						<p class="text-muted-foreground">Waiting for transfer metrics...</p>
					{/if}
					{#if progress.lastMessage}
						<p class="mt-1">
							<span class="font-medium">Last update:</span>
							<code class="ml-1 rounded bg-background px-1 py-0.5">{progress.lastMessage}</code>
						</p>
					{/if}
				</div>

				<div class="rounded-md border bg-muted/20 p-3">
					<p class="mb-2 font-medium">Live Output</p>
					<pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md border bg-background p-3 text-xs"
						>{event.output?.trim() || 'No output yet...'}</pre
					>
				</div>
			</div>

			<div class="mt-4 flex items-center justify-end gap-2">
				<Button
					variant="outline"
					onclick={() => {
						loadProgressEvent(event.id);
					}}
				>
					Refresh
				</Button>
				<Button variant="outline" onclick={() => (progressModal.open = false)}>Close</Button>
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
