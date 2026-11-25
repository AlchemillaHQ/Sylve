<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { afterNavigate } from '$app/navigation';
	import { page } from '$app/state';
	import { storage } from '$lib';
	import SylveIcon from '$lib/components/custom/SylveIcon.svelte';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Sidebar from '$lib/components/ui/sidebar/index.js';
	import { setInterface } from '$lib/stores/interface.svelte';
	import { isSuperuser, nodePath, visibleItems } from '$lib/stores/permissions.svelte';
	import { resolveNodeHostname } from '$lib/utils/enabled-services';
	import Navigation from './Navigation.svelte';
	import { getDatacenterNavigation, getNodeNavigation, isDatacenterScope } from './sitemap';

	const sidebar = Sidebar.useSidebar();
	const menuButton = 'h-9 gap-2.5 rounded-lg text-[13px]';

	let node = $derived(
		resolveNodeHostname(page.url.pathname) || storage.hostname || storage.localHostname || ''
	);
	let datacenter = $derived(isDatacenterScope(page.url.pathname));
	let items = $derived(
		visibleItems(
			datacenter
				? getDatacenterNavigation()
				: getNodeNavigation(node, nodePath(node), isSuperuser())
		)
	);

	afterNavigate(() => sidebar.setOpenMobile(false));
</script>

<Sidebar.Root collapsible="icon">
	<Sidebar.Header class="h-14 justify-center border-b px-3 py-0">
		<a
			href={datacenter ? '/datacenter/summary' : `/${node}/summary`}
			class="flex h-9 items-center justify-center gap-3 rounded-lg px-2"
			aria-label="Sylve home"
		>
			<span class="flex size-7 shrink-0 items-center justify-center">
				<SylveIcon class="size-7 shrink-0" />
			</span>
			<!-- @wc-ignore -->
			<span class="text-lg font-medium tracking-[.3em] group-data-[collapsible=icon]:hidden"
				>SYLVE</span
			>
		</a>
	</Sidebar.Header>

	<Sidebar.Content class="overflow-hidden">
		<ScrollArea
			class="h-full min-h-0 w-full flex-1"
			fade
			fadeClasses="from-sidebar"
			scrollbarYClasses="hidden"
			viewportProps={{
				class: 'overscroll-contain',
				role: 'region',
				'aria-label': 'Sidebar navigation',
				tabindex: 0
			}}
		>
			<Sidebar.Group class="p-3">
				<Navigation
					{items}
					pathname={page.url.pathname}
					label={datacenter ? 'Datacenter navigation' : 'Node navigation'}
				/>
			</Sidebar.Group>
		</ScrollArea>
	</Sidebar.Content>

	<Sidebar.Footer class="border-t px-3 py-3">
		<Sidebar.Menu>
			<Sidebar.MenuItem>
				<Sidebar.MenuButton
					tooltipContent="Documentation"
					aria-label="Documentation"
					class={menuButton}
				>
					{#snippet child({ props })}
						<a href="https://sylve.io/docs" target="_blank" rel="noreferrer" {...props}>
							<span class="icon-[lucide--book-open] size-4 shrink-0"></span>
							<span class="group-data-[collapsible=icon]:hidden">Documentation</span>
						</a>
					{/snippet}
				</Sidebar.MenuButton>
			</Sidebar.MenuItem>
			<Sidebar.MenuItem>
				<Sidebar.MenuButton
					tooltipContent="About"
					aria-label="About"
					class={menuButton}
					onclick={() => (storage.openAbout = true)}
				>
					<span class="icon-[lucide--heart] size-4 shrink-0"></span>
					<span class="group-data-[collapsible=icon]:hidden">About</span>
				</Sidebar.MenuButton>
			</Sidebar.MenuItem>
			<Sidebar.MenuItem>
				<Sidebar.MenuButton
					tooltipContent="Classic interface"
					aria-label="Classic interface"
					class={menuButton}
					onclick={() => setInterface('classic')}
				>
					<span class="icon-[lucide--arrow-left-right] size-4 shrink-0"></span>
					<span class="group-data-[collapsible=icon]:hidden">Classic interface</span>
				</Sidebar.MenuButton>
			</Sidebar.MenuItem>
		</Sidebar.Menu>
	</Sidebar.Footer>

	<Sidebar.Rail />
</Sidebar.Root>
