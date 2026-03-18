<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { jailAction } from '$lib/api/jail/jail';
	import { actionVm } from '$lib/api/vm/vm';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { slide } from 'svelte/transition';
	import SidebarElement from './TreeViewCluster.svelte';

	interface SidebarProps {
		id: string;
		label: string;
		icon: string;
		href?: string;
		state?: 'active' | 'inactive';
		resourceId?: number;
		resourceType?: 'vm' | 'jail';
		nodeHostname?: string;
		children?: SidebarProps[];
	}

	interface Props {
		item: SidebarProps;
		openIds: Set<string>;
		onToggleId: (id: string) => void;
	}

	let { item, openIds, onToggleId }: Props = $props();
	let isOpen = $derived(openIds.has(item.id));

	const handleLabelClick = (e: MouseEvent) => {
		e.preventDefault();
		if (item.href) {
			goto(item.href, { replaceState: false, noScroll: false });
		}
	};

	const handleIconClick = (e: MouseEvent) => {
		e.preventDefault();
		e.stopPropagation();
		if (item.children && item.children.length > 0) {
			onToggleId(item.id);
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
	let hasContextMenu = $derived(item.resourceType === 'vm' || item.resourceType === 'jail');
	let lastActiveUrl = $derived.by(() => {
		const segments = activeUrl.split('/');
		return segments[segments.length - 1];
	});

	const handleActionClick = async (action: 'start' | 'reboot' | 'shutdown' | 'stop') => {
		if (item.resourceId === undefined || item.resourceType === undefined) {
			return;
		}

		if (item.resourceType === 'jail') {
			if (action !== 'start' && action !== 'stop') {
				return;
			}
			await jailAction(item.resourceId, action, item.nodeHostname);
		} else {
			await actionVm(item.resourceId, action, item.nodeHostname);
		}

		reload.leftPanel = true;

		console.log(`[cluster-tree] ${action} ${item.resourceType}`, {
			id: item.resourceId,
			hostname: item.nodeHostname
		});
	};
</script>

<li class="w-full">
	{#if hasContextMenu}
		<ContextMenu.Root>
			<ContextMenu.Trigger
				role="button"
				tabindex={0}
				class={`my-0.5 flex w-full cursor-pointer items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}`}
				onclick={handleLabelClick}
				onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleLabelClick(e as any) : null)}
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
						role="button"
						tabindex="0"
						class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5 cursor-pointer`}
						onclick={handleIconClick}
						onkeydown={(e) =>
							e.key === 'Enter' || e.key === ' ' ? handleIconClick(e as any) : null}
					></span>
				{/if}
			</ContextMenu.Trigger>
			<ContextMenu.Content>
				{#if item.resourceType === 'jail'}
					{#if item.state === 'active'}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('stop')}>
							<span class="icon-[mdi--stop] h-4 w-4"></span>
							Stop
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('start')}>
							<span class="icon-[mdi--play] h-4 w-4"></span>
							Start
						</ContextMenu.Item>
					{/if}
				{:else if item.resourceType === 'vm'}
					{#if item.state === 'active'}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('reboot')}>
							<span class="icon-[mdi--restart] h-4 w-4"></span>
							Reboot
						</ContextMenu.Item>
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('shutdown')}>
							<span class="icon-[mdi--power] h-4 w-4"></span>
							Shutdown
						</ContextMenu.Item>
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('stop')}>
							<span class="icon-[mdi--stop] h-4 w-4"></span>
							Stop
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('start')}>
							<span class="icon-[mdi--play] h-4 w-4"></span>
							Start
						</ContextMenu.Item>
					{/if}
				{/if}
			</ContextMenu.Content>
		</ContextMenu.Root>
	{:else}
		<div
			role="button"
			tabindex="0"
			class={`my-0.5 flex w-full cursor-pointer items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}`}
			onclick={handleLabelClick}
			onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleLabelClick(e as any) : null)}
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
					role="button"
					tabindex="0"
					class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5 cursor-pointer`}
					onclick={handleIconClick}
					onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleIconClick(e as any) : null)}
				></span>
			{/if}
		</div>
	{/if}
</li>

{#if isOpen && item.children}
	<ul class="pl-5" transition:slide={{ duration: 200, easing: (t) => t }} style="overflow: hidden;">
		{#each item.children as child (child.id)}
			<SidebarElement item={child} {openIds} {onToggleId} />
		{/each}
	</ul>
{/if}
