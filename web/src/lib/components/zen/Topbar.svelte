<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { page } from '$app/state';
	import { languageArr, storage } from '$lib';
	import { getJWTClaims, logOut } from '$lib/api/auth';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Sidebar from '$lib/components/ui/sidebar/index.js';
	import { isDemoMode } from '$lib/demo/runtime';
	import { setInterface } from '$lib/stores/interface.svelte';
	import { resolveNodeHostname } from '$lib/utils/enabled-services';
	import { mode, toggleMode } from 'mode-watcher';
	import { loadLocale } from 'wuchale/load-utils';
	import Alerts from './Alerts.svelte';
	import NodeSwitcher from './NodeSwitcher.svelte';
	import { isDatacenterScope } from './sitemap';

	const username = getJWTClaims()?.custom_claims.username;
	let node = $derived(
		resolveNodeHostname(page.url.pathname) || storage.hostname || storage.localHostname || ''
	);
	let datacenter = $derived(isDatacenterScope(page.url.pathname));
</script>

<header class="bg-card flex h-14 shrink-0 items-center gap-2 border-b px-3 md:px-4">
	<Sidebar.Trigger class="size-8 shrink-0" />
	<span class="bg-border mx-1 h-5 w-px shrink-0" aria-hidden="true"></span>
	<NodeSwitcher {node} {datacenter} />

	<div class="ml-auto flex items-center gap-1">
		<Alerts />

		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button variant="ghost" class="h-9 gap-2 px-2" {...props}>
						<span class="icon-[lucide--user] size-4"></span>
						<span class="hidden max-w-40 truncate sm:inline">{username}</span>
						<span class="icon-[lucide--chevron-down] text-muted-foreground size-4"></span>
						<span class="sr-only">Account</span>
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content class="w-56" align="end">
				<DropdownMenu.Item class="cursor-pointer" onclick={toggleMode}>
					<span class="icon-[lucide--sun-moon] size-4"></span>
					<span>Color Theme</span>
					<span class="text-muted-foreground ml-auto text-xs">
						{#if mode.current === 'dark'}Dark{:else}Light{/if}
					</span>
				</DropdownMenu.Item>

				<DropdownMenu.Sub>
					<DropdownMenu.SubTrigger class="cursor-pointer">
						<span class="icon-[lucide--languages] size-4"></span>
						<span>Language</span>
					</DropdownMenu.SubTrigger>
					<DropdownMenu.SubContent class="w-48">
						{#each languageArr as { value, label } (value)}
							<DropdownMenu.CheckboxItem
								class="cursor-pointer"
								checked={storage.language === value}
								onclick={() => {
									storage.language = value;
									loadLocale(value);
								}}
							>
								{label}
							</DropdownMenu.CheckboxItem>
						{/each}
					</DropdownMenu.SubContent>
				</DropdownMenu.Sub>

				<DropdownMenu.Item class="cursor-pointer" onclick={() => setInterface('classic')}>
					<span class="icon-[lucide--arrow-left-right] size-4"></span>
					<span>Classic interface</span>
				</DropdownMenu.Item>

				{#if !isDemoMode}
					<DropdownMenu.Separator />
					<DropdownMenu.Item class="cursor-pointer" onclick={() => logOut()}>
						<span class="icon-[lucide--log-out] size-4"></span>
						<span>Log Out</span>
					</DropdownMenu.Item>
				{/if}
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
</header>
