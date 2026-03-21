<script lang="ts">
	import { page } from '$app/state';
	import { getSimpleJails } from '$lib/api/jail/jail';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { storage } from '$lib';
	import { hasSavedOpenIds, loadOpenIds, saveOpenIds } from '$lib/left-panel';
	import { reload } from '$lib/stores/api.svelte';
	import type { SimpleJail } from '$lib/types/jail/jail';
	import { DomainState, type SimpleVm } from '$lib/types/vm/vm';
	import { sameElements } from '$lib/utils/arr';
	import { updateCache } from '$lib/utils/http';
	import { onDestroy } from 'svelte';
	import { resource, watch } from 'runed';
	import TreeViewCluster from './TreeViewCluster.svelte';

	interface TreeItem {
		id: string;
		label: string;
		icon: string;
		href?: string;
		state?: 'active' | 'inactive';
		resourceId?: number;
		resourceType?: 'vm' | 'jail';
		nodeHostname?: string;
		children?: TreeItem[];
	}

	let openIds = $state(loadOpenIds());
	let hasInitializedOpenIds = $state(false);

	const toggleOpen = (id: string) => {
		if (openIds.has(id)) {
			openIds.delete(id);
		} else {
			openIds.add(id);
		}
		openIds = new Set(openIds);
		saveOpenIds(openIds);
	};

	function collectIds(nodes: TreeItem[]): string[] {
		const ids: string[] = [];
		for (const node of nodes) {
			ids.push(node.id);
			if (node.children && node.children.length > 0) {
				ids.push(...collectIds(node.children));
			}
		}
		return ids;
	}

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

	let node = $derived.by(() => {
		const routeHost = page.url.pathname.split('/').filter(Boolean)[0] || '';
		if (routeHost && routeHost !== 'datacenter' && routeHost !== 'login') {
			return routeHost;
		}
		return storage.hostname || 'default-node';
	});

	const simpleVMs = resource(
		() => `simple-vm-list-${node}`,
		async (key) => {
			if (!storage.enabledServices?.includes('virtualization')) {
				return [];
			}

			const result = await getSimpleVMs(node);
			if (Array.isArray(result)) {
				updateCache(key, result);
				return result;
			}

			return [];
		},
		{
			initialValue: [] as SimpleVm[]
		}
	);

	const simpleJails = resource(
		() => `simple-jail-list-${node}`,
		async (key) => {
			if (!storage.enabledServices?.includes('jails')) {
				return [];
			}

			const result = await getSimpleJails(node);
			if (Array.isArray(result)) {
				updateCache(key, result);
				return result;
			}

			return [];
		},
		{
			initialValue: [] as SimpleJail[]
		}
	);

	let children = $derived.by(() => {
		const merged = [
			...simpleVMs.current.map((vm) => ({
				id: `vm-${vm.rid}`,
				sortId: vm.rid,
				resourceId: vm.rid,
				resourceType: 'vm' as const,
				nodeHostname: node,
				label: `${vm.name} (${vm.rid})`,
				icon: 'material-symbols--monitor-outline',
				href: `/${node}/vm/${vm.rid}`,
				state: (vm.state === DomainState.DomainRunning ? 'active' : 'inactive') as
					| 'active'
					| 'inactive'
			})),
			...simpleJails.current
				.filter((jail) => jail.state?.trim() !== '')
				.map((jail) => ({
					id: `jail-${jail.ctId}`,
					sortId: jail.ctId,
					resourceId: jail.ctId,
					resourceType: 'jail' as const,
					nodeHostname: node,
					label: `${jail.name} (${jail.ctId})`,
					icon: 'hugeicons--prison',
					href: `/${node}/jail/${jail.ctId}`,
					state: (jail.state === 'ACTIVE' ? 'active' : 'inactive') as 'active' | 'inactive'
				}))
		].sort((a, b) => a.sortId - b.sortId);

		return merged.map(({ sortId: _sortId, ...item }) => item);
	}) as TreeItem[];

	// @wc-ignore
	const tree = $derived([
		{
			id: 'datacenter',
			label: 'Data Center',
			icon: 'fa-solid--server',
			href: '/datacenter',
			children: [
				{
					id: `node-${node}`,
					label: node,
					icon: 'fluent--storage-20-filled',
					href: `/${node}`,
					children: children.length > 0 ? children : undefined
				}
			]
		}
	]) as TreeItem[];

	let trailingRefetchTimer = $state<ReturnType<typeof setTimeout> | null>(null);
	async function refetchPanelResources() {
		await Promise.all([simpleVMs.refetch(), simpleJails.refetch()]);
	}

	function scheduleTrailingRefetch() {
		if (trailingRefetchTimer) {
			clearTimeout(trailingRefetchTimer);
		}

		trailingRefetchTimer = setTimeout(() => {
			trailingRefetchTimer = null;
			void refetchPanelResources();
		}, 1200);
	}

	function refreshPanelResources() {
		void refetchPanelResources();
		scheduleTrailingRefetch();
	}

	onDestroy(() => {
		if (trailingRefetchTimer) {
			clearTimeout(trailingRefetchTimer);
		}
	});

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle) {
				refreshPanelResources();
			}
		}
	);

	watch(
		() => storage.enabledServices,
		(enabledServices, prevEnabledServices) => {
			if (sameElements(enabledServices || [], prevEnabledServices || [])) {
				return;
			}

			refreshPanelResources();
		}
	);

	watch(
		() => reload.leftPanel,
		(value) => {
			if (value) {
				refreshPanelResources();
				reload.leftPanel = false;
			}
		}
	);

	watch(
		() => tree,
		(currentTree) => {
			if (currentTree.length === 0) {
				return;
			}

			const allCurrentIds = new Set(collectIds(currentTree));
			if (!hasInitializedOpenIds) {
				if (!hasSavedOpenIds()) {
					openIds = new Set(allCurrentIds);
					saveOpenIds(openIds);
				} else {
					const filteredInitialIds = new Set(
						Array.from(openIds).filter((id) => allCurrentIds.has(id))
					);
					if (!isSameSet(filteredInitialIds, openIds)) {
						openIds = filteredInitialIds;
						saveOpenIds(openIds);
					}
				}

				hasInitializedOpenIds = true;
				return;
			}

			const filtered = new Set(Array.from(openIds).filter((id) => allCurrentIds.has(id)));
			if (!isSameSet(filtered, openIds)) {
				openIds = filtered;
				saveOpenIds(openIds);
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
