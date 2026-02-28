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
	import type { ReplicationEvent, ReplicationEventProgress, ReplicationPolicy } from '$lib/types/cluster/replication';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { updateCache } from '$lib/utils/http';
	import { humanFormatBytes } from '$lib/utils/string';
	import { convertDbTime } from '$lib/utils/time';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		policies: ReplicationPolicy[];
		events: ReplicationEvent[];
	}

	let { data }: { data: Data } = $props();

	let query = $state('');
	let reload = $state(false);
	let filterPolicyId = $state('');
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
		() => `replication-events-${filterPolicyId || 'all'}`,
		async () => {
			const policyId = Number.parseInt(filterPolicyId, 10);
			const res = await listReplicationEvents(200, Number.isFinite(policyId) ? policyId : undefined);
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

	let policyFilterOptions = $derived.by(() => [
		{ value: '', label: 'All policies' },
		...policies.current.map((policy) => ({ value: String(policy.id), label: policy.name }))
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

	// svelte-ignore state_referenced_locally
	let progressEvent = resource(
		[() => progressModal.open, () => progressEventId],
		async ([open, eventId]) => {
			if (!open || eventId <= 0) return null;

			try {
				const res = await getReplicationEventProgress(eventId);
				progressModal.error = '';
				return res;
			} catch (error: any) {
				progressModal.error = error?.message || 'Failed to load progress';
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
			if (!progressModal.open || progressEventId <= 0) return;
			const status = progressEvent.current?.event?.status || '';
			if (status === '' || status === 'running') {
				progressEvent.refetch();
			}
		}
	});

	function statusMeta(status: string): { icon: string; label: string; className: string } {
		switch ((status || '').toLowerCase()) {
			case 'running':
				return { icon: 'mdi:progress-clock', label: 'Running', className: 'text-yellow-500' };
			case 'success':
				return { icon: 'mdi:check-circle', label: 'Success', className: 'text-green-500' };
			case 'failed':
				return { icon: 'mdi:close-circle', label: 'Failed', className: 'text-red-500' };
			default:
				return { icon: 'mdi:help-circle-outline', label: status || '-', className: 'text-muted-foreground' };
		}
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
		{ field: 'id', title: 'ID' },
		{
			field: 'status',
			title: 'Status',
			formatter: (cell: CellComponent) => {
				const meta = statusMeta(String(cell.getValue() || ''));
				return renderWithIcon(meta.icon, meta.label, meta.className);
			}
		},
		{ field: 'eventType', title: 'Type' },
		{ field: 'policy', title: 'Policy' },
		{
			field: 'workload',
			title: 'Workload',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData();
				const icon = row.guestType === 'jail' ? 'hugeicons:prison' : 'material-symbols:monitor-outline';
				return renderWithIcon(icon, String(cell.getValue()));
			}
		},
		{ field: 'sourceNodeId', title: 'Source Node' },
		{ field: 'targetNodeId', title: 'Target Node' },
		{ field: 'message', title: 'Message' },
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
			sourceNodeId: event.sourceNodeId || '-',
			targetNodeId: event.targetNodeId || '-',
			message: event.message || '-',
			startedAt: event.startedAt,
			completedAt: event.completedAt || null,
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
		if (progressModal.open && status !== '' && status !== 'running') {
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
				parent: 'w-[320px] space-y-0',
				trigger: 'h-6 w-full px-2',
				label: 'hidden'
			}}
			onChange={(value) => {
				filterPolicyId = value;
				activeRows = null;
			}}
		/>

		<Button size="sm" variant="outline" class="h-6" onclick={openProgress} disabled={selectedEventId <= 0}>
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
	<Dialog.Content class="w-[90%] max-w-xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>Replication Progress</Dialog.Title>
		</Dialog.Header>

		{#if progressModal.error}
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-300">
				{progressModal.error}
			</div>
		{:else if progressEvent.current}
			<div class="space-y-3">
				<div class="space-y-1 text-sm">
					<div class="flex justify-between">
						<span>Status</span>
						<span>{progressEvent.current.event.status}</span>
					</div>
					<div class="flex justify-between">
						<span>Moved</span>
						<span
							>{progressEvent.current.movedBytes !== null &&
							progressEvent.current.movedBytes !== undefined
								? humanFormatBytes(progressEvent.current.movedBytes)
								: '-'}</span
						>
					</div>
					<div class="flex justify-between">
						<span>Total</span>
						<span
							>{progressEvent.current.totalBytes !== null &&
							progressEvent.current.totalBytes !== undefined
								? humanFormatBytes(progressEvent.current.totalBytes)
								: '-'}</span
						>
					</div>
				</div>

				<div class="space-y-1">
					<div class="text-sm">{Math.round(progressPercent)}%</div>
					<Progress value={progressPercent} />
				</div>
			</div>
		{:else}
			<div class="text-sm text-muted-foreground">Loading progress...</div>
		{/if}

		<Dialog.Footer>
			<Button variant="outline" onclick={() => (progressModal.open = false)}>Close</Button>
		</Dialog.Footer>
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
