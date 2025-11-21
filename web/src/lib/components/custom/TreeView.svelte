<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { slide } from 'svelte/transition';
	import SidebarElement from './TreeView.svelte';

	interface SidebarProps {
		label: string;
		icon: string;
		href?: string;
		state?: 'active' | 'inactive';
		children?: SidebarProps[];
	}

	interface Props {
		item: SidebarProps;
		onToggle: (label: string) => void;
		openCategories?: { [key: string]: boolean };
	}

	let { item, onToggle, openCategories = {} }: Props = $props();
	let isOpen = $derived(openCategories[item.label] ?? false);

	const handleLabelClick = (e: MouseEvent) => {
		e.preventDefault();
		if (item.href) {
			goto(item.href, { replaceState: false, noScroll: false });
		}
	};

	const handleIconClick = (e: MouseEvent) => {
		e.preventDefault();
		e.stopPropagation();
		if (item.children) {
			onToggle(item.label);
		}
	};

	const sidebarActive = 'rounded-md bg-muted dark:bg-muted font-inter font-medium';

	function isItemActive(menuItem: SidebarProps, currentUrl: string): boolean {
		if (menuItem.href && currentUrl.startsWith(menuItem.href)) {
			return true;
		}
		if (menuItem.children) {
			return menuItem.children.some((child) => isItemActive(child, currentUrl));
		}
		return false;
	}

	let activeUrl = $derived(page.url.pathname);
	let isActive = $derived(isItemActive(item, activeUrl));
	let lastActiveUrl = $derived.by(() => {
		const segments = activeUrl.split('/');
		return segments[segments.length - 1];
	});

	function isItemOpen(menuItem: SidebarProps, currentUrl: string): boolean {
		if (menuItem.href && currentUrl.startsWith(menuItem.href)) {
			return true;
		}
		if (menuItem.children) {
			return menuItem.children.some((child) => isItemOpen(child, currentUrl));
		}
		return false;
	}
</script>

<li class="w-full">
	<div
		class={`my-0.5 flex w-full cursor-pointer items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? '!text-primary' : ' '}`}
		onclick={handleLabelClick}
	>
		<div class="flex items-center space-x-1 text-sm">
			{#if item.icon === 'material-symbols--monitor-outline' || item.icon === 'hugeicons--prison'}
				<div class="flex items-center space-x-1 text-sm">
					<div class="relative">
						<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
						{#if item.state && item.state === 'active'}
							<div
								class="absolute -right-1 bottom-0.5 flex h-2 w-2 items-center justify-center rounded-full bg-green-500"
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
			<span
				class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5 cursor-pointer`}
				onclick={handleIconClick}
			></span>
		{/if}
	</div>
</li>

{#if isOpen && item.children}
	<ul class="pl-5" transition:slide={{ duration: 200, easing: (t) => t }} style="overflow: hidden;">
		{#each item.children as child (child.label)}
			<SidebarElement item={child} {onToggle} {openCategories} />
		{/each}
	</ul>
{/if}
