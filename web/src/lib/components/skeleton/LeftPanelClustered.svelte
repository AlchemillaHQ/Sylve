<script lang="ts">
	import {
		collectIds,
		getClusterResources,
		getNodes,
		hasSavedClusterIds,
		loadClusterIds,
		saveOpenIds
	} from '$lib/api/cluster/cluster';
	import { ScrollArea } from '$lib/components/ui/scroll-area';
	import { reload } from '$lib/stores/api.svelte';
	import type { ClusterNode, NodeResource } from '$lib/types/cluster/cluster';
	import { default as TreeViewCluster } from './TreeViewCluster.svelte';
	import { DomainState } from '$lib/types/vm/vm';
	import { storage } from '$lib';
	import { resource, watch } from 'runed';
	import { page } from '$app/state';
	import { onDestroy } from 'svelte';

	let openIds = $state(
		(() => {
			const savedIds = loadClusterIds();
			return savedIds.size > 0 ? savedIds : new Set<string>(['datacenter']);
		})()
	);
	let trailingRefetchTimer = $state<ReturnType<typeof setTimeout> | null>(null);
	let hasInitializedOpenIds = $state(false);

	function isSameSet(a: Set<string>, b: Set<string>): boolean {
		if (a.size !== b.size) {
			return false;
		}

		for (const value of a) {
			if (!b.has(value)) {
				return false;
			}
		}

		return true;
	}

	const toggleOpen = (id: string) => {
		if (openIds.has(id)) openIds.delete(id);
		else openIds.add(id);
		openIds = new Set(openIds);
		saveOpenIds(openIds);
	};

	async function refetchClusterResources() {
		await Promise.all([cluster.refetch(), nodes.refetch()]);
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

	const cluster = resource(
		() => 'cluster-resources',
		async (_, __, { signal }) => {
			const services = storage.enabledServices || [];
			if (!services.includes('virtualization') && !services.includes('jails')) {
				return [];
			}
			return await getClusterResources();
		},
		{
			initialValue: [] as NodeResource[]
		}
	);

	const nodes = resource(
		() => 'cluster-nodes',
		async (_, __, { signal }) => {
			return await getNodes();
		},
		{
			initialValue: [] as ClusterNode[]
		}
	);

	let globalNextGuestId = $derived.by(() => {
		const guestIds = cluster.current.flatMap((resource) => [
			...(resource.jails ?? []).map((jail) => jail.ctId),
			...(resource.vms ?? []).map((vm) => vm.rid)
		]);

		if (guestIds.length === 0) {
			return 100;
		}

		return Math.max(...guestIds) + 1;
	});

	const tree = $derived([
		{
			id: 'datacenter',
			label: 'Data Center',
			icon: 'ant-design--cluster-outlined',
			href: '/datacenter',
			children: cluster.current.map((n) => {
				const nodeLabel = n.hostname || n.nodeUUID;
				let mergedChildren = [
					...(n.jails ?? []).map((j) => ({
						id: `jail-${j.ctId}`,
						sortId: j.ctId,
						resourceId: j.ctId,
						resourceType: 'jail' as const,
						nodeHostname: n.hostname,
						label: `${j.name} (${j.ctId})`,
						icon: 'hugeicons--prison',
						href: `/${nodeLabel}/jail/${j.ctId}`,
						state: (j.state === 'ACTIVE' ? 'active' : 'inactive') as 'active' | 'inactive'
					})),
					...(n.vms ?? []).map((vm) => ({
						id: `vm-${vm.rid}`,
						sortId: vm.rid,
						resourceId: vm.rid,
						resourceType: 'vm' as const,
						nodeHostname: n.hostname,
						label: `${vm.name} (${vm.rid})`,
						icon: 'material-symbols--monitor-outline',
						href: `/${nodeLabel}/vm/${vm.rid}`,
						state: (vm.state === DomainState.DomainRunning ? 'active' : 'inactive') as
							| 'active'
							| 'inactive'
					}))
				].sort((a, b) => a.sortId - b.sortId);

				const templateChildren = (n.jailTemplates ?? [])
					.map((template) => ({
						id: `jail-template-${n.nodeUUID}-${template.id}`,
						sortId: template.id,
						resourceId: template.id,
						resourceType: 'jail-template' as 'jail-template' | 'vm-template',
						nodeHostname: n.hostname,
						label: template.name,
						icon: 'mdi--file-tree-outline'
					}))
					.concat(
						(n.vmTemplates ?? []).map((template) => ({
							id: `vm-template-${n.nodeUUID}-${template.id}`,
							sortId: template.id,
							resourceId: template.id,
							resourceType: 'vm-template' as 'jail-template' | 'vm-template',
							nodeHostname: n.hostname,
							label: template.name,
							icon: 'mdi--monitor-shimmer'
						}))
					)
					.sort((a, b) => a.sortId - b.sortId)
					.map(({ sortId: _sortId, ...item }) => item);

				const nodeChildren = [
					...mergedChildren,
					...(templateChildren.length > 0
						? [
								{
									id: `templates-${n.nodeUUID}`,
									label: 'Templates',
									icon: 'mdi--layers-outline',
									children: templateChildren
								}
							]
						: [])
				];

				const found = nodes.current.find((node) => node.nodeUUID === n.nodeUUID);
				const isActive = found && found.status === 'online';

				return {
					id: n.nodeUUID,
					label: nodeLabel,
					icon: isActive ? 'fluent--storage-20-filled' : 'mdi--server-off',
					href: isActive ? `/${nodeLabel}` : `/inactive-node`,
					children: isActive ? nodeChildren : [],
					nextGuestId: globalNextGuestId
				};
			})
		}
	]);

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
		() => tree,
		(currentTree) => {
			if (currentTree.length > 0) {
				const hasClusterNodes = cluster.current.length > 0;
				const allCurrentIds = new Set(collectIds(currentTree));

				if (!hasInitializedOpenIds) {
					const hasSavedIds = hasSavedClusterIds();

					// Wait for cluster data before pruning saved IDs to avoid collapsing everything on refresh.
					if (hasSavedIds && !hasClusterNodes) {
						return;
					}

					if (!hasSavedIds) {
						openIds = new Set(allCurrentIds);
						saveOpenIds(openIds);
					} else {
						const storedIds = loadClusterIds();
						openIds = new Set(Array.from(storedIds).filter((id) => allCurrentIds.has(id)));
						if (!isSameSet(openIds, storedIds)) {
							saveOpenIds(openIds);
						}
					}

					hasInitializedOpenIds = true;
					return;
				}

				if (!hasClusterNodes) {
					return;
				}

				const filteredIds = new Set(Array.from(openIds).filter((id) => allCurrentIds.has(id)));
				if (!isSameSet(filteredIds, openIds)) {
					openIds = filteredIds;
					saveOpenIds(openIds);
				}
			}
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
		const node = cluster.current.find((n) => (n.hostname || n.nodeUUID) === nodeLabel);

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
