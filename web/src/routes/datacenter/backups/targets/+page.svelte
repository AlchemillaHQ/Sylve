<script lang="ts">
	import {
		deleteBackupTarget,
		listBackupTargetsResult,
		validateBackupTarget
	} from '$lib/api/cluster/backups';
	import { getDetails, getNodesResult } from '$lib/api/cluster/cluster';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { BackupTarget, BackupTargetNodeReadiness } from '$lib/types/cluster/backups';
	import type { ClusterDetails, ClusterNode } from '$lib/types/cluster/cluster';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { getQuorumStatus } from '$lib/utils/cluster';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';
	import Form from '$lib/components/custom/DataCenter/Backups/Targets/Form.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';

	interface Data {
		targets: BackupTarget[];
		clusterDetails: ClusterDetails | null;
		clusterNodes: ClusterNode[];
		availability: {
			targets: boolean;
			cluster: boolean;
			nodes: boolean;
		};
	}

	let { data }: { data: Data } = $props();
	// svelte-ignore state_referenced_locally
	let targetsAvailable = $state(data.availability.targets);
	// svelte-ignore state_referenced_locally
	let clusterStateAvailable = $state(data.availability.cluster);
	// svelte-ignore state_referenced_locally
	let clusterNodesAvailable = $state(data.availability.nodes);

	// svelte-ignore state_referenced_locally
	let targets = resource(
		() => 'backup-targets',
		async (_key, _previousKey, { data: previousTargets }): Promise<BackupTarget[]> => {
			const result = await listBackupTargetsResult();
			if (isAPIResponse(result)) {
				targetsAvailable = false;
				return previousTargets ?? [];
			}

			targetsAvailable = true;
			updateCache('backup-targets', result);
			return result;
		},
		{ initialValue: data.targets }
	);

	// svelte-ignore state_referenced_locally
	let clusterDetails = resource(
		() => 'cluster-details-backup-targets',
		async (_key, _previousKey, { data: previousDetails }): Promise<ClusterDetails | null> => {
			const result = await getDetails();
			if (isAPIResponse(result)) {
				clusterStateAvailable = false;
				return previousDetails ?? null;
			}

			clusterStateAvailable = true;
			updateCache('cluster-details', result);
			return result;
		},
		{ initialValue: data.clusterDetails }
	);

	// svelte-ignore state_referenced_locally
	let clusterNodes = resource(
		() => 'cluster-nodes-backup-targets',
		async (_key, _previousKey, { data: previousNodes }): Promise<ClusterNode[]> => {
			const result = await getNodesResult();
			if (isAPIResponse(result)) {
				clusterNodesAvailable = false;
				return previousNodes ?? [];
			}

			clusterNodesAvailable = true;
			updateCache('cluster-nodes', result);
			return result;
		},
		{ initialValue: data.clusterNodes }
	);

	type MutationBlockReason = 'cluster_state' | 'cluster_nodes' | 'quorum' | 'targets' | '';
	let mutationBlockReason = $derived.by((): MutationBlockReason => {
		const details = clusterDetails.current;
		if (!clusterStateAvailable || !details) return 'cluster_state';

		if (details.cluster.enabled) {
			if (!clusterNodesAvailable || details.partial) return 'cluster_nodes';
			if (getQuorumStatus(details, clusterNodes.current) === 'error') return 'quorum';
		}

		if (!targetsAvailable) return 'targets';
		return '';
	});
	let mutationsAvailable = $derived(mutationBlockReason === '');
	let mutationUnavailableMessage = $derived.by(() => {
		switch (mutationBlockReason) {
			case 'quorum':
				return 'Cluster quorum is unavailable. Changes are disabled until enough voters recover.';
			case 'cluster_nodes':
				return 'Could not verify complete cluster membership. Changes are disabled until cluster status recovers.';
			case 'targets':
				return 'Could not refresh backup targets. Changes are disabled until the page refreshes successfully.';
			case 'cluster_state':
				return 'Could not verify cluster state. Changes are disabled until cluster status recovers.';
			default:
				return '';
		}
	});

	let selectedValidationNodeId = $state('');
	let validationNodeInitialized = $state(false);
	let clusterEnabled = $derived(clusterDetails.current?.cluster.enabled === true);
	let validationNodeOptions = $derived.by(() => {
		if (!clusterEnabled) return [];

		const names = new Map(clusterNodes.current.map((node) => [node.nodeUUID, node.hostname]));
		return (clusterDetails.current?.nodes || [])
			.filter((node) => node.suffrage === 'voter')
			.map((node) => ({
				value: node.id,
				label: `${names.get(node.id) || node.id}${node.isLeader ? ' (leader)' : ''}`
			}));
	});

	watch(
		[
			() => validationNodeInitialized,
			() => clusterDetails.current?.nodeId,
			() => validationNodeOptions
		],
		([initialized, currentNodeId, options]) => {
			if (initialized || options.length === 0) return;
			const current = (currentNodeId || '').trim();
			selectedValidationNodeId = options.some((option) => option.value === current)
				? current
				: options[0].value;
			validationNodeInitialized = true;
		}
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				targets.refetch();
				reload = false;
			}
		}
	);

	let query = $state('');
	let activeRows: Row[] = $state([]);
	let selectedTargetId = $derived(
		activeRows !== null && activeRows.length === 1 && typeof activeRows[0].id === 'number'
			? Number(activeRows[0].id)
			: 0
	);

	let selectedTarget = $derived.by(() => {
		if (!selectedTargetId) return null;
		return targets.current.find((t) => t.id === selectedTargetId) || null;
	});

	let targetModal = $state({
		open: false,
		edit: false,
		name: '',
		sshHost: '',
		sshPort: 22,
		sshKey: '',
		backupRoot: '',
		createBackupRoot: false,
		description: '',
		enabled: true
	});

	let deleteModalOpen = $state(false);
	let validating = $state(false);

	function selectedReadiness(target: BackupTarget): BackupTargetNodeReadiness | undefined {
		if (!selectedValidationNodeId) {
			return target.readiness.find((status) => status.currentVoter);
		}
		return target.readiness.find((status) => status.nodeId === selectedValidationNodeId);
	}

	function readinessLabel(status: BackupTargetNodeReadiness | undefined): string | null {
		if (!status) return 'Not validated';
		if (!status.currentVoter) return 'Removed voter';
		if (status.ready) return 'Ready';
		if (!status.configurationCurrent) return 'Configuration changed';
		if (!status.validationSucceeded && status.lastVerifiedAt) return 'Failed';
		if (status.expired) return null;
		return 'Not validated';
	}

	function readinessTitle(status: BackupTargetNodeReadiness | undefined): string {
		if (!status?.lastVerifiedAt) return 'No validation has been recorded for this node';
		const readyUntil = status.readyUntil ? `; ready until ${status.readyUntil}` : '';
		return `Last checked ${status.lastVerifiedAt}${readyUntil}`;
	}

	const targetColumns: Column[] = [
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'enabled',
			title: 'Status',
			formatter: (cell: CellComponent) => {
				const icons = [
					cell.getValue()
						? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500')
						: renderWithIcon('mdi:close-circle', 'Disabled', 'text-muted-foreground')
				];
				const row = cell.getRow().getData();
				const value = typeof row.readiness === 'string' ? row.readiness : '';
				const title = String(row.readinessTitle || '');
				if (value) {
					switch (value) {
						case 'Ready':
							icons.push(renderWithIcon('mdi:check-network', value, 'text-green-500', title));
							break;
						case 'Failed':
							icons.push(renderWithIcon('mdi:network-off', value, 'text-red-500', title));
							break;
						case 'Configuration changed':
							icons.push(
								renderWithIcon('mdi:clock-alert-outline', value, 'text-orange-500', title)
							);
							break;
						default:
							icons.push(
								renderWithIcon('mdi:help-network-outline', value, 'text-muted-foreground', title)
							);
					}
				}

				return `<div class="flex flex-col gap-1">${icons.join(' ')}</div>`;
			}
		},
		{ field: 'name', title: 'Name' },
		{ field: 'sshHost', title: 'SSH Host', visible: false },
		{ field: 'sshPort', title: 'Port', visible: false },
		{ field: 'target', title: 'Target' },
		{ field: 'backupRoot', title: 'Backup Root' },
		{
			field: 'description',
			title: 'Description',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				if (typeof value === 'string' && value.length > 32) {
					const truncated = value.slice(0, 32) + '...';
					return `<span title="${value}">${truncated}</span>`;
				}

				return value || '-';
			}
		}
	];

	let tableData = $derived({
		rows: targets.current.map((target) => ({
			id: target.id,
			name: target.name,
			sshHost: target.sshHost,
			sshPort: target.sshPort || 22,
			target: `${target.sshHost}:${target.sshPort || 22}`,
			backupRoot: target.backupRoot,
			description: target.description || '-',
			enabled: target.enabled,
			readiness: readinessLabel(selectedReadiness(target)),
			readinessTitle: readinessTitle(selectedReadiness(target)),
			createdAt: target.createdAt
		})),
		columns: targetColumns
	});

	async function removeTarget() {
		if (!mutationsAvailable || !selectedTargetId) return;
		const response = await deleteBackupTarget(selectedTargetId);
		if (response.status === 'success') {
			toast.success('Backup target deleted', { position: 'bottom-center' });
			reload = true;
			deleteModalOpen = false;
			activeRows = [];
			return;
		}

		handleAPIError(response);
		toast.error('Failed to delete target', { position: 'bottom-center' });
	}

	async function validateTarget() {
		if (!mutationsAvailable || !selectedTargetId) return;
		validating = true;
		try {
			const response = await validateBackupTarget(selectedTargetId, selectedValidationNodeId);
			if (response.status === 'success') {
				toast.success('Target connectivity validated', { position: 'bottom-center' });
				reload = true;
			} else {
				handleAPIError(response);
				toast.error('Validation failed', { position: 'bottom-center' });
			}
		} catch {
			toast.error('Validation failed', { position: 'bottom-center' });
		} finally {
			validating = false;
		}
	}
