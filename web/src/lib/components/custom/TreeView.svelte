<script lang="ts">
	import { page } from '$app/state';
	import { slide } from 'svelte/transition';
	import SidebarElement from './TreeView.svelte';
	import SylveIcon from './SylveIcon.svelte';
	import { watch } from 'runed';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';

	interface SidebarProps {
		label: string;
		icon: string;
		href?: string;
		children?: SidebarProps[];
	}

	interface Props {
		item: SidebarProps;
	}

	let { item }: Props = $props();
	let isOpen = $state(false);

	const toggle = (e: MouseEvent) => {
		e.preventDefault();

		if (item.children) {
			isOpen = !isOpen;
		}

		if (item.href) {
			useSafeGoto(item.href, { replaceState: false, noScroll: false });
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

	// Only the URL resets the open state, so a refreshed item tree keeps the user's toggle.
	watch([() => activeUrl], ([activeUrl]) => {
		isOpen = isItemActive(item, activeUrl);
	});
</script>

<li class="w-full">
	<a
		class={`my-0.5 flex w-full items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}`}
		href={item.href}
		onclick={toggle}
	>
		<div class="flex items-center space-x-1 text-sm">
			{#if item.icon === 'sylve'}
				<SylveIcon class="h-[18px] w-[18px]" />
			{:else}
				<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
			{/if}
			<p class="font-inter cursor-pointer whitespace-nowrap">
				{item.label}
			</p>
		</div>
		{#if item.children && item.children.length > 0}
			<span class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5`}
			></span>
		{/if}
	</a>
</li>

{#if isOpen && item.children}
	<ul class="pl-5" transition:slide={{ duration: 200, easing: (t) => t }} style="overflow: hidden;">
		{#each item.children as child (child.href ?? child.icon)}
			<SidebarElement item={child} />
		{/each}
	</ul>
{/if}
