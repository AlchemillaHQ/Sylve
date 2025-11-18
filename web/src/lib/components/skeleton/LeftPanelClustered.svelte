<script lang="ts">
	import {
		collectIds,
		getClusterResources,
		getNodes,
		loadClusterIds,
		saveOpenIds
	} from '$lib/api/cluster/cluster';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import { reload } from '$lib/stores/api.svelte';
	import type { ClusterNode, NodeResource } from '$lib/types/cluster/cluster';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import { default as TreeViewCluster } from './TreeViewCluster.svelte';

	let openIds = $state(new Set<string>(['datacenter']));

	const toggleOpen = (id: string) => {
		if (openIds.has(id)) openIds.delete(id);
		else openIds.add(id);
		openIds = new Set(openIds);
		saveOpenIds(openIds);
	};

	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: 'cluster-resources',
			queryFn: async () => await getClusterResources(),
			keepPreviousData: true,
			refetchInterval: 30000,
			initialData: [] as NodeResource[],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'cluster-nodes',
			queryFn: async () => await getNodes(),
			keepPreviousData: true,
			refetchInterval: 30000,
			initialData: [] as ClusterNode[],
			refetchOnMount: 'always'
		}
	]);

	const clusterRes = $derived(($results[0]?.data as NodeResource[]) ?? []);
	const nodes = $derived(($results[1]?.data as ClusterNode[]) ?? []);

	const tree = $derived([
		{
			id: 'datacenter',
			label: 'Data Center',
			icon: 'ant-design--cluster-outlined',
			href: '/datacenter',
			children: clusterRes.map((n) => {
				const nodeLabel = n.hostname || n.nodeUUID;
				let mergedChildren = [
					...(n.jails ?? []).map((j) => ({
						id: `jail-${j.ctId}`,
						sortId: j.ctId,
						label: `${j.name} (${j.ctId})`,
						icon: 'hugeicons--prison',
						href: `/${nodeLabel}/jail/${j.ctId}`,
						state: (j.state === 'ACTIVE' ? 'active' : 'inactive') as 'active' | 'inactive'
					})),
					...(n.vms ?? []).map((vm) => ({
						id: `vm-${vm.vmId}`,
						sortId: vm.vmId,
						label: `${vm.name} (${vm.vmId})`,
						icon: 'material-symbols--monitor-outline',
						href: `/${nodeLabel}/vm/${vm.vmId}`,
						state: (vm.state === 'ACTIVE' ? 'active' : 'inactive') as 'active' | 'inactive'
					}))
				].sort((a, b) => a.sortId - b.sortId);

				const found = nodes.find((node) => node.nodeUUID === n.nodeUUID);
				const isActive = found && found.status === 'online';

				return {
					id: n.nodeUUID,
					label: nodeLabel,
					icon: isActive ? 'mdi--server' : 'mdi--server-off',
					href: isActive ? `/${nodeLabel}` : `/inactive-node`,
					children: isActive ? mergedChildren : []
				};
			})
		}
	]);

	$effect(() => {
		if (tree && tree.length > 0) {
			const storedIds = loadClusterIds();
			if (storedIds.size === 0) {
				openIds = new Set(collectIds(tree));
				saveOpenIds(openIds);
			} else {
				const allNodeIds = new Set(collectIds(tree));
				openIds = new Set(Array.from(storedIds).filter((id) => allNodeIds.has(id)));
			}
		}
	});

	$effect(() => {
		if (reload.leftPanel) {
			queryClient.refetchQueries('cluster-resources');
			reload.leftPanel = false;
		}
	});
</script>

<div class="h-full overflow-y-auto px-1.5 pt-1">
	<nav aria-label="sylve-sidebar" class="menu thin-scrollbar w-full">
		<ul>
			<ScrollArea orientation="both" class="h-full w-full">
				{#each tree as item (item.id)}
					<TreeViewCluster {item} {openIds} onToggleId={toggleOpen} />
				{/each}
			</ScrollArea>
		</ul>
	</nav>
</div>
