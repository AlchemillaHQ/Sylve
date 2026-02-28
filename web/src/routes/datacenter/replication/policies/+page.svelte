<script lang="ts">
	import {
		createReplicationPolicy,
		deleteReplicationPolicy,
		listReplicationPolicies,
		runReplicationPolicy,
		updateReplicationPolicy,
		type ReplicationPolicyInput,
		type ReplicationPolicyTargetInput
	} from '$lib/api/cluster/replication';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { ReplicationPolicy } from '$lib/types/cluster/replication';
	import type { SimpleJail } from '$lib/types/jail/jail';
	import type { SimpleVm } from '$lib/types/vm/vm';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		policies: ReplicationPolicy[];
		nodes: ClusterNode[];
		jails: SimpleJail[];
		vms: SimpleVm[];
	}

	type EditableTarget = {
		nodeId: string;
		weight: string;
	};

	let { data }: { data: Data } = $props();
	let nodes = $state(data.nodes);
	let jails = $state(data.jails);
	let vms = $state(data.vms);
	let reload = $state(false);
	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let deleteModalOpen = $state(false);

	// svelte-ignore state_referenced_locally
	let policies = resource(
		() => 'replication-policies',
		async () => {
			const res = await listReplicationPolicies();
			updateCache('replication-policies', res);
			return res;
		},
		{ initialValue: data.policies }
	);

	watch(
		() => reload,
		(value) => {
			if (!value) return;
			policies.refetch();
			reload = false;
		}
	);

	let selectedPolicyId = $derived.by(() => {
		if (!activeRows || activeRows.length !== 1) return 0;
		const parsed = Number(activeRows[0].id);
		if (!Number.isFinite(parsed) || parsed <= 0) return 0;
		return parsed;
	});

	let policyModal = $state({
		open: false,
		edit: false,
		name: '',
		guestType: 'vm' as 'vm' | 'jail',
		guestId: '',
		sourceMode: 'follow_active' as 'follow_active' | 'pinned_primary',
		sourceNodeId: '',
		failbackMode: 'manual' as 'manual' | 'auto',
		cronExpr: '*/15 * * * *',
		enabled: true,
		targets: [{ nodeId: '', weight: '100' }] as EditableTarget[]
	});

	let selectedPolicyName = $derived.by(() => {
		if (selectedPolicyId === 0) return '';
		return policies.current.find((policy) => policy.id === selectedPolicyId)?.name || '';
	});

	let nodeNameByID = $derived.by(() => {
		const out: Record<string, string> = {};
		for (const node of nodes) {
			out[node.nodeUUID] = node.hostname || node.nodeUUID;
		}
		return out;
	});

	let nodeOptions = $derived.by(() =>
		nodes.map((node) => ({
			value: node.nodeUUID,
			label: `${node.hostname} (${node.nodeUUID.slice(0, 8)})`
		}))
	);

	let sourceNodeOptions = $derived.by(() => [
		{ value: '', label: 'None' },
		...nodeOptions
	]);

	let vmOptions = $derived.by(() =>
		vms.map((vm) => ({ value: String(vm.rid), label: `${vm.name} (RID ${vm.rid})` }))
	);

	let jailOptions = $derived.by(() =>
		jails.map((jail) => ({ value: String(jail.ctId), label: `${jail.name} (CTID ${jail.ctId})` }))
	);

	let guestOptions = $derived.by(() =>
		policyModal.guestType === 'vm' ? [...vmOptions] : [...jailOptions]
	);

	const policyColumns: Column[] = [
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'enabled',
			title: 'Status',
			formatter: (cell: CellComponent) =>
				cell.getValue()
					? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500')
					: renderWithIcon('mdi:close-circle', 'Disabled', 'text-muted-foreground')
		},
		{ field: 'name', title: 'Policy' },
		{
			field: 'workload',
			title: 'Workload',
			formatter: (cell: CellComponent) => {
				const data = cell.getRow().getData();
				const icon = data.guestType === 'jail' ? 'hugeicons:prison' : 'material-symbols:monitor-outline';
				return renderWithIcon(icon, String(cell.getValue()));
			}
		},
		{ field: 'source', title: 'Source Node' },
		{ field: 'sourceMode', title: 'Source Mode' },
		{ field: 'failbackMode', title: 'Failback' },
		{ field: 'targets', title: 'Targets' },
		{ field: 'cronExpr', title: 'Cron' },
		{
			field: 'lastStatus',
			title: 'Last Status',
			formatter: (cell: CellComponent) => {
				const value = String(cell.getValue() || '').toLowerCase();
				if (value === 'success') return renderWithIcon('mdi:check-circle', 'Success', 'text-green-500');
				if (value === 'failed') return renderWithIcon('mdi:close-circle', 'Failed', 'text-red-500');
				if (value === 'running') return renderWithIcon('mdi:progress-clock', 'Running', 'text-yellow-500');
				return '-';
			}
		},
		{
			field: 'lastRunAt',
			title: 'Last Run',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		},
		{
			field: 'nextRunAt',
			title: 'Next Run',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		}
	];

	let tableData = $derived.by(() => ({
		rows: policies.current.map((policy) => {
			const workloadLabel = policy.guestType === 'jail' ? `Jail ${policy.guestId}` : `VM ${policy.guestId}`;
			const sourceNode = policy.activeNodeId || policy.sourceNodeId || '';
			const sourceLabel = sourceNode ? (nodeNameByID[sourceNode] ?? sourceNode) : '-';
			const targetsLabel =
				policy.targets
					?.map((target) => `${nodeNameByID[target.nodeId] || target.nodeId} (${target.weight})`)
					.join(', ') || '-';

			return {
				id: policy.id,
				enabled: policy.enabled,
				name: policy.name,
				guestType: policy.guestType,
				workload: workloadLabel,
				source: sourceLabel,
				sourceMode: policy.sourceMode === 'pinned_primary' ? 'Pinned Primary' : 'Follow Active',
				failbackMode: policy.failbackMode === 'auto' ? 'Auto' : 'Manual',
				targets: targetsLabel,
				cronExpr: policy.cronExpr || '-',
				lastStatus: policy.lastStatus,
				lastRunAt: policy.lastRunAt,
				nextRunAt: policy.nextRunAt
			};
		}),
		columns: policyColumns
	}));

	function resetPolicyModal() {
		policyModal.open = false;
		policyModal.edit = false;
		policyModal.name = '';
		policyModal.guestType = 'vm';
		policyModal.guestId = '';
		policyModal.sourceMode = 'follow_active';
		policyModal.sourceNodeId = '';
		policyModal.failbackMode = 'manual';
		policyModal.cronExpr = '*/15 * * * *';
		policyModal.enabled = true;
		policyModal.targets = [{ nodeId: '', weight: '100' }];
	}

	function openCreatePolicy() {
		resetPolicyModal();
		policyModal.open = true;
	}

	function openEditPolicy() {
		if (selectedPolicyId === 0) return;
		const policy = policies.current.find((entry) => entry.id === selectedPolicyId);
		if (!policy) return;

		policyModal.open = true;
		policyModal.edit = true;
		policyModal.name = policy.name;
		policyModal.guestType = policy.guestType;
		policyModal.guestId = String(policy.guestId);
		policyModal.sourceMode = policy.sourceMode;
		policyModal.sourceNodeId = policy.sourceNodeId || '';
		policyModal.failbackMode = policy.failbackMode;
		policyModal.cronExpr = policy.cronExpr || '';
		policyModal.enabled = policy.enabled;
		policyModal.targets =
			policy.targets.length > 0
				? policy.targets.map((target) => ({
						nodeId: target.nodeId,
						weight: String(target.weight || 100)
					}))
				: [{ nodeId: '', weight: '100' }];
	}

	function addTargetRow() {
		policyModal.targets = [...policyModal.targets, { nodeId: '', weight: '100' }];
	}

	function removeTargetRow(index: number) {
		if (policyModal.targets.length <= 1) return;
		policyModal.targets = policyModal.targets.filter((_, idx) => idx !== index);
	}

	function parseTargetsInput(targets: EditableTarget[]): ReplicationPolicyTargetInput[] | null {
		const parsedTargets: ReplicationPolicyTargetInput[] = [];
		const seen = new Set<string>();

		for (const target of targets) {
			const nodeId = target.nodeId.trim();
			if (!nodeId) {
				continue;
			}
			if (seen.has(nodeId)) {
				toast.error('Target nodes must be unique', { position: 'bottom-center' });
				return null;
			}
			seen.add(nodeId);

			let weight = Number.parseInt(String(target.weight || '100'), 10);
			if (!Number.isFinite(weight) || weight <= 0) {
				weight = 100;
			}

			parsedTargets.push({ nodeId, weight });
		}

		if (parsedTargets.length === 0) {
			toast.error('At least one target node is required', { position: 'bottom-center' });
			return null;
		}

		return parsedTargets;
	}

	function buildPolicyPayload(): ReplicationPolicyInput | null {
		const name = policyModal.name.trim();
		if (!name) {
			toast.error('Policy name is required', { position: 'bottom-center' });
			return null;
		}

		const guestId = Number.parseInt(policyModal.guestId, 10);
		if (!Number.isFinite(guestId) || guestId <= 0) {
			toast.error('Select a valid workload', { position: 'bottom-center' });
			return null;
		}

		const targets = parseTargetsInput(policyModal.targets);
		if (!targets) return null;

		const sourceNodeId = policyModal.sourceNodeId.trim();
		if (policyModal.sourceMode === 'pinned_primary' && !sourceNodeId) {
			toast.error('Source node is required for pinned primary mode', { position: 'bottom-center' });
			return null;
		}

		return {
			name,
			guestType: policyModal.guestType,
			guestId,
			sourceMode: policyModal.sourceMode,
			sourceNodeId,
			failbackMode: policyModal.failbackMode,
			cronExpr: policyModal.cronExpr.trim(),
			enabled: policyModal.enabled,
			targets
		};
	}

	async function savePolicy() {
		const payload = buildPolicyPayload();
		if (!payload) return;

		const result = policyModal.edit
			? await updateReplicationPolicy(selectedPolicyId, payload)
			: await createReplicationPolicy(payload);

		if (result.status === 'success') {
			toast.success(policyModal.edit ? 'Policy updated' : 'Policy created', {
				position: 'bottom-center'
			});
			reload = true;
			resetPolicyModal();
			return;
		}

		handleAPIError(result);
		toast.error(policyModal.edit ? 'Failed to update policy' : 'Failed to create policy', {
			position: 'bottom-center'
		});
	}

	async function removePolicy() {
		if (!selectedPolicyId) return;
		const result = await deleteReplicationPolicy(selectedPolicyId);
		if (result.status === 'success') {
			toast.success('Policy deleted', { position: 'bottom-center' });
			deleteModalOpen = false;
			activeRows = [];
			reload = true;
			return;
		}

		handleAPIError(result);
		toast.error('Failed to delete policy', { position: 'bottom-center' });
	}

	async function runNow() {
		if (!selectedPolicyId) return;
		const result = await runReplicationPolicy(selectedPolicyId);
		if (result.status === 'success') {
			toast.success('Replication run queued', { position: 'bottom-center' });
			reload = true;
			return;
		}

		handleAPIError(result);
		toast.error('Failed to start replication run', { position: 'bottom-center' });
	}
