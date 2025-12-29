<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { slide } from 'svelte/transition';
	import SidebarElement from './TreeViewCluster.svelte';

	interface SidebarProps {
		id: string;
		label: string;
		icon: string;
		href?: string;
		state?: 'active' | 'inactive';
		children?: SidebarProps[];
	}

	interface Props {
		item: SidebarProps;
		openIds: Set<string>;
		onToggleId: (id: string) => void;
	}

	let { item, openIds, onToggleId }: Props = $props();

	const toggleExpand = (e: MouseEvent) => {
		if (item.children?.length) onToggleId(item.id);
		e.preventDefault();
		e.stopPropagation();
	};

	const handleNavigation = (e: MouseEvent | KeyboardEvent) => {
		if (item.href) goto(item.href, { replaceState: false, noScroll: false });
		e.preventDefault();
	};

	const sidebarActive = 'rounded-md bg-muted dark:bg-muted font-inter font-medium';

	function isItemActive(menuItem: SidebarProps, currentUrl: string): boolean {
		if (menuItem.href && currentUrl.startsWith(menuItem.href)) return true;
		return menuItem.children?.some((c) => isItemActive(c, currentUrl)) ?? false;
	}

	let activeUrl = $derived(page.url.pathname);
	let isActive = $derived(isItemActive(item, activeUrl));
	let lastActiveUrl = $derived.by(() => {
		const segments = activeUrl.split('/');
		return segments[segments.length - 1];
	});
	let isOpen = $derived(openIds.has(item.id));
</script>

<li class="w-full">
	<div
		role="button"
		tabindex="0"
		onclick={handleNavigation}
		onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleNavigation(e) : null)}
		class={`my-0.5 flex w-full cursor-pointer items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? '!text-primary' : ' '}`}
	>
		<div class="flex items-center space-x-1 text-sm">
			{#if item.icon === 'material-symbols:monitor-outline' || item.icon === 'hugeicons:prison'}
				<div class="flex items-center space-x-1 text-sm">
					<div class="relative">
						<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>

						{#if item.state && item.state === 'active'}
							<div
								class="absolute -right-1 -bottom-1 flex h-2 w-2 items-center justify-center rounded-full bg-green-500"
							>
								<span class="icon-[mdi--play] h-2 w-2 text-white"></span>
							</div>
						{/if}
					</div>
				</div>
			{:else}
				<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
			{/if}
			<p class="font-inter cursor-pointer whitespace-nowrap">
				{item.label}
			</p>
		</div>
		{#if item.children && item.children.length > 0}
			<button
				onclick={toggleExpand}
				class="dark:hover:bg-muted flex cursor-pointer items-center justify-center rounded p-1 hover:bg-slate-200"
			>
				<span
					class={`icon-[${isOpen ? 'teenyicons--down-solid' : 'teenyicons--right-solid'}] h-3.5 w-3.5`}
				></span>
			</button>
		{/if}
	</div>
</li>

{#if isOpen && item.children}
	<ul class="pl-5" transition:slide={{ duration: 200, easing: (t) => t }} style="overflow: hidden;">
		{#each item.children as child (child.id)}
			<SidebarElement item={child} {openIds} {onToggleId} />
		{/each}
	</ul>
{/if}
