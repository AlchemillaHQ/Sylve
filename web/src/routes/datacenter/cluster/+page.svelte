<script lang="ts">
	import { createCluster, getCluster } from '$lib/api/datacenter/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
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

	async function handleJoin() {}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
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
	</div>
</div>
