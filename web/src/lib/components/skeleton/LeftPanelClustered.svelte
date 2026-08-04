<script lang="ts">
	import { SvelteSet } from 'svelte/reactivity';
	import {
		getClusterResourcesResult,
		getNodesResult
	} from '$lib/api/cluster/cluster';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import { hasSavedOpenIds, loadOpenIds, saveOpenIds } from '$lib/left-panel';
	import {
		buildResourceTree,
		collectResourceTreeIds,
		type ResourceTreeNodeInput,
		type ResourceTreePreferences,
		type ResourceTreeResource,
		type ResourceTreeView
	} from '$lib/resource-tree';
	import { reload } from '$lib/stores/api.svelte';
	import type { ClusterNode, NodeResource } from '$lib/types/cluster/cluster';
	import { default as TreeViewCluster } from './TreeViewCluster.svelte';
	import { DomainState } from '$lib/types/vm/vm';
	import { storage } from '$lib';
	import { resource, watch } from 'runed';
	import { page } from '$app/state';
	import { onDestroy } from 'svelte';
	import { isAPIResponse } from '$lib/utils/http';

	interface ClusterSidebarSnapshot {
		resources: NodeResource[];
		nodes: ClusterNode[];
	}

	interface Props {
		preferences: ResourceTreePreferences;
	}

	let { preferences }: Props = $props();

	const emptyClusterSidebarSnapshot: ClusterSidebarSnapshot = {
		resources: [],
		nodes: []
	};

	let openIdsByView = $state<Record<ResourceTreeView, Set<string>>>({
		server: loadOpenIds('cluster', 'server'),
		folder: loadOpenIds('cluster', 'folder')
	});
	let initializedViews = $state<Record<ResourceTreeView, boolean>>({
		server: false,
		folder: false
	});
	let openIds = $derived(openIdsByView[preferences.view]);

	let trailingRefetchTimer = $state<ReturnType<typeof setTimeout> | null>(null);

	const toggleOpen = (id: string) => {
		const view = preferences.view;
		const nextOpenIds = new SvelteSet(openIdsByView[view]);
		if (nextOpenIds.has(id)) nextOpenIds.delete(id);
		else nextOpenIds.add(id);

		openIdsByView = { ...openIdsByView, [view]: nextOpenIds };
		saveOpenIds(nextOpenIds, 'cluster', view);
	};

	async function refetchClusterResources() {
		await clusterSidebarSnapshot.refetch();
	}

	function scheduleTrailingRefetch() {
		if (trailingRefetchTimer) {
			clearTimeout(trailingRefetchTimer);
		}

		trailingRefetchTimer = setTimeout(() => {
			trailingRefetchTimer = null;
			void refetchClusterResources();
		}, 1200);
	}

	function refreshClusterResources() {
		void refetchClusterResources();
		scheduleTrailingRefetch();
	}

	onDestroy(() => {
		if (trailingRefetchTimer) {
			clearTimeout(trailingRefetchTimer);
		}
	});

	const clusterSidebarSnapshot = resource(
		() => 'cluster-sidebar-snapshot',
		async (_, __, { signal }): Promise<ClusterSidebarSnapshot> => {
			const [resourcesResult, nodesResult] = await Promise.all([
				getClusterResourcesResult(signal),
				getNodesResult(signal)
			]);

			if (isAPIResponse(resourcesResult) || isAPIResponse(nodesResult)) {
				throw new Error('Failed to refresh the cluster sidebar snapshot');
			}

			return {
				resources: resourcesResult,
				nodes: nodesResult
			};
		},
		{
			initialValue: emptyClusterSidebarSnapshot
		}
	);

	let cluster = $derived(clusterSidebarSnapshot.current.resources);
	let nodes = $derived(clusterSidebarSnapshot.current.nodes);
	let nodesById = $derived(new Map(nodes.map((node) => [node.nodeUUID, node])));

	let globalNextGuestId = $derived.by(() => {
		const guestIds = cluster.flatMap((resource) => [
			...(resource.jails ?? []).map((jail) => jail.ctId),
			...(resource.vms ?? []).map((vm) => vm.rid)
		]);

		if (guestIds.length === 0) {
			return 100;
		}

		return Math.max(...guestIds) + 1;
	});

	let treeNodes = $derived.by((): ResourceTreeNodeInput[] => {
		return cluster.map((n) => {
				const nodeLabel = n.hostname || n.nodeUUID;
				const resources: ResourceTreeResource[] = [
					...(n.jails ?? []).map((j) => ({
						id: `jail-${j.ctId}`,
						sortId: j.ctId,
						sortName: j.name,
						resourceId: j.ctId,
						resourceType: 'jail' as const,
						nodeHostname: n.hostname,
						label: `${j.name} (${j.ctId})`,
						icon: 'hugeicons--prison',
						href: `/${nodeLabel}/jail/${j.ctId}/summary`,
						state: (j.state === 'ACTIVE' ? 'active' : 'inactive') as 'active' | 'inactive'
					})),
					...(n.vms ?? []).map((vm) => ({
						id: `vm-${vm.rid}`,
						sortId: vm.rid,
						sortName: vm.name,
						resourceId: vm.rid,
						resourceType: 'vm' as const,
						nodeHostname: n.hostname,
						label: `${vm.name} (${vm.rid})`,
						icon: 'material-symbols--monitor-outline',
						href: `/${nodeLabel}/vm/${vm.rid}/summary`,
						state: (vm.state === DomainState.DomainRunning
							? 'active'
							: vm.state === DomainState.DomainNostate
								? 'orphan'
								: 'inactive') as 'active' | 'inactive' | 'orphan'
					})),
					...(n.jailTemplates ?? []).map((template) => ({
						id: `jail-template-${n.nodeUUID}-${template.id}`,
						sortId: template.id,
						sortName: template.name,
						resourceId: template.id,
						resourceType: 'jail-template' as const,
						nodeHostname: n.hostname,
						label: template.name,
						icon: 'mdi--file-tree-outline'
					})),
					...(n.vmTemplates ?? []).map((template) => ({
						id: `vm-template-${n.nodeUUID}-${template.id}`,
						sortId: template.id,
						sortName: template.name,
						resourceId: template.id,
						resourceType: 'vm-template' as const,
						nodeHostname: n.hostname,
						label: template.name,
						icon: 'mdi--monitor-shimmer'
					}))
				];

				const found = nodesById.get(n.nodeUUID);
				const isOffline = found?.status === 'offline';

				return {
					id: n.nodeUUID,
					label: nodeLabel,
					icon: isOffline ? 'mdi--server-off' : 'fluent--storage-20-filled',
					href: isOffline ? `/inactive-node` : `/${nodeLabel}/summary`,
					resources: isOffline ? [] : resources,
					nextGuestId: globalNextGuestId
				};
			});
	});

	// @wc-ignore
	const tree = $derived(
		buildResourceTree({
			nodes: treeNodes,
			preferences,
			rootIcon: 'ant-design--cluster-outlined',
			nextGuestId: globalNextGuestId
		})
	);

	let resourcesReady = $derived(!clusterSidebarSnapshot.loading);

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle) {
				refreshClusterResources();
			}
		}
	);

	watch(
		() => storage.enabledServices,
		() => {
			refreshClusterResources();
		}
	);

	watch(
		[() => preferences.view, () => tree, () => resourcesReady],
		([view, currentTree, ready]) => {
			if (!ready || currentTree.length === 0 || initializedViews[view]) return;

			if (!hasSavedOpenIds('cluster', view)) {
				const nextOpenIds = new Set(collectResourceTreeIds(currentTree));
				openIdsByView = { ...openIdsByView, [view]: nextOpenIds };
				saveOpenIds(nextOpenIds, 'cluster', view);
			}

			initializedViews = { ...initializedViews, [view]: true };
		}
	);

	watch(
		() => reload.leftPanel,
		(value) => {
			if (value) {
				refreshClusterResources();
				reload.leftPanel = false;
			}
		}
	);

	const activeNodeId = $derived.by(() => {
		const path = page.url.pathname;
		const parts = path.split('/').filter(Boolean);
		const nodeLabel = parts[0];
		const node = cluster.find((n) => (n.hostname || n.nodeUUID) === nodeLabel);

		return node?.nodeUUID ?? null;
	});

	watch(
		() => activeNodeId,
		(nodeId, prevNodeId) => {
			if (nodeId !== prevNodeId) {
				reload.leftPanel = true;
				reload.auditLog = true;
			}
		}
	);
</script>

<div class="flex h-full min-h-0 flex-col px-1.5 pt-1">
	<nav aria-label="sylve-sidebar" class="menu thin-scrollbar h-full min-h-0 w-full">
		<ul class="h-full min-h-0">
			<ScrollArea orientation="both" class="h-full w-full">
				{#each tree as item (item.id)}
					<TreeViewCluster {item} {openIds} onToggleId={toggleOpen} />
				{/each}
			</ScrollArea>
		</ul>
	</nav>
</div>
