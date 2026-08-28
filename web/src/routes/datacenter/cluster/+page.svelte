<script lang="ts">
	import {
		getDetails,
		getJoinStatus,
		getLeaveStatus,
		getNodes,
		refreshClusterAfterLifecycleChange,
		resetCluster
	} from '$lib/api/cluster/cluster';
	import Create from '$lib/components/custom/Cluster/Create.svelte';
	import ForceReset from '$lib/components/custom/Cluster/ForceReset.svelte';
	import Join from '$lib/components/custom/Cluster/Join.svelte';
	import JoinInformation from '$lib/components/custom/Cluster/JoinInformation.svelte';
	import JoinProgress from '$lib/components/custom/Cluster/JoinProgress.svelte';
	import LeaveProgress from '$lib/components/custom/Cluster/LeaveProgress.svelte';
	import RemovePeer from '$lib/components/custom/Cluster/RemovePeer.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type {
		ClusterDetails,
		ClusterJoinStatus,
		ClusterLeaveStatus,
		ClusterNode
	} from '$lib/types/cluster/cluster';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { reload } from '$lib/stores/api.svelte';
	import { getClusterLeaveErrorMessage } from '$lib/utils/cluster';
	import { handleAPIError, isAPIResponse, removeCache, updateCache } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { onMount } from 'svelte';
	import { resource, watch } from 'runed';

	interface Data {
		cluster: ClusterDetails;
	}

	let { data }: { data: Data } = $props();
	let reloadFlag = $state(false);

	// svelte-ignore state_referenced_locally
	const datacenter = resource(
		() => 'cluster-info',
		async () => {
			const res = await getDetails();
			if (isAPIResponse(res)) {
				return data.cluster;
			}

			updateCache('cluster-info', res);
			return res;
		},
		{ initialValue: data.cluster }
	);

	let pendingRecoveryTimer: ReturnType<typeof setTimeout> | null = null;
	let pendingJoinTimer: ReturnType<typeof setTimeout> | null = null;
	let pendingLeaveTimer: ReturnType<typeof setTimeout> | null = null;
	let joinStatus = $state<ClusterJoinStatus | null>(null);
	let leaveStatus = $state<ClusterLeaveStatus | null>(null);
	let activeJoinStatus = $derived(
		joinStatus && joinStatus.phase !== 'voter' && joinStatus.phase !== 'not_started'
			? joinStatus
			: null
	);
	let joinStatusPollingActive = false;
	let leaveStatusPollingActive = false;
	let recoveryAttempts = 0;
	let recoveryActive = false;

	function hasCompleteClusterNodeSnapshot(
		details: ClusterDetails | null | undefined,
		clusterNodes: ClusterNode[]
	): boolean {
		if (!details?.cluster?.enabled) return true;

		const raftNodes = details.nodes ?? [];
		return (
			raftNodes.length > 0 &&
			raftNodes.every((raftNode) =>
				clusterNodes.some((clusterNode) => clusterNode.nodeUUID === raftNode.id)
			)
		);
	}

	async function recoverClusterSnapshot() {
		if (!recoveryActive || hasCompleteClusterNodeSnapshot(datacenter.current, nodes.current)) {
			return;
		}

		recoveryAttempts += 1;
		await Promise.allSettled([datacenter.refetch(), nodes.refetch()]);

		if (
			!recoveryActive ||
			hasCompleteClusterNodeSnapshot(datacenter.current, nodes.current) ||
			recoveryAttempts >= 8
		) {
			return;
		}

		pendingRecoveryTimer = setTimeout(() => {
			void recoverClusterSnapshot();
		}, 1000);
	}

	function startRecovery() {
		stopRecovery();
		recoveryActive = true;
		recoveryAttempts = 0;
		void recoverClusterSnapshot();
	}

	function stopRecovery() {
		recoveryActive = false;
		if (pendingRecoveryTimer) {
			clearTimeout(pendingRecoveryTimer);
			pendingRecoveryTimer = null;
		}
	}

	async function refreshJoinStatus() {
		if (!joinStatusPollingActive) return;
		if (!datacenter.current.cluster.enabled || datacenter.current.cluster.raftBootstrap === true) {
			joinStatus = null;
			return;
		}

		const result = await getJoinStatus();
		if (!isAPIResponse(result)) {
			joinStatus = result;
		}
		if (joinStatusPollingActive && (isAPIResponse(result) || joinStatus?.retrying)) {
			pendingJoinTimer = setTimeout(() => {
				void refreshJoinStatus();
			}, 3000);
		}
	}

	function stopJoinStatusPolling() {
		joinStatusPollingActive = false;
		if (pendingJoinTimer) {
			clearTimeout(pendingJoinTimer);
			pendingJoinTimer = null;
		}
	}

	function startJoinStatusPolling() {
		stopJoinStatusPolling();
		joinStatusPollingActive = true;
		void refreshJoinStatus();
	}

	async function refreshLeaveStatus() {
		if (!leaveStatusPollingActive) return;
		const wasActive = Boolean(leaveStatus?.phase);
		const result = await getLeaveStatus();
		if (!isAPIResponse(result)) {
			leaveStatus = result;
			if (
				wasActive !== Boolean(result.phase) ||
				result.enabled !== datacenter.current.cluster.enabled
			) {
				await Promise.allSettled([datacenter.refetch(), nodes.refetch()]);
			}
		}
		if (
			leaveStatusPollingActive &&
			(isAPIResponse(result) || leaveStatus?.phase || datacenter.current.cluster.enabled)
		) {
			pendingLeaveTimer = setTimeout(() => {
				void refreshLeaveStatus();
			}, 3000);
		}
	}

	function stopLeaveStatusPolling() {
		leaveStatusPollingActive = false;
		if (pendingLeaveTimer) {
			clearTimeout(pendingLeaveTimer);
			pendingLeaveTimer = null;
		}
	}

	function startLeaveStatusPolling() {
		stopLeaveStatusPolling();
		leaveStatusPollingActive = true;
		void refreshLeaveStatus();
	}

	watch(
		() => reloadFlag,
		() => {
			if (reloadFlag) {
				void datacenter.refetch().then(() => {
					startJoinStatusPolling();
					startLeaveStatusPolling();
				});
				void nodes.refetch();
				reloadFlag = false;
				startRecovery();
			}
		}
	);

	watch(
		() => reload.datacenterDetailsPulse,
		() => {
			void datacenter.refetch().then(() => {
				startJoinStatusPolling();
				startLeaveStatusPolling();
			});
			void nodes.refetch();
			startRecovery();
		}
	);

	watch(
		() => reload.datacenterNodesPulse,
		() => {
			void nodes.refetch();
		}
	);

	onMount(() => {
		startJoinStatusPolling();
		startLeaveStatusPolling();
		return () => {
			stopRecovery();
			stopJoinStatusPolling();
			stopLeaveStatusPolling();
		};
	});

	let canReset = $derived(datacenter.current.cluster.enabled === true);
	let canCreate = $derived(
		datacenter.current.cluster.raftBootstrap === null &&
			datacenter.current.cluster.enabled === false
	);

	let canJoin = $derived(
		datacenter.current.cluster.raftBootstrap !== true &&
			datacenter.current.cluster.enabled === false
	);

	let isLeaderUi = $derived(datacenter.current.leaderId === datacenter.current.nodeId);
	let activeRows = $state<Array<Row & { leader: boolean }> | null>(null);
	let selectedPeerRow = $derived<(Row & { leader: boolean }) | null>(activeRows?.[0] ?? null);

	let canRemovePeer = $derived(
		isLeaderUi && !leaveStatus?.phase && selectedPeerRow !== null && !selectedPeerRow.leader
	);

	let nodes = resource(
		() => 'cluster-nodes',
		async (key, prevKey, { signal }) => {
			const result = await getNodes(signal);
			if (hasCompleteClusterNodeSnapshot(datacenter.current, result)) {
				await updateCache('cluster-nodes', result);
			} else {
				await removeCache('cluster-nodes');
			}
			return result;
		},
		{ initialValue: [] as ClusterNode[] }
	);

	function esc(value: unknown): string {
		return String(value ?? '')
			.replace(/&/g, '&amp;')
			.replace(/</g, '&lt;')
			.replace(/>/g, '&gt;')
			.replace(/"/g, '&quot;')
			.replace(/'/g, '&#39;');
	}

	function usageCell(cell: CellComponent): string {
		const percent = Math.min(100, Math.max(0, Number(cell.getValue()) || 0));
		return `<div class="flex items-center gap-1.5"><div class="h-1.5 w-12 overflow-hidden rounded bg-muted"><div class="h-full rounded bg-primary" style="width:${Math.round(percent)}%"></div></div><span class="text-muted-foreground text-xs">${Math.round(percent)}%</span></div>`;
	}

	let modals = $state({
		create: {
			open: false
		},
		view: {
			open: false
		},
		join: {
			open: false
		},
		reset: {
			open: false
		},
		forceReset: {
			open: false
		},
		remove: {
			open: false
		}
	});

	let query = $state('');

	let table = $derived.by(() => {
		const rows: Row[] = [];
		const columns: Column[] = [
			{
				field: 'hostname',
				title: 'Hostname',
				formatter: (cell: CellComponent) => {
					const data = cell.getRow().getData() as Row & {
						leader?: boolean;
						status?: string;
						hostname?: string;
					};

					const online = data.status === 'online';
					const icon = online ? 'mdi--server' : 'mdi--server-off';
					const iconCls = online ? 'text-green-500' : 'text-muted-foreground';
					const name = data.hostname || String(data.id ?? '');
					const nameHtml = data.leader
						? `<span class="inline-flex items-center gap-1"><span class="icon-[mdi--crown] h-3.5 w-3.5 text-muted-foreground"></span><span>${esc(name)}</span></span>`
						: esc(name);
					return `<span class="inline-flex items-center gap-1.5"><span class="icon-[${icon}] h-4 w-4 ${iconCls}"></span><span>${nameHtml}</span></span>`;
				}
			},
			{
				field: 'id',
				title: 'Node ID',
				formatter: (cell: CellComponent) => {
					return `<span class="font-mono text-xs">${esc(cell.getValue())}</span>`;
				}
			},
			{
				field: 'address',
				title: 'Address',
				formatter: (cell: CellComponent) => {
					return `<span class="font-mono text-xs">${esc(cell.getValue())}</span>`;
				}
			},
			{
				field: 'status',
				title: 'Status',
				formatter: (cell: CellComponent) => {
					const data = cell.getRow().getData() as Row;
					const online = data.status === 'online';
					const icon = online ? 'mdi--check-circle' : 'mdi--close-circle';
					const iconCls = online ? 'text-green-500' : 'text-muted-foreground';
					return `<span class="inline-flex items-center gap-1"><span class="icon-[${icon}] h-4 w-4 ${iconCls}"></span><span class="text-muted-foreground">${online ? 'Online' : 'Offline'}</span></span>`;
				}
			},
			{
				field: 'suffrage',
				title: 'Suffrage',
				formatter: (cell: CellComponent) => {
					let value = '';
					switch (cell.getValue()) {
						case 'voter':
							value = 'Voter';
							break;
						case 'nonvoter':
							value = 'Non Voter';
							break;
						case 'staging':
							value = 'Staging';
							break;
						default:
							value = 'Unknown';
					}

					return value;
				}
			},
			{
				field: 'guestCount',
				title: 'Guests',
				formatter: (cell: CellComponent) => {
					return String(cell.getValue() ?? 0);
				}
			},
			{
				field: 'cpuUsage',
				title: 'CPU',
				formatter: usageCell
			},
			{
				field: 'memoryUsage',
				title: 'RAM',
				formatter: usageCell
			},
			{
				field: 'diskUsage',
				title: 'Disk',
				formatter: usageCell
			}
		];

		const nodesById = new Map(nodes.current.map((node) => [node.nodeUUID, node]));

		for (const node of datacenter.current.nodes ?? []) {
			const health = nodesById.get(node.id);

			rows.push({
				id: node.id,
				leader: node.isLeader,
				address: node.address,
				suffrage: node.suffrage,
				hostname: health?.hostname ?? '',
				status: health?.status ?? 'offline',
				guestCount: node.guestIDs?.length ?? health?.guestIDs?.length ?? 0,
				cpuUsage: health?.cpuUsage ?? 0,
				memoryUsage: health?.memoryUsage ?? 0,
				diskUsage: health?.diskUsage ?? 0
			});
		}

		return {
			rows,
			columns
		};
	});
</script>

{#snippet button(type: string, icon: string, title: string, disabled: boolean)}
	<Button
		onclick={() => {
			switch (type) {
				case 'create':
					modals.create.open = true;
					break;
				case 'join':
					modals.join.open = true;
					break;
				case 'reset':
					modals.reset.open = true;
					break;
				case 'force-reset':
					modals.forceReset.open = true;
					break;
				case 'remove':
					modals.remove.open = true;
					break;
			}
		}}
		size="sm"
		variant="outline"
		class="h-6.5"
		{disabled}
	>
		<div class="flex items-center">
			<span class="icon-[{icon}] mr-1 h-4 w-4"></span>
			<span>{title}</span>
		</div>
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		{#if activeJoinStatus}
			<JoinProgress status={activeJoinStatus} />
		{/if}
		{#if leaveStatus?.phase}
			<LeaveProgress
				status={leaveStatus}
				onRetry={() => (modals.reset.open = true)}
				onForceReset={() => (modals.forceReset.open = true)}
			/>
		{/if}

		{#if !canCreate}
			<Button onclick={() => (modals.view.open = true)} size="sm" class="h-6  ">
				<div class="flex items-center">
					<span class="icon-[mdi--eye] mr-1 h-4 w-4"></span>

					<span>View Join Information</span>
				</div>
			</Button>
		{/if}

		{#if canCreate}
			{@render button('create', 'oui--ml-create-population-job', 'Create Cluster', !canCreate)}
		{/if}

		{#if canJoin}
			{@render button('join', 'grommet-icons--cluster', 'Join Cluster', !canJoin)}
		{/if}

		{#if canReset && !leaveStatus?.phase}
			{@render button('reset', 'mdi--refresh', 'Leave / Reset Cluster', !canReset)}
		{/if}

		{#if canRemovePeer}
			{@render button('remove', 'mdi--account-remove-outline', 'Remove Peer', false)}
		{/if}
	</div>

	<TreeTable
		data={table}
		name="cluster-nodes-tt"
		bind:query
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
	/>
</div>

<Create bind:open={modals.create.open} bind:reload={reloadFlag} />

<JoinInformation bind:open={modals.view.open} cluster={datacenter.current} />

<Join bind:open={modals.join.open} bind:reload={reloadFlag} />

<RemovePeer bind:open={modals.remove.open} bind:reload={reloadFlag} node={selectedPeerRow} />

<AlertDialog
	open={modals.reset.open}
	customTitle="This will reset all clustered data and configuration on THIS node, including all notes, backup targets, jobs and events. This action cannot be undone."
	actions={{
		onConfirm: async () => {
			const response = await resetCluster();
			reloadFlag = true;
			if (response.error) {
				startLeaveStatusPolling();
				handleAPIError(response);
				const detail = Array.isArray(response.error)
					? response.error.join(', ')
					: String(response.error || response.message);
				toast.error(getClusterLeaveErrorMessage(`${response.message}: ${detail}`), {
					position: 'bottom-center'
				});
				modals.reset.open = false;
				return;
			}

			await refreshClusterAfterLifecycleChange();
			modals.reset.open = false;
			toast.success('Cluster leave completed', {
				position: 'bottom-center'
			});
		},
		onCancel: () => {
			modals.reset.open = false;
		}
	}}
></AlertDialog>

<ForceReset
	bind:open={modals.forceReset.open}
	bind:reload={reloadFlag}
	nodeId={leaveStatus?.localNodeId || datacenter.current.nodeId}
/>
