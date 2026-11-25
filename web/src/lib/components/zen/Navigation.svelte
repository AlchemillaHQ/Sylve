<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import SylveIcon from '$lib/components/custom/SylveIcon.svelte';
	import * as Popover from '$lib/components/ui/popover/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Sidebar from '$lib/components/ui/sidebar/index.js';
	import { PersistedState, watch } from 'runed';
	import { activeAncestors, containsCurrent, isCurrent, type NavigationItem } from './navigation';

	let {
		items,
		pathname,
		label = 'Node navigation'
	}: { items: NavigationItem[]; pathname: string; label?: string } = $props();
	const sidebar = Sidebar.useSidebar();
	const id = $props.id();
	const expanded = new PersistedState<Record<string, boolean>>('sylve-zen-nav-expanded', {});
	const buttonClass = 'h-9 gap-2.5 rounded-lg text-[13px]';
	const activeClass =
		'data-[active=true]:bg-zen-highlight data-[active=true]:text-zen-highlight-foreground';
	let rail = $derived(sidebar.state === 'collapsed' && !sidebar.isMobile);
	let flyout = $state<string | null>(null);

	watch(
		() => pathname,
		() => {
			const ancestors = Object.fromEntries(
				activeAncestors(items, pathname).map((key) => [key, true])
			);
			expanded.current = { ...expanded.current, ...ancestors };
			flyout = null;
		}
	);
	watch(
		() => rail,
		() => {
			flyout = null;
		},
		{ lazy: true }
	);

	function navigate() {
		flyout = null;
		sidebar.setOpenMobile(false);
	}
</script>

{#snippet itemIcon(icon: string | undefined)}
	{#if icon === 'sylve'}
		<SylveIcon class="size-4" />
	{:else if icon}
		<span class="{icon} size-4 shrink-0" aria-hidden="true"></span>
	{/if}
{/snippet}

{#snippet branches(list: NavigationItem[], depth: number)}
	{#each list as item (item.id)}
		{@const current = isCurrent(item, pathname)}
		{@const inBranch = containsCurrent(item, pathname)}
		{@const open = expanded.current[item.id] ?? (inBranch || item.defaultOpen || false)}
		<Sidebar.MenuItem data-nav-depth={depth}>
			{#if item.children?.length}
				{#if depth === 0 && rail}
					<Popover.Root
						open={flyout === item.id}
						onOpenChange={(value) => (flyout = value ? item.id : null)}
					>
						<Popover.Trigger>
							{#snippet child({ props })}
								<Sidebar.MenuButton
									{...props}
									isActive={inBranch}
									aria-label={item.label}
									class="{buttonClass} {activeClass}"
								>
									{@render itemIcon(item.icon)}
								</Sidebar.MenuButton>
							{/snippet}
						</Popover.Trigger>
						<Popover.Content
							side="right"
							align="start"
							sideOffset={12}
							collisionPadding={12}
							class="bg-sidebar text-sidebar-foreground w-60 overflow-hidden p-0"
							aria-label={item.label}
						>
							<ScrollArea
								class="w-full"
								fade
								fadeClasses="from-sidebar"
								scrollbarYClasses="hidden"
								viewportProps={{
									class: 'max-h-(--bits-popover-content-available-height) overscroll-contain',
									role: 'region',
									'aria-label': `${item.label} navigation`,
									tabindex: 0
								}}
							>
								<div class="p-2">
									<p class="text-muted-foreground px-2 py-2 text-xs font-medium">{item.label}</p>
									<Sidebar.Menu>{@render branches(item.children, depth + 1)}</Sidebar.Menu>
								</div>
							</ScrollArea>
						</Popover.Content>
					</Popover.Root>
				{:else}
					<Sidebar.MenuButton
						class="{buttonClass} {inBranch ? 'text-zen-accent' : ''}"
						aria-expanded={open}
						aria-controls="{id}-{item.id}"
						onclick={() => (expanded.current[item.id] = !open)}
					>
						{@render itemIcon(item.icon)}
						<span class="min-w-0 flex-1 truncate">{item.label}</span>
						<span
							class="{open
								? 'icon-[lucide--chevron-down]'
								: 'icon-[lucide--chevron-right]'} text-muted-foreground size-3.5 shrink-0"
							aria-hidden="true"
						></span>
					</Sidebar.MenuButton>
					<ul
						id="{id}-{item.id}"
						class="border-sidebar-border mt-1 ml-2 min-w-0 space-y-1 border-l pl-1.5"
						hidden={!open}
					>
						{@render branches(item.children, depth + 1)}
					</ul>
				{/if}
			{:else if item.href}
				<Sidebar.MenuButton
					isActive={current}
					tooltipContent={depth === 0 ? item.label : undefined}
					aria-label={item.label}
					class="{buttonClass} {activeClass}"
				>
					{#snippet child({ props })}
						<a
							href={item.href}
							aria-current={current ? 'page' : undefined}
							onclick={navigate}
							{...props}
						>
							{@render itemIcon(item.icon)}
							<span class="min-w-0 flex-1 truncate" class:sr-only={depth === 0 && rail}
								>{item.label}</span
							>
						</a>
					{/snippet}
				</Sidebar.MenuButton>
			{:else}
				<Sidebar.MenuButton
					aria-disabled="true"
					aria-label={item.label}
					class="{buttonClass} disabled:opacity-100"
					title="Navigation preview — page not built yet"
				>
					{#snippet child({ props })}
						<button {...props} disabled>
							{@render itemIcon(item.icon)}
							<span class="min-w-0 flex-1 truncate" class:sr-only={depth === 0 && rail}
								>{item.label}</span
							>
						</button>
					{/snippet}
				</Sidebar.MenuButton>
			{/if}
		</Sidebar.MenuItem>
	{/each}
{/snippet}

<nav aria-label={label}>
	<Sidebar.Menu>{@render branches(items, 0)}</Sidebar.Menu>
</nav>
