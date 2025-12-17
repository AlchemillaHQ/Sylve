<script lang="ts">
	import { getSimpleJails } from '$lib/api/jail/jail';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import { updateCache } from '$lib/utils/http';
	import { default as TreeView } from '$lib/components/custom/TreeView.svelte';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { DomainState, type SimpleVm } from '$lib/types/vm/vm';
	import { loadOpenCategories, saveOpenCategories } from '$lib/left-panel';
	import { storage } from '$lib';
	import { resource } from 'runed';
	import { untrack } from 'svelte';
	import type { SimpleJail } from '$lib/types/jail/jail';

	let openCategories: { [key: string]: boolean } = $state(loadOpenCategories());
	let node = $derived(storage.hostname || 'default-node');

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

	const simpleVMs = resource(
		() => 'simple-vm-list',
		async (key, prevKey, { signal }) => {
			const result = await getSimpleVMs();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: [] as SimpleVm[]
		}
	);

	const simpleJails = resource(
		() => 'simple-jail-list',
		async (key, prevKey, { signal }) => {
			const result = await getSimpleJails();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: [] as SimpleJail[]
		}
	);

	$effect(() => {
		if (storage.visible) {
			untrack(() => {
				simpleVMs.refetch();
				simpleJails.refetch();
			});
		}
	});

	let children = $derived(
		[
			...(simpleVMs.current.map((vm) => ({
				id: vm.rid,
				label: `${vm.name} (${vm.rid})`,
				icon: 'material-symbols--monitor-outline',
				href: `/${node}/vm/${vm.rid}`,
				state: vm.state === DomainState.DomainRunning ? 'active' : 'inactive'
			})) || []),
			...(simpleJails.current.map((jail) => ({
				id: jail.ctId,
				label: `${jail.name} (${jail.ctId})`,
				icon: 'hugeicons--prison',
				href: `/${node}/jail/${jail.ctId}`,
				state: jail.state === 'ACTIVE' ? 'active' : 'inactive'
			})) || [])
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
					icon: 'fluent--storage-20-filled',
					href: `/${node}`,
					children: children.length > 0 ? children : undefined
				}
			]
		}
	]);

	$effect(() => {
		if (reload.leftPanel) {
			console.log('LeftPanel reload triggered');
			simpleVMs.refetch();
			simpleJails.refetch();
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
