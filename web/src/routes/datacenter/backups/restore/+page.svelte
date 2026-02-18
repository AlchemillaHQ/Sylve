<script lang="ts">
	import {
		getBackupTargetDatasets,
		getBackupTargetSnapshots,
		listBackupTargets,
		pullFromBackupTarget,
		type BackupPullInput
	} from '$lib/api/cluster/backups';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { BackupDataset, BackupSnapshot, BackupTarget } from '$lib/types/cluster/backups';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import humanFormat from 'human-format';
	import Icon from '@iconify/svelte';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';

	interface Data {
		targets: BackupTarget[];
		nodes: ClusterNode[];
	}

	let { data }: { data: Data } = $props();
	// svelte-ignore state_referenced_locally
	let nodes = $state(data.nodes);

	// svelte-ignore state_referenced_locally
	let targets = resource(
		() => 'backup-targets-restore',
		async () => {
			const res = await listBackupTargets();
			updateCache('backup-targets-restore', res);
			return res;
		},
		{ initialValue: data.targets }
	);

	let selectedTargetId = $state('');
	let datasetPrefix = $state('');
	let tableRows = $state<Row[]>([]);
	let loadingDatasets = $state(false);

	let query = $state('');
	let activeRows: Row[] | null = $state(null);

	let restoreModal = $state({
		open: false,
		sourceDataset: '',
		destinationDataset: '',
		snapshot: '',
		runnerNodeId: '',
		force: false,
		withIntermediates: true,
		rollback: false,
		loading: false
	});

	let nodeOptions = $derived([
		{ value: '', label: 'Current node (local)' },
		...nodes.map((node) => ({
			value: node.nodeUUID,
			label: `${node.hostname} (${node.api})`
		}))
	]);

	const columns: Column[] = [
		{
			field: 'displayName',
			title: 'Name',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData();
				const value = cell.getValue() as string;
				if (row.rowType === 'snapshot') {
					return `<span class="icon-[mdi--camera] mr-1 h-4 w-4 inline-block align-middle"></span>${value}`;
				} else if (row.rowType === 'filesystem') {
					return `<span class="icon-[mdi--folder] mr-1 h-4 w-4 inline-block align-middle text-green-500"></span>${value}`;
				} else if (row.rowType === 'volume') {
					return `<span class="icon-[mdi--harddisk] mr-1 h-4 w-4 inline-block align-middle text-purple-500"></span>${value}`;
				}
				return value;
			}
		},
		{
			field: 'rowType',
			title: 'Type',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (value === 'snapshot') {
					return '<span class="text-blue-500">Snapshot</span>';
				} else if (value === 'filesystem') {
					return '<span class="text-green-500">Filesystem</span>';
				} else if (value === 'volume') {
					return '<span class="text-purple-500">Volume</span>';
				}
				return value || '-';
			}
		},
		{
			field: 'size',
			title: 'Size',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (value && Number(value) > 0) {
					return humanFormat(Number(value));
				}
				return '-';
			}
		},
		{
			field: 'guid',
			title: 'GUID'
		},
		{
			field: 'createtxg',
			title: 'TXG',
			formatter: (cell: CellComponent) => cell.getValue() || '-'
		}
	];

	let tableData = $derived({
		rows: tableRows,
		columns: columns
	});

	let targetOptions = $derived([
		{ value: '', label: 'Select a backup target' },
		...targets.current
			.filter((t) => t.enabled)
			.map((target) => ({
				value: String(target.id),
				label: `${target.name} (${target.endpoint})`
			}))
	]);

	// Derive selected row info
	let selectedDataset = $derived.by(() => {
		if (activeRows && activeRows.length === 1) {
			const row = activeRows[0];
			if (row.rowType === 'filesystem' || row.rowType === 'volume') {
				return row;
			}
		}
		return null;
	});

	let selectedSnapshot = $derived.by(() => {
		if (activeRows && activeRows.length === 1) {
			const row = activeRows[0];
			if (row.rowType === 'snapshot') {
				return row;
			}
		}
		return null;
	});

	async function refreshDatasets() {
		if (!selectedTargetId) {
			toast.error('Please select a backup target first', { position: 'bottom-center' });
			return;
		}

		loadingDatasets = true;
		tableRows = [];

		try {
			const datasets = await getBackupTargetDatasets(parseInt(selectedTargetId), datasetPrefix);
			const filteredDatasets = datasets.filter(
				(d) => d.type === 'filesystem' || d.type === 'volume'
			);

			// Build rows with children (snapshots)
			const rows: Row[] = [];

			for (const dataset of filteredDatasets) {
				const row: Row = {
					id: `dataset-${dataset.guid}`,
					name: dataset.name,
					displayName: dataset.name,
					rowType: dataset.type,
					size: dataset.usedBytes,
					guid: dataset.guid,
					createtxg: '',
					children: []
				};

				// Fetch snapshots for this dataset
				try {
					const snaps = await getBackupTargetSnapshots(parseInt(selectedTargetId), dataset.name);
					row.children = snaps.map((snap, i) => {
						// Extract snapshot name after @
						const snapName = snap.name.includes('@') ? snap.name.split('@')[1] : snap.name;
						return {
							id: `snap-${snap.guid}-${i}`,
							name: snap.name,
							displayName: snapName,
							rowType: 'snapshot',
							size: 0,
							guid: snap.guid,
							createtxg: snap.createtxg,
							parentDataset: dataset.name
						};
					});
				} catch {
					// If we can't get snapshots, just leave children empty
					row.children = [];
				}

				rows.push(row);
			}

			tableRows = rows;
		} catch (err) {
			toast.error('Failed to load datasets from target', { position: 'bottom-center' });
			tableRows = [];
		} finally {
			loadingDatasets = false;
		}
	}

	function openRestoreModal(datasetName: string, snapshotName?: string) {
		restoreModal.open = true;
		restoreModal.sourceDataset = datasetName;
		restoreModal.destinationDataset = datasetName;
		restoreModal.snapshot = snapshotName || '';
		restoreModal.runnerNodeId = '';
		restoreModal.force = false;
		restoreModal.withIntermediates = true;
		// Default rollback to true when restoring to a specific snapshot
		restoreModal.rollback = !!snapshotName;
	}

	function resetRestoreModal() {
		restoreModal.open = false;
		restoreModal.sourceDataset = '';
		restoreModal.destinationDataset = '';
		restoreModal.snapshot = '';
		restoreModal.runnerNodeId = '';
		restoreModal.force = false;
		restoreModal.withIntermediates = true;
		restoreModal.rollback = false;
		restoreModal.loading = false;
	}

	async function executeRestore() {
		if (!selectedTargetId) return;

		const payload: BackupPullInput = {
			targetId: parseInt(selectedTargetId),
			runnerNodeId: restoreModal.runnerNodeId || undefined,
			sourceDataset: restoreModal.sourceDataset,
			destinationDataset: restoreModal.destinationDataset,
			snapshot: restoreModal.snapshot,
			force: restoreModal.force,
			withIntermediates: restoreModal.withIntermediates,
			rollback: restoreModal.rollback
		};

		restoreModal.loading = true;
		try {
			const result = await pullFromBackupTarget(payload);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				toast.error('Failed to restore from backup target', { position: 'bottom-center' });
				return;
			}

			if (result.mode === 'rollback') {
				toast.success('Dataset rolled back to snapshot successfully', {
					position: 'bottom-center'
				});
			} else if (result.noop) {
				toast.info('Dataset was already up to date (no changes needed)', {
					position: 'bottom-center'
				});
			} else {
				toast.success(`Restore completed successfully (${result.mode})`, {
					position: 'bottom-center'
				});
			}

			resetRestoreModal();
		} catch (err) {
			toast.error('Restore operation failed', { position: 'bottom-center' });
		} finally {
			restoreModal.loading = false;
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<div class="w-64">
			<SimpleSelect
				placeholder="Select backup target"
				options={targetOptions}
				bind:value={selectedTargetId}
				onChange={() => {
					tableRows = [];
				}}
				classes={{
					parent: 'w-full',
					trigger: '!h-6.5 text-sm'
				}}
			/>
		</div>

		<Button
			onclick={refreshDatasets}
			size="sm"
			variant="outline"
			class="h-6 ml-auto"
			disabled={!selectedTargetId || loadingDatasets}
		>
			<div class="flex items-center">
				<!-- <Icon icon={loadingDatasets ? 'mdi:loading' : 'mdi:refresh'} class="mr-1 h-4 w-4" />
				<span>{loadingDatasets ? 'Loading...' : 'Load Datasets'}</span> -->
				{#if loadingDatasets}
					<span class="icon-[mdi--loading] h-4 w-4 mr-1 animate-spin"></span>
				{:else}
					<span class="icon-[mdi--refresh] h-4 w-4 mr-1"></span>
				{/if}
			</div>
		</Button>

		{#if selectedDataset}
			<Button
				onclick={() => openRestoreModal(selectedDataset!.name as string)}
				size="sm"
				class="ml-auto h-6"
			>
				<div class="flex items-center">
					<Icon icon="mdi:restore" class="mr-1 h-4 w-4" />
					<span>Restore Dataset</span>
				</div>
			</Button>
		{:else if selectedSnapshot}
			<Button
				onclick={() =>
					openRestoreModal(
						selectedSnapshot!.parentDataset as string,
						selectedSnapshot!.name as string
					)}
				size="sm"
				class="ml-auto h-6"
			>
				<div class="flex items-center">
					<Icon icon="mdi:restore" class="mr-1 h-4 w-4" />
					<span>Restore to Snapshot</span>
				</div>
			</Button>
		{/if}
	</div>

	<TreeTable
		data={tableData}
		name="restore-datasets-tt"
		bind:query
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		customPlaceholder="Select a target and click 'Load Datasets' to browse"
	/>
</div>

<Dialog.Root bind:open={restoreModal.open}>
	<Dialog.Content class="w-[92%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>Restore from Backup</Dialog.Title>
			<Dialog.Description>
				Pull data from the backup target to restore a dataset locally.
			</Dialog.Description>
		</Dialog.Header>

		<div class="grid gap-4 py-4">
			<CustomValueInput
				label="Source Dataset (on backup target)"
				placeholder="zroot/data"
				bind:value={restoreModal.sourceDataset}
				classes="space-y-1"
				disabled
			/>

			<CustomValueInput
				label="Destination Dataset (local)"
				placeholder="zroot/restored-data"
				bind:value={restoreModal.destinationDataset}
				classes="space-y-1"
			/>

			<SimpleSelect
				label="Restore to Node"
				placeholder="Current node (local)"
				options={nodeOptions}
				bind:value={restoreModal.runnerNodeId}
				onChange={() => {}}
			/>

			{#if restoreModal.snapshot}
				<CustomValueInput
					label="Restore to Snapshot"
					placeholder="Optional - restore up to specific snapshot"
					bind:value={restoreModal.snapshot}
					classes="space-y-1"
				/>
			{:else}
				<CustomValueInput
					label="Snapshot (optional)"
					placeholder="Leave empty for latest"
					bind:value={restoreModal.snapshot}
					classes="space-y-1"
				/>
			{/if}

			<div class="grid grid-cols-2 gap-4">
				<CustomCheckbox
					label="Force receive (overwrite existing)"
					bind:checked={restoreModal.force}
					classes="flex items-center gap-2"
				/>
				<CustomCheckbox
					label="Include intermediate snapshots"
					bind:checked={restoreModal.withIntermediates}
					classes="flex items-center gap-2"
				/>
			</div>

			{#if restoreModal.snapshot}
				<CustomCheckbox
					label="Rollback dataset to this snapshot (destroys newer snapshots)"
					bind:checked={restoreModal.rollback}
					classes="flex items-center gap-2"
				/>
			{/if}

			<div class="rounded-md bg-muted p-3 text-sm">
				<p class="font-medium">What will happen:</p>
				<ul class="mt-2 list-inside list-disc space-y-1 text-muted-foreground">
					{#if restoreModal.runnerNodeId}
						{@const selectedNode = nodes.find((n) => n.nodeUUID === restoreModal.runnerNodeId)}
						<li>
							Data will be restored to node: <span class="text-blue-500"
								>{selectedNode?.hostname || restoreModal.runnerNodeId}</span
							>
						</li>
					{:else}
						<li>Data will be pulled from the backup target to this node</li>
					{/if}
					<li>The destination dataset will be created if it doesn't exist</li>
					{#if restoreModal.force}
						<li class="text-yellow-500">Force mode: existing data may be overwritten</li>
					{/if}
					{#if restoreModal.snapshot}
						<li>Will restore up to snapshot: {restoreModal.snapshot}</li>
						{#if restoreModal.rollback}
							<li class="text-orange-500">
								Will rollback dataset to this snapshot - <strong
									>snapshots newer than this will be destroyed</strong
								>
							</li>
						{/if}
					{:else}
						<li>Will restore all available snapshots (latest state)</li>
					{/if}
				</ul>
			</div>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={resetRestoreModal}>Cancel</Button>
			<Button
				onclick={executeRestore}
				disabled={restoreModal.loading || !restoreModal.destinationDataset}
			>
				{restoreModal.loading ? 'Restoring...' : 'Start Restore'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
