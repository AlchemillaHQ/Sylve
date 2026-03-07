<script lang="ts">
	import {
		createReplicationPolicy,
		deleteReplicationPolicy,
		failoverReplicationPolicy,
		listReplicationPolicies,
		runReplicationPolicy,
		updateReplicationPolicy,
		type ReplicationPolicyInput,
		type ReplicationPolicyTargetInput
	} from '$lib/api/cluster/replication';
	import { getJails } from '$lib/api/jail/jail';
	import { getVMs } from '$lib/api/vm/vm';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import type { ReplicationPolicy } from '$lib/types/cluster/replication';
	import type { SimpleJail } from '$lib/types/jail/jail';
	import type { SimpleVm } from '$lib/types/vm/vm';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import {
		FORCE_RECOVERY_CONFIRM_TEXT,
		failoverModeOptions,
		normalizeFailoverMode,
		userFailoverErrorMessage,
		validateFailoverAction
	} from '$lib/utils/replication-failover';
	import { convertDbTime, cronToHuman } from '$lib/utils/time';
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

	type SourceModeCard = {
		value: 'follow_active' | 'pinned_primary';
		title: string;
		description: string;
	};

	type FailoverCard = {
		value: 'manual' | 'auto_safe' | 'auto_force';
		title: string;
		description: string;
		impact: string;
		dangerous?: boolean;
	};

	const sourceModeCards: SourceModeCard[] = [
		{
			value: 'follow_active',
			title: 'Stay with whichever node is currently active',
			description: 'After any move, this new active node becomes the new source.'
		},
		{
			value: 'pinned_primary',
			title: 'Keep one preferred primary node',
			description: 'Use one fixed primary node as the source until you change it.'
		}
	];

	const failoverCards: FailoverCard[] = [
		{
			value: 'manual',
			title: 'Manual only',
			description: 'No automatic move when the active node goes down.',
			impact: 'An admin chooses when and where to move the workload.'
		},
		{
			value: 'auto_safe',
			title: 'Automatic safe move',
			description: 'Automatically move after a safe demote and sync.',
			impact: 'Protects data first, then promotes a target.'
		},
		{
			value: 'auto_force',
			title: 'Automatic force recovery',
			description: 'Automatically recover even when clean handoff is impossible.',
			impact: 'Fastest recovery, but newest writes may be lost.',
			dangerous: true
		}
	];

	let { data }: { data: Data } = $props();
	let nodes = $state(data.nodes);
	let jails = $state(data.jails);
	let vms = $state(data.vms);
	let reload = $state(false);
	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let deleteModalOpen = $state(false);
	let failoverModalOpen = $state(false);
	let jailsLoading = $state(false);
	let vmsLoading = $state(false);
	let jailsLoadedForNode = $state('');
	let vmsLoadedForNode = $state('');

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
		workloadNodeId: '',
		guestId: '',
		sourceMode: 'follow_active' as 'follow_active' | 'pinned_primary',
		sourceNodeId: '',
		failbackMode: 'manual' as 'manual' | 'auto',
		failoverMode: 'manual' as 'manual' | 'auto_safe' | 'auto_force',
		confirmAutoForce: false,
		cronExpr: '*/15 * * * *',
		enabled: true,
		targets: [{ nodeId: '', weight: '100' }] as EditableTarget[]
	});

	let failoverModal = $state({
		mode: 'safe' as 'safe' | 'force',
		targetNodeId: '',
		movePinnedSource: true,
		confirmDataLoss: false,
		forceConfirmText: ''
	});

	let selectedPolicyName = $derived.by(() => {
		if (selectedPolicyId === 0) return '';
		return policies.current.find((policy) => policy.id === selectedPolicyId)?.name || '';
	});

	let selectedPolicy = $derived.by(() => {
		if (selectedPolicyId === 0) return null;
		return policies.current.find((policy) => policy.id === selectedPolicyId) || null;
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

	function scheduleLabel(cronExpr: string): string {
		const value = String(cronExpr || '').trim();
		if (!value) return '-';
		try {
			return cronToHuman(value);
		} catch {
			return value;
		}
	}

	function sourceModeSummary(sourceMode: string): string {
		return sourceMode === 'pinned_primary' ? 'Preferred primary node' : 'Follow current active node';
	}

	function failoverModeSummary(failoverMode: string): string {
		if (failoverMode === 'auto_force') return 'Auto force recovery';
		if (failoverMode === 'auto_safe') return 'Auto safe move';
		return 'Manual moves only';
	}

	function failbackModeSummary(failbackMode: string): string {
		return failbackMode === 'auto' ? 'Auto move back' : 'Manual move back';
	}

	function policyModeSummary(policy: ReplicationPolicy): string {
		return `${failoverModeSummary(policy.failoverMode)} | ${sourceModeSummary(policy.sourceMode)} | ${failbackModeSummary(policy.failbackMode)}`;
	}

	let nodeOptions = $derived.by(() =>
		nodes.map((node) => ({
			value: node.nodeUUID,
			label: `${node.hostname} (${node.nodeUUID.slice(0, 8)})`
		}))
	);

	let nodeStatusByID = $derived.by(() => {
		const out: Record<string, string> = {};
		for (const node of nodes) {
			out[node.nodeUUID] = String(node.status || '').trim().toLowerCase();
		}
		return out;
	});

	function isOnlineNode(nodeId: string): boolean {
		return nodeStatusByID[String(nodeId || '').trim()] === 'online';
	}

	let sourceNodeOptions = $derived.by(() => [{ value: '', label: 'None' }, ...nodeOptions]);
	let workloadNodeOptions = $derived.by(() => [{ value: '', label: 'All Nodes' }, ...nodeOptions]);
	let selectedPolicyOwnerNodeId = $derived.by(() =>
		String(selectedPolicy?.activeNodeId || selectedPolicy?.sourceNodeId || '').trim()
	);
	let selectedPolicyOwnerOnline = $derived.by(() =>
		selectedPolicyOwnerNodeId ? isOnlineNode(selectedPolicyOwnerNodeId) : true
	);
	let failoverModeCards = $derived.by(() => failoverModeOptions(selectedPolicyOwnerOnline));

	let failoverTargetOptions = $derived.by(() => {
		const policy = selectedPolicy;
		if (!policy) {
			return [{ value: '', label: 'Auto-pick the best target from policy priority' }, ...nodeOptions];
		}

		const ownerNodeID = (policy.activeNodeId || policy.sourceNodeId || '').trim();
		const configuredTargets = new Set(
			policy.targets
				.map((target) => String(target.nodeId || '').trim())
				.filter((value) => value.length > 0 && value !== ownerNodeID)
		);
		const scopedOptions = nodeOptions.filter(
			(option) => configuredTargets.has(option.value) && isOnlineNode(option.value)
		);
		return [{ value: '', label: 'Auto-pick the best target from policy priority' }, ...scopedOptions];
	});
	let failoverTargetHint = $derived.by(() => {
		if (!selectedPolicy) return '';
		if (failoverTargetOptions.length > 1) return '';
		return 'No online target server is currently available for this policy.';
	});

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
			field: 'status',
			title: 'Status',
			width: 150,
			minWidth: 130,
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData() as { enabled: boolean; lastStatus: string };
				const icons = [];
				if (row.enabled) {
					icons.push(renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500'));
				} else {
					icons.push(renderWithIcon('mdi:close-circle', 'Disabled', 'text-red-500'));
				}

				const lastStatus = String(row.lastStatus || '').toLowerCase();
				if (lastStatus === 'success') {
					icons.push(renderWithIcon('mdi:check-circle', 'Success', 'text-green-500'));
				} else if (lastStatus === 'failed') {
					icons.push(renderWithIcon('mdi:close-circle', 'Failed', 'text-red-500'));
				} else if (lastStatus === 'running') {
					icons.push(renderWithIcon('mdi:progress-clock', 'Running', 'text-yellow-500'));
				}

				return `<div class="flex flex-col gap-1">${icons.join(' ')}</div>`;
			}
		},
		{ field: 'name', title: 'Policy', width: 190, minWidth: 150 },
		{
			field: 'workload',
			title: 'Workload',
			width: 120,
			minWidth: 110,
			formatter: (cell: CellComponent) => {
				const data = cell.getRow().getData();
				const icon =
					data.guestType === 'jail' ? 'hugeicons:prison' : 'material-symbols:monitor-outline';
				return renderWithIcon(icon, String(cell.getValue()));
			}
		},
		{ field: 'activeNode', title: 'Active Node', width: 170, minWidth: 130 },
		{ field: 'mode', title: 'Behavior', width: 320, minWidth: 240 },
		{ field: 'targets', title: 'Targets', width: 260, minWidth: 180 },
		{ field: 'schedule', title: 'Schedule', width: 190, minWidth: 150 },
		{
			field: 'lastRunAt',
			title: 'Last Run',
			width: 170,
			minWidth: 145,
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		},
		{
			field: 'nextRunAt',
			title: 'Next Run',
			width: 170,
			minWidth: 145,
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				return value ? convertDbTime(value) : '-';
			}
		}
	];

	let tableData = $derived.by(() => ({
		rows: policies.current.map((policy) => {
			const workloadLabel =
				policy.guestType === 'jail' ? `Jail ${policy.guestId}` : `VM ${policy.guestId}`;
			const sourceNode = policy.activeNodeId || policy.sourceNodeId || '';
			const sourceLabel = compactNodeLabel(sourceNode);
			const targetsLabel =
				policy.targets
					?.map((target) => `${compactNodeLabel(target.nodeId)} (${target.weight})`)
					.join(' | ') || '-';

				return {
					id: policy.id,
					status: policy.id,
					enabled: policy.enabled,
					name: policy.name,
					guestType: policy.guestType,
					workload: workloadLabel,
					activeNode: sourceLabel,
					mode: policyModeSummary(policy),
					targets: targetsLabel,
					schedule: scheduleLabel(policy.cronExpr),
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
		policyModal.workloadNodeId = '';
		policyModal.guestId = '';
		policyModal.sourceMode = 'follow_active';
		policyModal.sourceNodeId = '';
		policyModal.failbackMode = 'manual';
		policyModal.failoverMode = 'manual';
		policyModal.confirmAutoForce = false;
		policyModal.cronExpr = '*/15 * * * *';
		policyModal.enabled = true;
		policyModal.targets = [{ nodeId: '', weight: '100' }];
	}

	function openCreatePolicy() {
		resetPolicyModal();
		policyModal.open = true;
		void loadVMsForNode();
	}

	async function openEditPolicy() {
		if (selectedPolicyId === 0) return;
		const policy = policies.current.find((entry) => entry.id === selectedPolicyId);
		if (!policy) return;

		policyModal.open = true;
		policyModal.edit = true;
		policyModal.name = policy.name;
		policyModal.guestType = policy.guestType;
		policyModal.workloadNodeId = policy.activeNodeId || policy.sourceNodeId || '';
		policyModal.guestId = String(policy.guestId);
		policyModal.sourceMode = policy.sourceMode;
		policyModal.sourceNodeId = policy.sourceNodeId || '';
		policyModal.failbackMode = policy.failbackMode;
		policyModal.failoverMode = policy.failoverMode || 'manual';
		policyModal.confirmAutoForce = (policy.failoverMode || 'manual') === 'auto_force';
		policyModal.cronExpr = policy.cronExpr || '';
		policyModal.enabled = policy.enabled;
		policyModal.targets =
			policy.targets.length > 0
				? policy.targets.map((target) => ({
						nodeId: target.nodeId,
						weight: String(target.weight || 100)
					}))
				: [{ nodeId: '', weight: '100' }];

		if (policyModal.guestType === 'jail') {
			await loadJailsForNode(true);
			return;
		}
		await loadVMsForNode(true);
	}

	function selectedWorkloadHostname(): string {
		const nodeId = policyModal.workloadNodeId.trim();
		if (!nodeId) return '';

		const selectedNode = nodes.find((node) => node.nodeUUID === nodeId);
		if (selectedNode?.hostname) {
			return selectedNode.hostname;
		}

		const nodeByHostname = nodes.find((node) => node.hostname === nodeId);
		return nodeByHostname?.hostname || nodeId;
	}

	async function loadJailsForNode(force: boolean = false) {
		const hostname = selectedWorkloadHostname();
		if (jailsLoading) return;
		if (!force && jailsLoadedForNode === hostname) return;
		jailsLoading = true;
		try {
			const res = await getJails(hostname || undefined);
			updateCache(hostname ? `jail-list-${hostname}` : 'jail-list', res);
			jails = res;
			jailsLoadedForNode = hostname;
		} finally {
			jailsLoading = false;
		}
	}

	async function loadVMsForNode(force: boolean = false) {
		const hostname = selectedWorkloadHostname();
		if (vmsLoading) return;
		if (!force && vmsLoadedForNode === hostname) return;
		vmsLoading = true;
		try {
			const res = await getVMs(hostname || undefined);
			updateCache(hostname ? `vm-list-${hostname}` : 'vm-list', res);
			vms = res;
			vmsLoadedForNode = hostname;
		} finally {
			vmsLoading = false;
		}
	}

	function closePolicyModal() {
		resetPolicyModal();
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
				toast.error('Each target server can be added only once.', { position: 'bottom-center' });
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
			toast.error('Add at least one target server.', { position: 'bottom-center' });
			return null;
		}

		return parsedTargets;
	}

	function buildPolicyPayload(): ReplicationPolicyInput | null {
		const name = policyModal.name.trim();
		if (!name) {
			toast.error('Give this policy a name.', { position: 'bottom-center' });
			return null;
		}

		const guestId = Number.parseInt(policyModal.guestId, 10);
		if (!Number.isFinite(guestId) || guestId <= 0) {
			toast.error('Choose the VM or jail to protect.', { position: 'bottom-center' });
			return null;
		}

		const targets = parseTargetsInput(policyModal.targets);
		if (!targets) return null;

		const sourceNodeId = policyModal.sourceNodeId.trim();
		if (policyModal.sourceMode === 'pinned_primary' && !sourceNodeId) {
			toast.error('Pick the preferred primary node for this policy.', { position: 'bottom-center' });
			return null;
		}
		if (policyModal.failoverMode === 'auto_force' && !policyModal.confirmAutoForce) {
			toast.error('Confirm the risk before enabling automatic force recovery.', {
				position: 'bottom-center'
			});
			return null;
		}

		return {
			name,
			guestType: policyModal.guestType,
			guestId,
			sourceMode: policyModal.sourceMode,
			sourceNodeId,
			failbackMode: policyModal.failbackMode,
			failoverMode: policyModal.failoverMode,
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

	function openFailoverModal() {
		if (!selectedPolicy) return;
		failoverModal.mode = normalizeFailoverMode('safe', selectedPolicyOwnerOnline);
		failoverModal.targetNodeId = '';
		failoverModal.movePinnedSource = true;
		failoverModal.confirmDataLoss = false;
		failoverModal.forceConfirmText = '';
		failoverModalOpen = true;
	}

	function closeFailoverModal() {
		failoverModalOpen = false;
	}

	let failoverLoading = $state(false);

	async function failoverNow() {
		if (!selectedPolicyId) return;
		const validationError = validateFailoverAction({
			mode: failoverModal.mode,
			ownerOnline: selectedPolicyOwnerOnline,
			confirmDataLoss: failoverModal.confirmDataLoss,
			forceConfirmationText: failoverModal.forceConfirmText
		});
		if (validationError) {
			toast.error(validationError, { position: 'bottom-center' });
			return;
		}

		failoverLoading = true;

		const result = await failoverReplicationPolicy(selectedPolicyId, {
			targetNodeId: failoverModal.targetNodeId.trim() || undefined,
			mode: failoverModal.mode,
			confirmDataLoss: failoverModal.mode === 'force' ? true : undefined,
			movePinnedSource: failoverModal.movePinnedSource
		});

		failoverLoading = false;

		if (result.status === 'success') {
			toast.success(
				failoverModal.mode === 'force'
					? 'Force recovery requested.'
					: 'Safe move requested.',
				{ position: 'bottom-center' }
			);
			failoverModalOpen = false;
			reload = true;
			return;
		}

		handleAPIError(result);
		toast.error(userFailoverErrorMessage(result.message || '', result.error || ''), {
			position: 'bottom-center'
		});
	}

	watch(
		[() => policyModal.open, () => policyModal.workloadNodeId, () => policyModal.guestType],
		([isOpen, _workloadNodeId, guestType]) => {
			if (!isOpen) return;
			if (guestType === 'jail') {
				void loadJailsForNode(true);
				return;
			}
			void loadVMsForNode(true);
		}
	);

	watch(
		[() => failoverModalOpen, () => selectedPolicyOwnerOnline],
		([isOpen, ownerOnline]) => {
			if (!isOpen) return;
			failoverModal.mode = normalizeFailoverMode(failoverModal.mode, ownerOnline);
			if (failoverModal.mode !== 'force') {
				failoverModal.confirmDataLoss = false;
				failoverModal.forceConfirmText = '';
			}
		}
	);

	watch(
		() => policyModal.failoverMode,
		(mode) => {
			if (mode !== 'auto_force') {
				policyModal.confirmAutoForce = false;
			}
		}
	);

	watch(
		() => failoverModal.mode,
		(mode) => {
			if (mode !== 'force') {
				failoverModal.confirmDataLoss = false;
				failoverModal.forceConfirmText = '';
			}
		}
	);

	let humanCron = $derived.by(() => {
		try {
			return cronToHuman(policyModal.cronExpr);
		} catch {
			return '';
		}
	});
</script>

{#snippet actionButtons(type: string)}
	{#if type === 'failover' && selectedPolicyId > 0}
		<Button onclick={openFailoverModal} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<span class="icon-[mdi--swap-horizontal-bold] mr-1 h-4 w-4"></span>
				<span>Move Active</span>
			</div>
		</Button>
	{/if}

	{#if type === 'run' && selectedPolicyId > 0}
		<Button onclick={runNow} size="sm" variant="outline" class="h-6">
			<div class="flex items-center">
				<span class="icon-[mdi--play] mr-1 h-4 w-4"></span>
				<span>Sync Now</span>
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
				<span>New Policy</span>
			</div>
		</Button>

		{@render actionButtons('failover')}
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
	<Dialog.Content class="w-[90%] max-w-4xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<span>{policyModal.edit ? 'Edit Protection Policy' : 'New Protection Policy'}</span>
				<Button size="sm" variant="link" class="h-4" title="Close" onclick={closePolicyModal}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Close</span>
				</Button>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<p class="text-muted-foreground text-sm">
				Use the guided settings below for normal HA behavior. Advanced controls are available at the
				end.
			</p>

			<CustomValueInput
				label="Policy name"
				placeholder="critical-vm-ha"
				bind:value={policyModal.name}
				classes="space-y-1"
			/>

			<div class="grid grid-cols-1 gap-3 md:grid-cols-3">
				<SimpleSelect
					label="Protect"
					value={policyModal.guestType}
					options={[
						{ value: 'vm', label: 'Virtual machine (VM)' },
						{ value: 'jail', label: 'Jail (container)' }
					]}
					onChange={(value) => {
						policyModal.guestType = (value || 'vm') as 'vm' | 'jail';
						policyModal.guestId = '';
						if (policyModal.guestType === 'jail') {
							void loadJailsForNode(true);
							return;
						}
						void loadVMsForNode(true);
					}}
				/>

				<SimpleSelect
					label="Find workload on"
					value={policyModal.workloadNodeId}
					options={workloadNodeOptions}
					onChange={(value) => {
						policyModal.workloadNodeId = value;
						policyModal.guestId = '';
					}}
				/>

				<SimpleSelect
					label="Workload to protect"
					value={policyModal.guestId}
					options={guestOptions}
					placeholder={policyModal.guestType === 'vm' ? 'Choose VM' : 'Choose Jail'}
					disabled={policyModal.guestType === 'vm'
						? vmsLoading || guestOptions.length === 0
						: jailsLoading || guestOptions.length === 0}
					onChange={(value) => {
						policyModal.guestId = value;
					}}
				/>
			</div>

			<div class="rounded-md border p-3">
				<div class="mb-2">
					<p class="text-sm font-medium">If the active server goes down</p>
					<p class="text-muted-foreground text-xs">
						Choose what should happen automatically.
					</p>
				</div>
				<RadioGroup.Root bind:value={policyModal.failoverMode} class="gap-2">
					{#each failoverCards as card (card.value)}
						<label
							for={`policy-failover-${card.value}`}
							class={`flex cursor-pointer items-start gap-3 rounded-md border p-3 ${
								policyModal.failoverMode === card.value ? 'border-primary bg-muted/30' : 'border-border'
							} ${card.dangerous ? 'border-red-500/40' : ''}`}
						>
							<RadioGroup.Item value={card.value} id={`policy-failover-${card.value}`} class="mt-1" />
							<div class="space-y-1">
								<p class="text-sm font-medium">{card.title}</p>
								<p class="text-muted-foreground text-xs">{card.description}</p>
								<p class={`text-xs ${card.dangerous ? 'text-red-300' : 'text-muted-foreground'}`}>
									{card.impact}
								</p>
							</div>
						</label>
					{/each}
				</RadioGroup.Root>
				{#if policyModal.failoverMode === 'auto_force'}
					<div class="mt-3 rounded-md border border-red-500/40 bg-red-500/10 p-2">
						<CustomCheckbox
							label="I understand automatic force recovery can lose the newest writes."
							bind:checked={policyModal.confirmAutoForce}
							classes="flex items-start gap-2"
						/>
					</div>
				{/if}
			</div>

			<div class="rounded-md border p-3">
				<div class="mb-2">
					<p class="text-sm font-medium">Normal source behavior</p>
					<p class="text-muted-foreground text-xs">
						Choose whether the source follows the active node or stays pinned to one preferred
						node.
					</p>
				</div>
				<RadioGroup.Root bind:value={policyModal.sourceMode} class="gap-2">
					{#each sourceModeCards as card (card.value)}
						<label
							for={`policy-source-${card.value}`}
							class={`flex cursor-pointer items-start gap-3 rounded-md border p-3 ${
								policyModal.sourceMode === card.value ? 'border-primary bg-muted/30' : 'border-border'
							}`}
						>
							<RadioGroup.Item value={card.value} id={`policy-source-${card.value}`} class="mt-1" />
							<div class="space-y-1">
								<p class="text-sm font-medium">{card.title}</p>
								<p class="text-muted-foreground text-xs">{card.description}</p>
							</div>
						</label>
					{/each}
				</RadioGroup.Root>
			</div>

			<div class="rounded-md border p-3">
				<div class="mb-2 flex items-center justify-between">
					<div>
						<p class="text-sm font-medium">Target servers</p>
						<p class="text-muted-foreground text-xs">
							Higher priority gets picked first when auto-selecting a target.
						</p>
					</div>
					<Button size="sm" variant="outline" class="h-6" onclick={addTargetRow}>
						<div class="flex items-center">
							<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
							<span>Add target</span>
						</div>
					</Button>
				</div>

				<div class="space-y-2">
					{#each policyModal.targets as target, idx (idx)}
						<div class="grid grid-cols-1 items-end gap-2 md:grid-cols-[1fr_140px_auto]">
							<SimpleSelect
								label={idx === 0 ? 'Target server' : ''}
								value={target.nodeId}
								options={nodeOptions}
								placeholder="Choose target"
								onChange={(value) => {
									policyModal.targets[idx].nodeId = value;
								}}
							/>

							<CustomValueInput
								label={idx === 0 ? 'Priority' : ''}
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

			<details class="rounded-md border p-3">
				<summary class="cursor-pointer text-sm font-medium">Advanced options</summary>
				<div class="mt-3 grid gap-3">
					<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
						<SimpleSelect
							label="Preferred primary node"
							value={policyModal.sourceNodeId}
							options={sourceNodeOptions}
							disabled={policyModal.sourceMode !== 'pinned_primary'}
							onChange={(value) => {
								policyModal.sourceNodeId = value;
							}}
						/>

						<SimpleSelect
							label="When preferred primary comes back"
							value={policyModal.failbackMode}
							options={[
								{ value: 'manual', label: 'Stay on current active node until admin moves it' },
								{ value: 'auto', label: 'Automatically move back to preferred primary node' }
							]}
							onChange={(value) => {
								policyModal.failbackMode = (value || 'manual') as 'manual' | 'auto';
							}}
						/>
					</div>

					<div class="grid grid-cols-[1fr_auto] items-end gap-3">
						<CustomValueInput
							label="Sync schedule (cron)"
							placeholder="*/15 * * * *"
							bind:value={policyModal.cronExpr}
							classes="space-y-1"
						/>
						<CustomCheckbox
							label="Policy enabled"
							bind:checked={policyModal.enabled}
							classes="mb-2 flex items-center gap-2"
						/>
					</div>
					<p class="text-muted-foreground text-xs">
						{humanCron
							? `Current schedule: ${humanCron}.`
							: 'Enter a valid cron schedule, for example: */15 * * * *'}
					</p>
				</div>
			</details>
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={resetPolicyModal}>Cancel</Button>
			<Button onclick={savePolicy}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={failoverModalOpen}>
	<Dialog.Content class="w-[90%] max-w-xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<span>Move Active Workload</span>
				<Button size="sm" variant="link" class="h-4" title="Close" onclick={closeFailoverModal}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Close</span>
				</Button>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<p class="text-muted-foreground text-sm">
				Choose how to move policy <span class="font-medium">{selectedPolicyName || '-'}</span>.
			</p>

			{#if !selectedPolicyOwnerOnline}
				<div class="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-200">
					Safe move is unavailable because the current active node appears down.
				</div>
			{/if}

			<div class="rounded-md border p-3">
				<div class="mb-2">
					<p class="text-sm font-medium">Move type</p>
					<p class="text-muted-foreground text-xs">
						Use Safe move when possible. Use Force recovery only for hard-down owner scenarios.
					</p>
				</div>
				<RadioGroup.Root bind:value={failoverModal.mode} class="gap-2">
					{#each failoverModeCards as card (card.value)}
						<label
							for={`failover-mode-${card.value}`}
							class={`flex cursor-pointer items-start gap-3 rounded-md border p-3 ${
								failoverModal.mode === card.value ? 'border-primary bg-muted/30' : 'border-border'
							} ${card.value === 'force' ? 'border-red-500/40' : ''}`}
						>
							<RadioGroup.Item value={card.value} id={`failover-mode-${card.value}`} class="mt-1" />
							<div class="space-y-1">
								<p class="text-sm font-medium">{card.title}</p>
								<p class="text-muted-foreground text-xs">{card.description}</p>
								<p class={`text-xs ${card.value === 'force' ? 'text-red-300' : 'text-muted-foreground'}`}>
									{card.impact}
								</p>
							</div>
						</label>
					{/each}
				</RadioGroup.Root>
			</div>

			<SimpleSelect
				label="Move to (optional)"
				value={failoverModal.targetNodeId}
				options={failoverTargetOptions}
				disabled={failoverTargetOptions.length <= 1}
				onChange={(value) => {
					failoverModal.targetNodeId = value;
				}}
			/>
			{#if failoverTargetHint}
				<p class="text-xs text-amber-300">{failoverTargetHint}</p>
			{/if}

			{#if selectedPolicy?.sourceMode === 'pinned_primary'}
				<CustomCheckbox
					label="Also set this target as the new preferred primary (prevents bounce-back)"
					bind:checked={failoverModal.movePinnedSource}
					classes="flex items-center gap-2"
				/>
			{/if}

			{#if failoverModal.mode === 'force'}
				<div class="rounded-md border border-red-500/40 bg-red-500/10 p-3">
					<div class="space-y-3">
						<CustomCheckbox
							label="I understand Force recovery may lose the newest writes from the old active node."
							bind:checked={failoverModal.confirmDataLoss}
							classes="flex items-start gap-2"
						/>
						<CustomValueInput
							label={`Type ${FORCE_RECOVERY_CONFIRM_TEXT} to continue`}
							placeholder={FORCE_RECOVERY_CONFIRM_TEXT}
							bind:value={failoverModal.forceConfirmText}
							classes="space-y-1"
						/>
					</div>
				</div>
			{/if}
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={closeFailoverModal}>Cancel</Button>
			<Button
				variant={failoverModal.mode === 'force' ? 'destructive' : 'default'}
				onclick={failoverNow}
				disabled={failoverLoading || failoverTargetOptions.length <= 1}
			>
				{#if failoverLoading}
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--loading] animate-spin h-4 w-4"></span>
						<span>Requesting</span>
					</div>
				{:else}
					<span>{failoverModal.mode === 'force' ? 'Force recovery' : 'Safe move'}</span>
				{/if}
			</Button>
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
