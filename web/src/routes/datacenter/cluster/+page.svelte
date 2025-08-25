<script lang="ts">
	import { createCluster, getCluster, joinCluster } from '$lib/api/datacenter/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { nodeId } from '$lib/stores/basic';
	import type { DataCenter } from '$lib/types/datacenter/cluster';
	import { handleAPIError } from '$lib/utils/http';
	import Icon from '@iconify/svelte';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import { toast } from 'svelte-sonner';

	interface Data {
		cluster: DataCenter;
	}

	let { data }: { data: Data } = $props();
	let reload = $state(false);
	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: 'cluster-info',
			queryFn: async () => {
				return await getCluster();
			},
			keepPreviousData: true,
			initialData: data.cluster,
			refetchOnMount: 'always'
		}
	]);

	$effect(() => {
		if (reload) {
			queryClient.refetchQueries('cluster-info');
			reload = false;
		}
	});

	let cluster = $derived($results[0].data);
	let canCreate = $derived(cluster === undefined || cluster === null);

	async function handleCreate() {
		const response = await createCluster();
		if (response.error) {
			handleAPIError(response);
			toast.error('Error creating cluster', {
				position: 'bottom-center'
			});
			return;
		}

		if (response.message === 'cluster_created') {
			reload = true;
			toast.success('Cluster created', {
				position: 'bottom-center'
			});
		}
	}

	let modals = $state({
		view: {
			open: false
		},
		join: {
			open: false,
			leaderAPI: '',
			nodeAddr: '',
			clusterKey: ''
		}
	});

	async function handleView() {
		modals.view.open = true;
	}

	async function handleJoin() {
		const response = await joinCluster(
			$nodeId,
			modals.join.nodeAddr,
			modals.join.leaderAPI,
			modals.join.clusterKey
		);
		console.log(response);
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		{#if cluster?.id}
			<Button
				onclick={() => {
					handleView();
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
				disabled={canCreate}
			>
				<div class="flex items-center">
					<Icon icon="mdi:eye" class="mr-1 h-4 w-4" />
					<span>View</span>
				</div>
			</Button>
		{/if}

		<Button
			onclick={() => {
				handleCreate();
			}}
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={!canCreate}
		>
			<div class="flex items-center">
				<Icon icon="oui:ml-create-population-job" class="mr-1 h-4 w-4" />
				<span>Create Cluster</span>
			</div>
		</Button>

		<Button
			onclick={() => {
				modals.join.open = true;
			}}
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={!canCreate}
		>
			<div class="flex items-center">
				<Icon icon="oui:ml-create-population-job" class="mr-1 h-4 w-4" />
				<span>Join Cluster</span>
			</div>
		</Button>
	</div>
</div>

<Dialog.Root bind:open={modals.view.open}>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<Icon icon="oui:ml-create-population-job" class="h-6 w-6" />
					<span>Cluster Information</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							modals.view.open = false;
						}}
					>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="p-4">
			<p>Node ID: {$nodeId}</p>
			<p>Cluster ID: {cluster?.id}</p>
			<p>Cluster Key: {cluster?.clusterKey}</p>
		</div>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={modals.join.open}>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<Icon icon="oui:ml-create-population-job" class="h-6 w-6" />
					<span>Join Cluster</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							modals.join.open = false;
						}}
					>
						<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex flex-col gap-3">
			<CustomValueInput
				bind:value={modals.join.leaderAPI}
				placeholder="Leader API"
				classes="flex-1 space-y-1.5"
			/>
			<CustomValueInput
				bind:value={modals.join.nodeAddr}
				placeholder="Node Address"
				classes="flex-1 space-y-1.5"
			/>
			<CustomValueInput
				bind:value={modals.join.clusterKey}
				placeholder="Cluster Key"
				classes="flex-1 space-y-1.5"
			/>

			<Button
				onclick={() => {
					handleJoin();
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<Icon icon="oui:ml-create-population-job" class="mr-1 h-4 w-4" />
					<span>Join Cluster</span>
				</div>
			</Button>
		</div>
	</Dialog.Content>
</Dialog.Root>