</script>

{#snippet button(type: string)}
	{#if activeRows !== null && activeRows.length === 1}
		{#if type === 'validate'}
			<Button
				onclick={validateTarget}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={!mutationsAvailable || validating}
			>
				<SpanWithIcon
					icon={validating ? 'icon-[mdi--loading]' : 'icon-[mdi--connection]'}
					size="h-4 w-4 {validating ? 'animate-spin' : ''}"
					gap="gap-2"
					title={validating ? 'Validating' : 'Validate'}
				/>
			</Button>
		{/if}

		{#if type === 'edit'}
			<Button
				onclick={() => {
					targetModal.edit = true;
					targetModal.open = true;
				}}
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
				onclick={() => {
					targetModal.name = targets.current.find((t) => t.id === selectedTargetId)?.name || '';
					deleteModalOpen = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={!mutationsAvailable ||
					(targets.current.find((target) => target.id === selectedTargetId)?.enabled ?? true)}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	{#if mutationBlockReason}
		<div class="border-b border-destructive/40 bg-destructive/10 px-3 py-2 text-sm">
			<p class="font-medium text-destructive">Backup target changes are unavailable</p>
			<p class="text-muted-foreground">{mutationUnavailableMessage}</p>
		</div>
	{/if}

	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button
			onclick={() => {
				targetModal.edit = false;
				targetModal.open = true;
				activeRows = [];
			}}
			size="sm"
			class="h-6"
			disabled={!mutationsAvailable}
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}

		{#if validationNodeOptions.length > 0}
			<SimpleSelect
				options={validationNodeOptions}
				value={selectedValidationNodeId}
				onChange={(value) => (selectedValidationNodeId = value)}
				placeholder="Validation node"
				title="Node used for target connectivity validation"
				classes={{
					trigger: '!h-6.5 text-sm'
				}}
			/>
		{/if}
		{@render button('validate')}

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
			name="backup-targets-tt"
			bind:query
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
		/>
	</div>
</div>

<Form
	bind:open={targetModal.open}
	bind:edit={targetModal.edit}
	bind:name={targetModal.name}
	bind:sshHost={targetModal.sshHost}
	bind:sshPort={targetModal.sshPort}
	bind:sshKey={targetModal.sshKey}
	bind:backupRoot={targetModal.backupRoot}
	bind:createBackupRoot={targetModal.createBackupRoot}
	bind:description={targetModal.description}
	bind:enabled={targetModal.enabled}
	bind:reload
	{selectedTarget}
	disabled={!mutationsAvailable}
/>

<AlertDialog
	open={deleteModalOpen}
	names={{ parent: 'backup target', element: targetModal.name || '' }}
	customTitle="Delete this backup target? This removes only its saved connection metadata. Backup datasets and snapshots on the remote host are not deleted."
	actions={{
		onConfirm: removeTarget,
		onCancel: () => {
			deleteModalOpen = false;
		}
	}}
/>
