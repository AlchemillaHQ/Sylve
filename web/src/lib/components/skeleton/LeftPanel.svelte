<script lang="ts">
	import { SvelteSet } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { getSimpleJails, getSimpleJailTemplates } from '$lib/api/jail/jail';
	import { getSimpleVMs, getSimpleVMTemplates } from '$lib/api/vm/vm';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { storage } from '$lib';
	import { hasSavedOpenIds, loadOpenIds, saveOpenIds } from '$lib/left-panel';
	import { reload } from '$lib/stores/api.svelte';
	import type { SimpleJail, SimpleJailTemplate } from '$lib/types/jail/jail';
	import type { ActiveLifecycleGuest } from '$lib/types/task/lifecycle';
	import { DomainState, type SimpleVm, type SimpleVmTemplate } from '$lib/types/vm/vm';
	import { sameElements } from '$lib/utils/arr';
	import { getEnabledServicesForHostname } from '$lib/utils/enabled-services';
	import { updateCache } from '$lib/utils/http';
	import {
		buildResourceTree,
		collectResourceTreeIds,
		filterResourceTree,
		type ResourceTreePreferences,
		type ResourceTreeResource,
		type ResourceTreeView
	} from '$lib/resource-tree';
	import { onDestroy } from 'svelte';
	import { resource, watch } from 'runed';
	import TreeViewCluster from './TreeViewCluster.svelte';

	interface Props {
		preferences: ResourceTreePreferences;
		searchQuery?: string;
		activeLifecycleGuests?: ActiveLifecycleGuest[];
	}

	let { preferences, searchQuery = '', activeLifecycleGuests = [] }: Props = $props();

	let openIdsByView = $state<Record<ResourceTreeView, Set<string>>>({
		server: loadOpenIds('single', 'server'),
		folder: loadOpenIds('single', 'folder')
	});
	let initializedViews = $state<Record<ResourceTreeView, boolean>>({
		server: false,
		folder: false
	});
	let openIds = $derived(openIdsByView[preferences.view]);

	const toggleOpen = (id: string) => {
		const view = preferences.view;
		const nextOpenIds = new SvelteSet(openIdsByView[view]);
		if (nextOpenIds.has(id)) {
			nextOpenIds.delete(id);
		} else {
			nextOpenIds.add(id);
		}

		openIdsByView = { ...openIdsByView, [view]: nextOpenIds };
		saveOpenIds(nextOpenIds, 'single', view);
	};

	export function expandAll() {
		const view = preferences.view;
		const nextOpenIds = new Set(collectResourceTreeIds(tree));
		openIdsByView = { ...openIdsByView, [view]: nextOpenIds };
		saveOpenIds(nextOpenIds, 'single', view);
	}

	export function collapseAll() {
		const view = preferences.view;
		const nextOpenIds = new Set<string>();
		openIdsByView = { ...openIdsByView, [view]: nextOpenIds };
		saveOpenIds(nextOpenIds, 'single', view);
	}

	let node = $derived.by(() => {
		const routeHost = page.url.pathname.split('/').filter(Boolean)[0] || '';
		if (routeHost && routeHost !== 'datacenter' && routeHost !== 'login') {
			return routeHost;
		}
		return storage.localHostname || storage.hostname || 'default-node';
	});

	let enabledServices = $derived(getEnabledServicesForHostname(node));

	const simpleVMs = resource(
		() => `simple-vm-list-${node}`,
		async (key) => {
			if (!enabledServices.includes('virtualization')) {
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
			if (!enabledServices.includes('jails')) {
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

	const simpleJailTemplates = resource(
		() => `simple-jail-template-list-${node}`,
		async (key) => {
			if (!enabledServices.includes('jails')) {
				return [];
			}

			const result = await getSimpleJailTemplates(node);
			if (Array.isArray(result)) {
				updateCache(key, result);
				return result;
			}

			return [];
		},
		{
			initialValue: [] as SimpleJailTemplate[]
		}
	);

	const simpleVMTemplates = resource(
		() => `simple-vm-template-list-${node}`,
		async (key) => {
			if (!enabledServices.includes('virtualization')) {
				return [];
			}

			const result = await getSimpleVMTemplates(node);
			if (Array.isArray(result)) {
				updateCache(key, result);
				return result;
			}

			return [];
		},
		{
			initialValue: [] as SimpleVmTemplate[]
		}
	);

	let guestResourceIds = $derived.by(() => {
		const jailCTIDs = simpleJails.current.map((jail) => jail.ctId);
		const vmCTIDs = simpleVMs.current.map((vm) => vm.rid);
		return [...jailCTIDs, ...vmCTIDs].sort((a, b) => a - b);
	});

	let nextGuestId = $derived.by(() => {
		if (guestResourceIds.length === 0) {
			return 100;
		}

		return Math.max(...guestResourceIds) + 1;
	});

	let nodeResources = $derived.by((): ResourceTreeResource[] => {
		return [
			...simpleVMs.current.map((vm) => ({
				id: `vm-${vm.rid}`,
				sortId: vm.rid,
				sortName: vm.name,
				resourceId: vm.rid,
				resourceType: 'vm' as const,
				nodeHostname: node,
				label: `${vm.name} (${vm.rid})`,
				icon: 'material-symbols--monitor-outline',
				href: `/${node}/vm/${vm.rid}/summary`,
				state: (vm.state === DomainState.DomainRunning ? 'active' : 'inactive') as
					| 'active'
					| 'inactive'
			})),
			...simpleJails.current.map((jail) => ({
				id: `jail-${jail.ctId}`,
				sortId: jail.ctId,
				sortName: jail.name,
				resourceId: jail.ctId,
				resourceType: 'jail' as const,
				nodeHostname: node,
				label: `${jail.name} (${jail.ctId})`,
				icon: 'hugeicons--prison',
				href: `/${node}/jail/${jail.ctId}/summary`,
				state: (jail.state === 'ACTIVE' ? 'active' : 'inactive') as 'active' | 'inactive'
			})),
			...simpleJailTemplates.current.map((template) => ({
				id: `jail-template-${template.id}`,
				sortId: template.id,
				sortName: template.name,
				resourceId: template.id,
				resourceType: 'jail-template' as const,
				nodeHostname: node,
				label: template.name,
				icon: 'icon-park-outline--prison'
			})),
			...simpleVMTemplates.current.map((template) => ({
				id: `vm-template-${template.id}`,
				sortId: template.id,
				sortName: template.name,
				resourceId: template.id,
				resourceType: 'vm-template' as const,
				nodeHostname: node,
				label: template.name,
				icon: 'mdi--monitor-shimmer'
			}))
		];
	});

	// @wc-ignore
	const tree = $derived(
		buildResourceTree({
			nodes: [
				{
					id: `node-${node}`,
					label: node,
					icon: 'fluent--storage-20-filled',
					href: `/${node}/summary`,
					resources: nodeResources,
					nextGuestId
				}
			],
			preferences,
			rootIcon: 'fa-solid--server',
			nextGuestId
		})
	);

	let displayTree = $derived(searchQuery.trim() ? filterResourceTree(tree, searchQuery) : tree);
	let searching = $derived(searchQuery.trim().length > 0);
	let effectiveOpenIds = $derived(
		searching ? new Set(collectResourceTreeIds(displayTree)) : openIds
	);
	function effectiveToggleOpen(id: string) {
		if (searching) return;
		toggleOpen(id);
	}

	let resourcesReady = $derived(
		!simpleVMs.loading &&
			!simpleJails.loading &&
			!simpleJailTemplates.loading &&
			!simpleVMTemplates.loading
	);

	let trailingRefetchTimer = $state<ReturnType<typeof setTimeout> | null>(null);
	async function refetchPanelResources() {
		await Promise.all([
			simpleVMs.refetch(),
			simpleJails.refetch(),
			simpleJailTemplates.refetch(),
			simpleVMTemplates.refetch()
		]);
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
		() => enabledServices,
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
		[() => preferences.view, () => tree, () => resourcesReady],
		([view, currentTree, ready]) => {
			if (!ready || currentTree.length === 0 || initializedViews[view]) return;

			if (!hasSavedOpenIds('single', view)) {
				const nextOpenIds = new Set(collectResourceTreeIds(currentTree));
				openIdsByView = { ...openIdsByView, [view]: nextOpenIds };
				saveOpenIds(nextOpenIds, 'single', view);
			}

			initializedViews = { ...initializedViews, [view]: true };
		}
	);
</script>

<div class="flex h-full min-h-0 flex-col px-1.5 pt-1">
	<nav aria-label="sylve-sidebar" class="menu thin-scrollbar h-full min-h-0 w-full">
		<ul class="h-full min-h-0">
			<ScrollArea orientation="both" class="h-full w-full">
				{#if searching && displayTree.length === 0}
					<div
						class="text-muted-foreground flex flex-col items-center gap-1.5 px-2 py-8 text-center text-xs"
					>
						<span class="icon-[lucide--search-x] size-5"></span>
						<span>No matches for &ldquo;{searchQuery}&rdquo;</span>
					</div>
				{:else}
					{#each displayTree as item (item.id)}
						<TreeViewCluster
							{item}
							openIds={effectiveOpenIds}
							onToggleId={effectiveToggleOpen}
							{nextGuestId}
							density={preferences.density}
							{activeLifecycleGuests}
						/>
					{/each}
				{/if}
			</ScrollArea>
		</ul>
	</nav>
</div>
