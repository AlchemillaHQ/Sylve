<script lang="ts">
	import { getSimpleJails } from '$lib/api/jail/jail';
	import { getSimpleVMs, getVMs } from '$lib/api/vm/vm';
	import { default as TreeView } from '$lib/components/custom/TreeView.svelte';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { hostname } from '$lib/stores/basic';
	import type { SimpleJail } from '$lib/types/jail/jail';
	import { DomainState, type SimpleVm, type VM } from '$lib/types/vm/vm';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import { loadOpenCategories, saveOpenCategories } from '$lib/left-panel';

	let openCategories: { [key: string]: boolean } = $state(loadOpenCategories());

	let node = $hostname;

	const toggleCategory = (label: string) => {
		openCategories[label] = !openCategories[label];
		saveOpenCategories(openCategories);
	};

	function initializeOpenCategories(treeItems: any[]) {
		let hasChanges = false;
		treeItems.forEach((item) => {
			if (openCategories[item.label] === undefined) {
				openCategories[item.label] = true;
				hasChanges = true;
			}
			if (item.children) {
				initializeOpenCategories(item.children);
			}
		});
		if (hasChanges) {
			saveOpenCategories(openCategories);
		}
	}

	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: 'simple-vms-list',
			queryFn: async () => {
				return await getSimpleVMs();
			},
			keepPreviousData: true,
			initialData: [] as SimpleVm[],
			refetchOnMount: 'always'
		},
		{
			queryKey: 'simple-jails-list',
			queryFn: async () => {
				return await getSimpleJails();
			},
			keepPreviousData: true,
			initialData: [] as SimpleJail[],
			refetchOnMount: 'always'
		}
	]);

	const simpleVMs = $derived($results[0].data || []);
	const simpleJails = $derived($results[1].data || []);

	let children = $derived(
		[
			...simpleVMs.map((vm) => ({
				id: vm.vmId,
				label: `${vm.name} (${vm.vmId})`,
				icon: 'material-symbols--monitor-outline',
				href: `/${node}/vm/${vm.vmId}`,
				state: vm.state === DomainState.DomainRunning ? 'active' : 'inactive'
			})),
			...simpleJails.map((jail) => ({
				id: jail.ctId,
				label: `${jail.name} (${jail.ctId})`,
				icon: 'hugeicons--prison',
				href: `/${node}/jail/${jail.ctId}`,
				state: jail.state === 'ACTIVE' ? 'active' : 'inactive'
			}))
		].sort((a, b) => a.id - b.id)
	) as {
		id: number;
		label: string;
		icon: string;
		href: string;
		state: 'active' | 'inactive';
		children?: {
			label: string;
			icon: string;
			href: string;
			state: 'active' | 'inactive';
		}[];
	}[];

	const tree = $derived([
		{
			label: 'Data Center',
			icon: 'fa-solid--server',
			href: '/datacenter',
			children: [
				{
					label: node,
					icon: 'mdi--dns',
					href: `/${node}`,
					children: children.length > 0 ? children : undefined
				}
			]
		}
	]);

	$effect(() => {
		if (reload.leftPanel) {
			queryClient.refetchQueries('simple-vms-list');
			queryClient.refetchQueries('simple-jails-list');

			reload.leftPanel = false;
		}
	});

	$effect(() => {
		if (tree && tree.length > 0) {
			initializeOpenCategories(tree);
		}
	});
</script>

<div class="h-full overflow-y-auto px-1.5 pt-1">
	<nav aria-label="sylve-sidebar" class="menu thin-scrollbar w-full">
		<ul>
			<ScrollArea orientation="both" class="h-full w-full">
				{#each tree as item}
					<TreeView {item} onToggle={toggleCategory} {openCategories} />
				{/each}
			</ScrollArea>
		</ul>
	</nav>
</div>
