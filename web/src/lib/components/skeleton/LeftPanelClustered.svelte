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
	import { default as TreeViewCluster } from './TreeViewCluster.svelte';
	import { DomainState } from '$lib/types/vm/vm';
	import { storage } from '$lib';
	import { resource, useInterval, watch } from 'runed';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';

	let openIds = $state(new Set<string>(['datacenter']));

	const toggleOpen = (id: string) => {
		if (openIds.has(id)) openIds.delete(id);
		else openIds.add(id);
		openIds = new Set(openIds);
		saveOpenIds(openIds);
	};

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
						label: `${j.name} (${j.ctId})`,
						icon: 'hugeicons--prison',
						href: `/${nodeLabel}/jail/${j.ctId}`,
						state: (j.state === 'ACTIVE' ? 'active' : 'inactive') as 'active' | 'inactive'
					})),
					...(n.vms ?? []).map((vm) => ({
						id: `vm-${vm.rid}`,
						sortId: vm.rid,
						label: `${vm.name} (${vm.rid})`,
						icon: 'material-symbols--monitor-outline',
						href: `/${nodeLabel}/vm/${vm.rid}`,
						state: (vm.state === DomainState.DomainRunning ? 'active' : 'inactive') as
							| 'active'
							| 'inactive'
					}))
				].sort((a, b) => a.sortId - b.sortId);

				const found = nodes.current.find((node) => node.nodeUUID === n.nodeUUID);
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

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle) {
				cluster.refetch();
				nodes.refetch();
			}
		}
	);

	watch(
		() => storage.enabledServices,
		() => {
			cluster.refetch();
			nodes.refetch();
		}
	);

	watch(
		() => tree.length,
		(length) => {
			if (length > 0) {
				const storedIds = loadClusterIds();
				if (storedIds.size === 0) {
					openIds = new Set(collectIds(tree));
					saveOpenIds(openIds);
				} else {
					const allCurrentIds = new Set(collectIds(tree));
					openIds = new Set(Array.from(storedIds).filter((id) => allCurrentIds.has(id)));
				}
			}
		}
	);

	watch(
		() => reload.leftPanel,
		(value) => {
			if (value) {
				cluster.refetch();
				nodes.refetch();
				reload.leftPanel = false;
			}
		}
	);

	useInterval(30000, {
		callback: () => {
			if (!storage.idle) {
				reload.leftPanel = true;
			}
		}
	});

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

	watch(
		() => storage.hostname,
		(hostname, prevHostname) => {
			if (!hostname || hostname === prevHostname) {
				return;
			}

			const parts = page.url.pathname.split('/').filter(Boolean);
			if (parts.length === 0) {
				return;
			}

			const currentNodeLabel = parts[0];
			const isNodeRoute = cluster.current.some(
				(node) => (node.hostname || node.nodeUUID) === currentNodeLabel
			);

			if (!isNodeRoute || currentNodeLabel === hostname) {
				return;
			}

			parts[0] = hostname;
			const nextPath = `/${parts.join('/')}`;
			const nextUrl = `${nextPath}${page.url.search}${page.url.hash}`;

			goto(nextUrl, { replaceState: true });
		}
	);
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
