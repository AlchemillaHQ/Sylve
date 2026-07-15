<script lang="ts">
	import {
		getReplicationEventProgress,
		listReplicationEvents,
		listReplicationPolicies
	} from '$lib/api/cluster/replication';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Progress } from '$lib/components/ui/progress/index.js';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type {
		ReplicationEvent,
		ReplicationEventProgress,
		ReplicationPolicy
	} from '$lib/types/cluster/replication';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { renderWithIcon } from '$lib/utils/table';
	import { storage } from '$lib';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface Data {
		policies: ReplicationPolicy[];
		events: ReplicationEvent[];
		nodes: ClusterNode[];
	}

	let { data }: { data: Data } = $props();
	let nodes = $state(data.nodes);

	let query = $state('');
	let reload = $state(false);
	let filterPolicyId = $state('');
	let selectedNodeId = $state('');
	let activeRows: Row[] | null = $state(null);

	let errorModal = $state({
		open: false,
		title: '',
		error: ''
	});

	let outputModal = $state({
		open: false,
		title: '',
		output: ''
	});

	// svelte-ignore state_referenced_locally
	let policies = resource(
		() => 'replication-policies-events',
		async () => {
			const res = await listReplicationPolicies();
			updateCache('replication-policies', res);
			return res;
		},
		{ initialValue: data.policies }
	);

	// svelte-ignore state_referenced_locally
	let events = resource(
		() => `replication-events-${filterPolicyId || 'all'}-${selectedNodeId || 'auto'}`,
		async () => {
			const policyId = Number.parseInt(filterPolicyId, 10);
			const numeric = Number.isFinite(policyId) ? policyId : undefined;
			const nodeId = selectedNodeId || (numeric ? policyNodeIDByID[numeric] : undefined);
			const res = await listReplicationEvents(200, numeric, nodeId);
			updateCache('replication-events', res);
			return res;
		},
		{ initialValue: data.events }
	);

	watch(
		() => reload,
		(value) => {
			if (!value) return;
			policies.refetch();
			events.refetch();
			reload = false;
		}
	);

	let policyNameByID = $derived.by(() => {
		const out: Record<number, string> = {};
		for (const policy of policies.current) {
			out[policy.id] = policy.name;
		}
		return out;
	});

	let policyByID = $derived.by(() => {
		const out: Record<number, ReplicationPolicy> = {};
		for (const policy of policies.current) {
			out[policy.id] = policy;
		}
		return out;
	});

	let policyNodeIDByID = $derived.by(() => {
		const out: Record<number, string> = {};
		for (const policy of policies.current) {
			const nodeId = (policy.activeNodeId || policy.sourceNodeId || '').trim();
			if (nodeId) {
				out[policy.id] = nodeId;
			}
		}
		return out;
	});

	let nodeNameByID = $derived.by(() => {
		const out: Record<string, string> = {};
		for (const node of nodes) {
			out[node.nodeUUID] = node.hostname || node.nodeUUID;
		}
		return out;
	});

	function compactNodeLabel(nodeId: string): string {
		const value = String(nodeId || '').trim();
		if (!value) return '-';
		const known = nodeNameByID[value];
		if (known) return known;
		return value.length > 12 ? `${value.slice(0, 8)}...` : value;
	}

	function eventPath(event: ReplicationEvent): string {
		const policy = event.policyId ? policyByID[event.policyId] : undefined;
		const sourceNodeId = String(
			event.sourceNodeId || policy?.activeNodeId || policy?.sourceNodeId || ''
		).trim();
		const directTargetNodeId = String(event.targetNodeId || '').trim();

		let targetNodeIds: string[] = [];
		if (directTargetNodeId) {
			targetNodeIds = [directTargetNodeId];
		} else if (event.eventType === 'replication' && policy) {
			targetNodeIds = policy.targets
				.slice()
				.sort((a, b) => b.weight - a.weight || a.nodeId.localeCompare(b.nodeId))
				.map((target) => String(target.nodeId || '').trim());
		}

		const destinations = Array.from(
			new Set(targetNodeIds.filter((nodeId) => nodeId && nodeId !== sourceNodeId))
		).map(compactNodeLabel);

		const destinationLabel =
			destinations.length > 0
				? destinations.join(', ')
				: event.eventType === 'replication' && !directTargetNodeId
					? 'policy targets'
					: '-';

		return `${compactNodeLabel(sourceNodeId)} → ${destinationLabel}`;
	}

	function eventMessageLabel(value: string): string {
		const message = String(value || '')
			.trim()
			.replace(/[_-]+/g, ' ')
			.replace(/\s+/g, ' ');
		if (!message) return '-';
		return message.charAt(0).toUpperCase() + message.slice(1);
	}

	function targetStatusMeta(target: ReplicationPolicy['targets'][number]): {
		icon: string;
		label: string;
		className: string;
	} {
		if (target.ready) {
			const readyUntil = target.readyUntil ? Date.parse(target.readyUntil) : Number.NaN;
			if (Number.isFinite(readyUntil) && readyUntil <= Date.now()) {
				return { icon: 'mdi:clock-alert-outline', label: 'Stale', className: 'text-amber-500' };
			}
			return { icon: 'mdi:check-circle', label: 'Ready', className: 'text-green-500' };
		}
		const reason = String(target.lastError || '')
			.trim()
			.toLowerCase();
		if (reason === 'replication_generation_commit_in_progress' || reason.endsWith('_in_progress')) {
			return { icon: 'mdi:sync', label: 'Syncing', className: 'text-blue-500' };
		}
		if (
			reason === 'awaiting_post_transition_validation' ||
			(reason.startsWith('awaiting_') && reason.endsWith('_validation'))
		) {
			return { icon: 'mdi:shield-sync-outline', label: 'Validating', className: 'text-blue-500' };
		}
		if (reason.endsWith('_requires_validation')) {
			return { icon: 'mdi:sync-alert', label: 'Needs sync', className: 'text-amber-500' };
		}
		if (reason) {
			return { icon: 'mdi:close-circle', label: 'Failed', className: 'text-red-500' };
		}
		if ((target.completedDatasetCount || 0) > 0) {
			return {
				icon: 'mdi:progress-clock',
				label: 'Incomplete',
				className: 'text-amber-500'
			};
		}
		return {
			icon: 'mdi:clock-outline',
			label: 'Pending',
			className: 'text-muted-foreground'
		};
	}

	let targetRows = $derived.by(() => {
		const parsedPolicyId = Number.parseInt(filterPolicyId, 10);
		const selectedPolicyId = Number.isFinite(parsedPolicyId) ? parsedPolicyId : 0;
		return policies.current
			.filter((policy) => selectedPolicyId === 0 || policy.id === selectedPolicyId)
			.flatMap((policy) => {
				const sourceNodeId = String(policy.activeNodeId || policy.sourceNodeId || '').trim();
				return policy.targets.map((target) => ({ policy, target, sourceNodeId }));
			})
			.filter(({ target, sourceNodeId }) => {
				if (!selectedNodeId) return true;
				return sourceNodeId === selectedNodeId || target.nodeId === selectedNodeId;
			})
			.sort(
				(a, b) =>
					a.policy.name.localeCompare(b.policy.name) ||
					b.target.weight - a.target.weight ||
					a.target.nodeId.localeCompare(b.target.nodeId)
			);
	});

	let policyFilterOptions = $derived.by(() => [
		{ value: '', label: 'All policies' },
		...policies.current.map((policy) => ({ value: String(policy.id), label: policy.name }))
	]);

	let nodeFilterOptions = $derived.by(() => [
		{ value: '', label: 'Auto-detect node' },
		...nodes.map((node) => ({
			value: node.nodeUUID,
			label: node.hostname || node.nodeUUID
		}))
	]);

	let selectedEventId = $derived.by(() => {
		if (!activeRows || activeRows.length !== 1) return 0;
		const parsed = Number(activeRows[0].id);
		if (!Number.isFinite(parsed) || parsed <= 0) return 0;
		return parsed;
	});

	let selectedEvent = $derived.by(() => {
		if (selectedEventId <= 0) return null;
		return events.current.find((event) => event.id === selectedEventId) || null;
	});

	let progressEventId = $state(0);
	let progressModal = $state({
		open: false,
		error: ''
	});

	let progressEvent = resource(
		[() => progressModal.open, () => progressEventId],
		async ([open, eventId]) => {
			if (!open || eventId <= 0) return null;

			try {
				const nodeId = selectedEvent ? (selectedEvent.sourceNodeId || '').trim() : undefined;
				const res = await getReplicationEventProgress(eventId, nodeId || undefined);
				progressModal.error = '';
				return res;
			} catch (error: unknown) {
				const err = error as { message?: string } | null | undefined;
				progressModal.error = err?.message || 'Failed to load progress';
				return null;
			}
		},
		{ initialValue: null as ReplicationEventProgress | null }
	);

	let progressPercent = $derived.by(() => {
		const current = progressEvent.current;
		if (!current) return 0;

		if (current.progressPercent !== null && current.progressPercent !== undefined) {
			return Math.max(0, Math.min(100, current.progressPercent));
		}
		if (
			current.movedBytes !== null &&
			current.movedBytes !== undefined &&
			current.totalBytes !== null &&
			current.totalBytes !== undefined &&
			current.totalBytes > 0
		) {
			return Math.max(0, Math.min(100, (current.movedBytes / current.totalBytes) * 100));
		}
		return 0;
	});

	useInterval(2000, {
		callback: () => {
			if (!storage.visible) return;
			if (!progressModal.open || progressEventId <= 0) return;
			const status = progressEvent.current?.event?.status || '';
			if (status === '' || isInProgressStatus(status)) {
				progressEvent.refetch();
			}
		}
	});

	function isInProgressStatus(status: string): boolean {
		switch ((status || '').toLowerCase()) {
			case 'running':
			case 'demoting':
			case 'catchup':
			case 'promoting':
				return true;
			default:
				return false;
		}
	}

	function statusMeta(status: string): { icon: string; label: string; className: string } {
		switch ((status || '').toLowerCase()) {
			case 'running':
				return { icon: 'mdi:progress-clock', label: 'Running', className: 'text-yellow-500' };
			case 'demoting':
				return { icon: 'mdi:arrow-collapse-right', label: 'Demoting', className: 'text-amber-500' };
			case 'catchup':
				return { icon: 'mdi:sync', label: 'Catching Up', className: 'text-indigo-500' };
			case 'promoting':
				return { icon: 'mdi:arrow-expand-right', label: 'Promoting', className: 'text-sky-500' };
			case 'active':
				return { icon: 'mdi:check-decagram', label: 'Active', className: 'text-green-500' };
			case 'success':
				return { icon: 'mdi:check-circle', label: 'Success', className: 'text-green-500' };
			case 'failed':
				return { icon: 'mdi:close-circle', label: 'Failed', className: 'text-red-500' };
			case 'degraded':
				return { icon: 'mdi:alert-circle-outline', label: 'Degraded', className: 'text-amber-500' };
			default:
				return {
					icon: 'mdi:help-circle-outline',
					label: status || '-',
					className: 'text-muted-foreground'
				};
		}
	}

	function eventTypeLabel(value: string): string {
		if (value === 'failover') return 'Failover';
		if (value === 'replication') return 'Replication';
		if (value === 'failback') return 'Failback';
		if (value === 'demotion') return 'Demotion';
		return value || 'Event';
	}

	function openProgress() {
		if (!selectedEvent || selectedEventId <= 0) return;
		progressEventId = selectedEventId;
		progressModal.open = true;
		progressModal.error = '';
		progressEvent.refetch();
	}

	function openError() {
		if (!selectedEvent || !selectedEvent.error) return;
		errorModal.open = true;
		errorModal.title = `Event #${selectedEvent.id}`;
		errorModal.error = selectedEvent.error;
	}

	function openOutput() {
		if (!selectedEvent || !selectedEvent.output) return;
		outputModal.open = true;
		outputModal.title = `Event #${selectedEvent.id}`;
		outputModal.output = selectedEvent.output;
	}

	async function copyError() {
		if (!errorModal.error) return;
		try {
			await navigator.clipboard.writeText(errorModal.error);
			toast.success('Error copied to clipboard', { position: 'bottom-center' });
		} catch (_error) {
			toast.error('Failed to copy error', { position: 'bottom-center' });
		}
	}

	async function copyOutput() {
		if (!outputModal.output) return;
		try {
			await navigator.clipboard.writeText(outputModal.output);
			toast.success('Output copied to clipboard', { position: 'bottom-center' });
		} catch (_error) {
			toast.error('Failed to copy output', { position: 'bottom-center' });
		}
	}

	let eventColumns = $derived.by((): Column[] => [
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'status',
			title: 'Status',
			width: 130,
			minWidth: 110,
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData();
				const rawStatus = String(cell.getValue() || '');
				if (rawStatus === 'active' && row.eventType === 'failover' && row.finishedAt) {
					const completed = statusMeta('success');
					return renderWithIcon(completed.icon, 'Completed', completed.className);
				}
				const meta = statusMeta(rawStatus);
				return renderWithIcon(meta.icon, meta.label, meta.className);
			}
		},
		{
			field: 'eventType',
			title: 'Type',
			width: 110,
			minWidth: 100,
			formatter: (cell: CellComponent) => eventTypeLabel(String(cell.getValue() || ''))
		},
		{ field: 'policy', title: 'Policy', width: 170, minWidth: 130 },
		{
			field: 'workload',
			title: 'Workload',
			width: 130,
			minWidth: 115,
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData();
				const icon =
					row.guestType === 'jail' ? 'hugeicons:prison' : 'material-symbols:monitor-outline';
				return renderWithIcon(icon, String(cell.getValue()));
			}
		},
		{ field: 'path', title: 'Path', width: 220, minWidth: 170 },
		{ field: 'message', title: 'Message', width: 250, minWidth: 180 },
		{
			field: 'startedAt',
			title: 'Started',
			width: 165,
			minWidth: 145,
			formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
		},
		{
			field: 'finishedAt',
			title: 'Finished',
			width: 165,
			minWidth: 145,
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		}
	]);

	let tableData = $derived.by(() => ({
		rows: events.current.map((event) => ({
			id: event.id,
			status: event.status,
			eventType: event.eventType,
			policy: event.policyId ? (policyNameByID[event.policyId] ?? `Policy ${event.policyId}`) : '-',
			guestType: event.guestType,
			workload: `${event.guestType || 'guest'} ${event.guestId || 0}`,
			path: eventPath(event),
			message: eventMessageLabel(event.message || ''),
			startedAt: event.startedAt,
			finishedAt: event.completedAt || null,
			error: event.error || '',
			output: event.output || ''
		})),
		columns: eventColumns
	}));

	$effect(() => {
		if (!progressModal.open) {
			progressEventId = 0;
			progressModal.error = '';
		}
	});

	$effect(() => {
		const status = progressEvent.current?.event?.status || '';
		if (progressModal.open && status !== '' && !isInProgressStatus(status)) {
			reload = true;
		}
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<SimpleSelect
			value={filterPolicyId}
			options={policyFilterOptions}
			classes={{
				trigger: '!h-6 text-sm'
			}}
			onChange={(value) => {
				filterPolicyId = value;
				activeRows = null;
			}}
		/>

		<SimpleSelect
			value={selectedNodeId}
			options={nodeFilterOptions}
			classes={{
				trigger: '!h-6 text-sm'
			}}
			onChange={(value) => {
				selectedNodeId = value;
				activeRows = null;
			}}
		/>

		<Button
			size="sm"
			variant="outline"
			class="h-6"
			onclick={openProgress}
			disabled={selectedEventId <= 0}
		>
			<div class="flex items-center">
				<span class="icon-[mdi--progress-clock] mr-1 h-4 w-4"></span>
				<span>Progress</span>
			</div>
		</Button>

		<Button
			size="sm"
			variant="outline"
			class="h-6"
			onclick={openOutput}
			disabled={!selectedEvent || !selectedEvent.output}
		>
			<div class="flex items-center">
				<span class="icon-[mdi--file-document-outline] mr-1 h-4 w-4"></span>
				<span>Output</span>
			</div>
		</Button>

		<Button
			size="sm"
			variant="outline"
			class="h-6"
			onclick={openError}
			disabled={!selectedEvent || !selectedEvent.error}
		>
			<div class="flex items-center">
				<span class="icon-[mdi--alert-circle-outline] mr-1 h-4 w-4"></span>
				<span>Error</span>
			</div>
		</Button>

		<Button size="sm" variant="outline" class="ml-auto h-6" onclick={() => (reload = true)}>
			<div class="flex items-center">
				<span class="icon-[mdi--refresh] mr-1 h-4 w-4"></span>
				<span>Refresh</span>
			</div>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<div class="border-b p-2">
			<div class="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
				Target Results
			</div>
			{#if targetRows.length === 0}
				<div class="text-xs text-muted-foreground">No replication targets match this filter.</div>
			{:else}
				<div class="max-h-44 overflow-auto rounded-md border">
					<table class="w-full text-xs">
						<thead class="bg-muted/40 text-muted-foreground">
							<tr>
								<th class="p-2 text-left">Policy</th>
								<th class="p-2 text-left">Path</th>
								<th class="p-2 text-left">Status</th>
								<th class="p-2 text-left">Datasets</th>
								<th class="p-2 text-left">Last Verified</th>
								<th class="p-2 text-left">Error</th>
							</tr>
						</thead>
						<tbody>
							{#each targetRows as row (`${row.policy.id}-${row.target.nodeId}`)}
								{@const status = targetStatusMeta(row.target)}
								<tr class="border-t align-top">
									<td class="p-2">{row.policy.name || `Policy ${row.policy.id}`}</td>
									<td class="p-2">
										{compactNodeLabel(row.sourceNodeId)} → {compactNodeLabel(row.target.nodeId)}
									</td>
									<td class="p-2">
										<span class={`inline-flex items-center gap-1 ${status.className}`}>
											<span class={status.icon + ' h-4 w-4'}></span>
											<span>{status.label}</span>
										</span>
									</td>
									<td class="p-2">
										{row.target.completedDatasetCount || 0}/{row.target.requiredDatasetCount || 0}
									</td>
									<td class="p-2">
										{row.target.lastVerifiedAt ? convertDbTime(row.target.lastVerifiedAt) : '-'}
									</td>
									<td class="max-w-[320px] truncate p-2" title={row.target.lastError || ''}>
										{row.target.lastError || '-'}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>

		<TreeTable
			data={tableData}
			name="replication-events-tt"
			bind:query
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
		/>
	</div>
</div>

<Dialog.Root bind:open={progressModal.open}>
	<Dialog.Content class="w-[90%] max-w-xl overflow-hidden p-6">
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--progress-clock]"
					title="Replication Progress"
					size="w-4 h-4"
					gap="gap-2"
				/>
			</Dialog.Title>
		</Dialog.Header>

		{#if progressModal.error}
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-300">
				{progressModal.error}
			</div>
		{:else if progressEvent.current}
			{@const ev = progressEvent.current.event}
			{@const sm = statusMeta(ev.status === 'active' && ev.completedAt ? 'success' : ev.status)}
			{@const terminated = ev.completedAt !== null && ev.completedAt !== undefined}
			{@const showBytes =
				(progressEvent.current.movedBytes !== null &&
					progressEvent.current.movedBytes !== undefined) ||
				(progressEvent.current.totalBytes !== null &&
					progressEvent.current.totalBytes !== undefined)}
			<div class="space-y-3">
				<div class="space-y-2 text-sm">
					<div class="flex justify-between">
						<span>Status</span>
						<span class="inline-flex items-center gap-1">
							<span class={sm.icon + ' h-4 w-4'}></span>
							<span class={sm.className}>{terminated ? 'Completed' : sm.label}</span>
						</span>
					</div>
					{#if showBytes}
						<div class="flex justify-between">
							<span>Moved</span>
							<span
								>{progressEvent.current.movedBytes !== null &&
								progressEvent.current.movedBytes !== undefined
									? formatBytesBinary(progressEvent.current.movedBytes)
									: '-'}</span
							>
						</div>
						<div class="flex justify-between">
							<span>Total</span>
							<span
								>{progressEvent.current.totalBytes !== null &&
								progressEvent.current.totalBytes !== undefined
									? formatBytesBinary(progressEvent.current.totalBytes)
									: '-'}</span
							>
						</div>
					{/if}
				</div>

				<div class="space-y-2">
					<div class="text-sm">{terminated ? 100 : Math.round(progressPercent)}%</div>
					<Progress value={terminated ? 100 : progressPercent} />
				</div>
			</div>
		{:else}
			<div class="text-sm text-muted-foreground">Loading progress...</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={outputModal.open}>
	<Dialog.Content class="w-[90%] max-w-4xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>Event Output - {outputModal.title}</Dialog.Title>
		</Dialog.Header>

		<div class="max-h-[60vh] overflow-auto rounded-md border bg-muted/20 p-3">
			<pre class="whitespace-pre-wrap break-words text-xs">{outputModal.output || '-'}</pre>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={copyOutput}>Copy</Button>
			<Button variant="outline" onclick={() => (outputModal.open = false)}>Close</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={errorModal.open}>
	<Dialog.Content class="w-[90%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>Event Error - {errorModal.title}</Dialog.Title>
		</Dialog.Header>

		<div class="max-h-[45vh] overflow-auto rounded-md border bg-muted/20 p-3">
			<pre class="whitespace-pre-wrap break-words text-xs">{errorModal.error || '-'}</pre>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={copyError}>Copy</Button>
			<Button variant="outline" onclick={() => (errorModal.open = false)}>Close</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
