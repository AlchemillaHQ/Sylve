<script lang="ts">
	import {
		deleteBackupTarget,
		listBackupTargetsResult,
		resetBackupTargetHostKey,
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
	import { escapeHTML } from '$lib/utils/string';
	import { dateToAgo } from '$lib/utils/time';
	import Form from '$lib/components/custom/DataCenter/Backups/Targets/Form.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

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

	let clusterEnabled = $derived(clusterDetails.current?.cluster.enabled === true);
	let nodeNames = $derived(
		new Map(clusterNodes.current.map((node) => [node.nodeUUID, node.hostname]))
	);
	let validationNodeIds = $derived.by(() => {
		if (!clusterEnabled) return [''];
		const localId = clusterDetails.current?.nodeId || '';
		return (clusterDetails.current?.nodes || [])
			.filter((node) => node.suffrage === 'voter')
			.map((node) => node.id)
			.sort((a, b) => Number(b === localId) - Number(a === localId));
	});

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
	let resettingHostKey = $state(false);
	let hostKeyResetOpen = $state(false);
	let hostKeyResetTarget = $state<{
		id: number;
		name: string;
		revision: number;
		fingerprint: string;
		nodeId: string;
	} | null>(null);

	type ConnectivityState = 'ok' | 'failed' | 'changed' | 'unchecked';
	type Connectivity = { state: ConnectivityState; label: string; title: string };

	const connectivityIcons: Record<ConnectivityState, [string, string]> = {
		ok: ['mdi:check-network', 'text-green-500'],
		failed: ['mdi:network-off', 'text-red-500'],
		changed: ['mdi:clock-alert-outline', 'text-orange-500'],
		unchecked: ['mdi:help-network-outline', 'text-muted-foreground']
	};

	function nodeConnectivity(status: BackupTargetNodeReadiness): Connectivity {
		const node = clusterEnabled ? nodeNames.get(status.nodeId) || status.nodeId : '';
		const withNode = (text: string) => (node ? `${node} · ${text}` : text);
		if (!status.lastVerifiedAt) {
			return {
				state: 'unchecked',
				label: withNode('Not checked'),
				title: 'No connection check from this node yet'
			};
		}

		const checked = `Last checked ${new Date(status.lastVerifiedAt).toLocaleString()}`;
		if (!status.configurationCurrent) {
			return {
				state: 'changed',
				label: withNode('Settings changed'),
				title: `${checked}, before the target settings changed`
			};
		}
		if (!status.validationSucceeded) {
			return {
				state: 'failed',
				label: withNode('Failed'),
				title: `${checked}: ${status.lastError}`
			};
		}
		return { state: 'ok', label: withNode(dateToAgo(status.lastVerifiedAt)), title: checked };
	}

	function targetConnectivity(target: BackupTarget): Connectivity[] {
		return target.readiness
			.filter((status) => status.currentVoter)
			.map(nodeConnectivity)
			.sort((a, b) => a.label.localeCompare(b.label));
	}

	const targetColumns: Column[] = [
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'enabled',
			title: 'Status',
			formatter: (cell: CellComponent) =>
				cell.getValue()
					? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500')
					: renderWithIcon('mdi:close-circle', 'Disabled', 'text-muted-foreground')
		},
		{
			field: 'connectivity',
			title: 'Connectivity',
			formatter: (cell: CellComponent) => {
				const lines = (cell.getValue() as Connectivity[]).map(({ state, label, title }) => {
					const [icon, color] = connectivityIcons[state];
					return renderWithIcon(icon, escapeHTML(label), color, escapeHTML(title));
				});
				return `<div class="flex flex-col gap-1">${lines.join('')}</div>`;
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
			connectivity: targetConnectivity(target),
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
		if (!mutationsAvailable || !selectedTargetId || validating || resettingHostKey) return;
		const targetId = selectedTargetId;
		const failed: string[] = [];
		validating = true;
		try {
			// One node at a time: the first successful check enrolls the host key.
			for (const nodeId of validationNodeIds) {
				const response = await validateBackupTarget(targetId, nodeId).catch(() => null);
				if (response?.status !== 'success') failed.push(nodeNames.get(nodeId) || nodeId);
			}
		} finally {
			validating = false;
			reload = true;
		}

		if (selectedTargetId !== targetId) return;
		if (failed.length === 0) {
			toast.success('Target connectivity validated', { position: 'bottom-center' });
		} else {
			const message = clusterEnabled
				? `Validation failed on ${failed.join(', ')}`
				: 'Validation failed';
			toast.error(message, { position: 'bottom-center' });
		}
	}

	function openHostKeyReset() {
		if (!mutationsAvailable || !selectedTarget?.hostKey || validating || resettingHostKey) return;
		hostKeyResetTarget = {
			id: selectedTarget.id,
			name: selectedTarget.name,
			revision: selectedTarget.hostKey.revision,
			fingerprint: selectedTarget.hostKey.fingerprint,
			nodeId: validationNodeIds[0]
		};
		hostKeyResetOpen = true;
	}

	async function resetHostKey() {
		if (!mutationsAvailable || !hostKeyResetTarget || resettingHostKey || validating) return;
		const target = hostKeyResetTarget;
		resettingHostKey = true;
		let resetCommitted = false;
		try {
			const response = await resetBackupTargetHostKey(target.id, target.revision);
			if (response.status !== 'success') {
				if (selectedTargetId === target.id) handleAPIError(response);
				return;
			}
			resetCommitted = true;
			hostKeyResetOpen = false;
			await targets.refetch();
			const validation = await validateBackupTarget(target.id, target.nodeId);
			if (selectedTargetId !== target.id) return;
			if (validation.status !== 'success') {
				toast.error(
					'Host key reset. The connection test failed. Check the target and try Validate again.',
					{
						position: 'bottom-center'
					}
				);
				return;
			}
			toast.success('Host key reset. The connection test passed.', { position: 'bottom-center' });
		} catch {
			if (selectedTargetId !== target.id) return;
			toast.error(
				resetCommitted
					? 'Host key reset. The connection test failed. Check the target and try Validate again.'
					: 'Sylve cannot confirm the host-key reset. Refresh the page before you try again.',
				{ position: 'bottom-center' }
			);
		} finally {
			resettingHostKey = false;
			reload = true;
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
				disabled={!mutationsAvailable || validating || resettingHostKey}
			>
				<SpanWithIcon
					icon={validating ? 'icon-[mdi--loading]' : 'icon-[mdi--connection]'}
					size="h-4 w-4 {validating ? 'animate-spin' : ''}"
					gap="gap-2"
					title={validating ? 'Validating' : 'Validate'}
				/>
			</Button>
		{/if}

		{#if type === 'reset-host-key'}
			<Button
				onclick={openHostKeyReset}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={!mutationsAvailable || !selectedTarget?.hostKey || validating || resettingHostKey}
			>
				<SpanWithIcon
					icon="icon-[mdi--key-change]"
					size="h-4 w-4"
					gap="gap-2"
					title="Reset host key"
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

		{@render button('validate')}
		{@render button('reset-host-key')}

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

<AlertDialog
	bind:open={hostKeyResetOpen}
	title={`Reset the SSH host key for ${hostKeyResetTarget?.name || ''}?`}
	customTitle="Sylve will trust the host key from the next successful connection. Continue only if you expect the target's host key to have changed."
	confirmLabel="Reset host key"
	loadingLabel="Resetting host key"
	loading={resettingHostKey}
	keepOpenOnConfirm={true}
	actions={{
		onConfirm: resetHostKey,
		onCancel: () => (hostKeyResetOpen = false)
	}}
>
	{#if hostKeyResetTarget?.fingerprint}
		<p class="break-all text-sm text-muted-foreground">
			Current fingerprint: <span class="font-mono">{hostKeyResetTarget.fingerprint}</span>
		</p>
	{/if}
</AlertDialog>