</script>

{#snippet actionButtons(type: string)}
	{#if type === 'run' && selectedPolicyId > 0}
		<Button onclick={runNow} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
				<span>Run Now</span>
			</div>
		</Button>
	{/if}

	{#if type === 'edit' && selectedPolicyId > 0}
		<Button onclick={openEditPolicy} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<span class="icon-[mdi--note-edit] mr-1 h-4 w-4"></span>
				<span>Edit</span>
			</div>
		</Button>
	{/if}

	{#if type === 'delete' && selectedPolicyId > 0}
		<Button onclick={() => (deleteModalOpen = true)} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
				<span>Delete</span>
			</div>
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={openCreatePolicy} size="sm" class="h-6">
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{@render actionButtons('run')}
		{@render actionButtons('edit')}
		{@render actionButtons('delete')}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={tableData}
			name="replication-policies-tt"
			bind:query
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
		/>
	</div>
</div>

<Dialog.Root bind:open={policyModal.open}>
	<Dialog.Content class="w-[90%] max-w-3xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>{policyModal.edit ? 'Edit Replication Policy' : 'New Replication Policy'}</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<CustomValueInput
				label="Policy Name"
				placeholder="critical-vm-ha"
				bind:value={policyModal.name}
				classes="space-y-1"
			/>

			<div class="grid grid-cols-2 gap-3">
				<SimpleSelect
					label="Workload Type"
					value={policyModal.guestType}
					options={[
						{ value: 'vm', label: 'VM' },
						{ value: 'jail', label: 'Jail' }
					]}
					onChange={(value) => {
						policyModal.guestType = (value || 'vm') as 'vm' | 'jail';
						policyModal.guestId = '';
					}}
				/>

				<SimpleSelect
					label="Workload"
					value={policyModal.guestId}
					options={guestOptions}
					placeholder={policyModal.guestType === 'vm' ? 'Select VM' : 'Select Jail'}
					disabled={guestOptions.length === 0}
					onChange={(value) => {
						policyModal.guestId = value;
					}}
				/>
			</div>

			<div class="grid grid-cols-3 gap-3">
				<SimpleSelect
					label="Source Mode"
					value={policyModal.sourceMode}
					options={[
						{ value: 'follow_active', label: 'Follow Active' },
						{ value: 'pinned_primary', label: 'Pinned Primary' }
					]}
					onChange={(value) => {
						policyModal.sourceMode = (value || 'follow_active') as
							| 'follow_active'
							| 'pinned_primary';
					}}
				/>

				<SimpleSelect
					label="Source Node"
					value={policyModal.sourceNodeId}
					options={sourceNodeOptions}
					disabled={policyModal.sourceMode !== 'pinned_primary'}
					onChange={(value) => {
						policyModal.sourceNodeId = value;
					}}
				/>

				<SimpleSelect
					label="Failback"
					value={policyModal.failbackMode}
					options={[
						{ value: 'manual', label: 'Manual' },
						{ value: 'auto', label: 'Auto' }
					]}
					onChange={(value) => {
						policyModal.failbackMode = (value || 'manual') as 'manual' | 'auto';
					}}
				/>
			</div>

			<div class="grid grid-cols-[1fr_auto] items-end gap-3">
				<CustomValueInput
					label="Cron"
					placeholder="*/15 * * * *"
					bind:value={policyModal.cronExpr}
					classes="space-y-1"
				/>
				<CustomCheckbox
					label="Enabled"
					bind:checked={policyModal.enabled}
					classes="mb-2 flex items-center gap-2"
				/>
			</div>

			<div class="rounded-md border p-3">
				<div class="mb-2 flex items-center justify-between">
					<span class="text-sm font-medium">Target Nodes (Weight)</span>
					<Button size="sm" variant="outline" class="h-6" onclick={addTargetRow}>
						<div class="flex items-center">
							<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
							<span>Add Target</span>
						</div>
					</Button>
				</div>

				<div class="space-y-2">
					{#each policyModal.targets as target, idx (idx)}
						<div class="grid grid-cols-[1fr_120px_auto] items-end gap-2">
							<SimpleSelect
								label={idx === 0 ? 'Node' : ''}
								value={target.nodeId}
								options={nodeOptions}
								placeholder="Select node"
								onChange={(value) => {
									policyModal.targets[idx].nodeId = value;
								}}
							/>

							<CustomValueInput
								label={idx === 0 ? 'Weight' : ''}
								placeholder="100"
								type="number"
								bind:value={policyModal.targets[idx].weight}
								classes="space-y-1"
							/>

							<Button
								size="sm"
								variant="outline"
								class="h-8"
								disabled={policyModal.targets.length <= 1}
								onclick={() => removeTargetRow(idx)}
							>
								<span class="icon-[mdi--delete] h-4 w-4"></span>
							</Button>
						</div>
					{/each}
				</div>
			</div>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={resetPolicyModal}>Cancel</Button>
			<Button onclick={savePolicy}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={deleteModalOpen}
	names={{ parent: 'replication policy', element: selectedPolicyName }}
	actions={{
		onConfirm: async () => {
			await removePolicy();
		},
		onCancel: () => {
			deleteModalOpen = false;
		}
	}}
/>
