<script lang="ts">
	import { logOut } from '$lib/api/auth';
	import { getDetails, resetCluster } from '$lib/api/cluster/cluster';
	import Create from '$lib/components/custom/Cluster/Create.svelte';
	import Join from '$lib/components/custom/Cluster/Join.svelte';
	import JoinInformation from '$lib/components/custom/Cluster/JoinInformation.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import type { ClusterDetails } from '$lib/types/cluster/cluster';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import { resource, watch } from 'runed';

	interface Data {
		cluster: ClusterDetails;
	}

	let { data }: { data: Data } = $props();
	let reload = $state(false);

	// svelte-ignore state_referenced_locally
	const datacenter = resource(
		() => 'cluster-info',
		async (key, prevKey, { signal }) => {
			const res = await getDetails();
			updateCache('cluster-info', res);
			return res;
		},
		{ initialValue: data.cluster }
	);

	watch(
		() => reload,
		() => {
			if (reload) {
				datacenter.refetch();
				reload = false;
			}
		}
	);

	let canReset = $derived(datacenter.current.cluster.enabled === true);
	let canCreate = $derived(
		datacenter.current.cluster.raftBootstrap === null &&
			datacenter.current.cluster.enabled === false
	);

	let canJoin = $derived(
		datacenter.current.cluster.raftBootstrap !== true &&
			datacenter.current.cluster.enabled === false
	);

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
		}
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let table = $derived.by(() => {
		const rows: Row[] = [];
		const columns: Column[] = [
			{
				field: 'id',
				title: 'Node ID',
				formatter: (cell: CellComponent) => {
					const row = cell.getRow();
					const data = row.getData();

					if (data.leader) {
						return renderWithIcon('fluent-mdl2:party-leader', cell.getValue());
					} else {
						return cell.getValue();
					}
				}
			},
			{
				field: 'address',
				title: 'Address'
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
			}
		];

		if (datacenter.current.nodes) {
			for (const node of datacenter.current.nodes) {
				rows.push({
					id: node.id,
					leader: node.isLeader,
					address: node.address,
					suffrage: node.suffrage
				});
			}
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

		{#if canReset}
			{@render button('reset', 'mdi--refresh', 'Reset Cluster', !canReset)}
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

<Create bind:open={modals.create.open} bind:reload />
<JoinInformation bind:open={modals.view.open} cluster={datacenter.current} />
<Join bind:open={modals.join.open} bind:reload />

<AlertDialog
	open={modals.reset.open}
	customTitle={`This will reset all clustered data and configuration, including all notes, backup targets, jobs and events. This action cannot be undone.`}
	actions={{
		onConfirm: async () => {
			const response = await resetCluster();
			storage.clusterToken = '';
			reload = true;
			if (response.error) {
				if (response.error.includes('leader_cannot_reset_while_other_nodes_exist')) {
					toast.error('Leader cannot exit when followers are present', {
						position: 'bottom-center'
					});

					modals.reset.open = false;
					return;
				}

				handleAPIError(response);
				toast.error('Failed to reset cluster', {
					position: 'bottom-center'
				});
				return;
			}

			modals.reset.open = false;
			await logOut('Login required after cluster reset');
		},
		onCancel: () => {
			modals.reset.open = false;
		}
	}}
></AlertDialog>
