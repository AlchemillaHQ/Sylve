<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { storage } from '$lib';
	import NodeTreeView from '$lib/components/custom/NodeTreeView.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Resizable from '$lib/components/ui/resizable';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { triggers } from '$lib/utils/keyboard-shortcuts';
	import { shortcut, type ShortcutTrigger } from '@svelte-put/shortcut';
	let openCategories: { [key: string]: boolean } = $state({});
	import { Debounced } from 'runed';

	const toggleCategory = (label: string) => {
		openCategories[label] = !openCategories[label];
	};

	let node = $derived.by(() => {
		let url = page.url.pathname;
		return url.split('/')[1];
	});

	$effect(() => {
		if (node) {
			storage.hostname = node;
		}
	});

	interface NodeItem {
		label: string;
		icon: string;
		href?: string;
		children?: NodeItem[];
	}

	let nodeItems: NodeItem[] = $derived.by(() => {
		const hasDHCP = storage.enabledServices?.includes('dhcp-server');
		const hasSamba = storage.enabledServices?.includes('samba-server');

		if (page.url.pathname.startsWith(`/${node}/vm`)) {
			const vmName = page.url.pathname.split('/')[3];
			return [
				{
					label: 'Summary',
					icon: 'basil--document-outline',
					href: `/${node}/vm/${vmName}/summary`
				},
				{ label: 'Console', icon: 'mdi--monitor', href: `/${node}/vm/${vmName}/console` },
				{ label: 'Storage', icon: 'mdi--storage', href: `/${node}/vm/${vmName}/storage` },
				{ label: 'Hardware', icon: 'ix--hardware-cabinet', href: `/${node}/vm/${vmName}/hardware` },
				{ label: 'Network', icon: 'mdi--network', href: `/${node}/vm/${vmName}/network` },
				{ label: 'Options', icon: 'mdi--settings', href: `/${node}/vm/${vmName}/options` }
			];
		}

		if (page.url.pathname.startsWith(`/${node}/jail`)) {
			const jailName = page.url.pathname.split('/')[3];
			return [
				{
					label: 'Summary',
					icon: 'basil--document-outline',
					href: `/${node}/jail/${jailName}/summary`
				},
				{ label: 'Console', icon: 'mdi--monitor', href: `/${node}/jail/${jailName}/console` },
				{
					label: 'Hardware',
					icon: 'ix--hardware-cabinet',
					href: `/${node}/jail/${jailName}/hardware`
				},
				{ label: 'Network', icon: 'mdi--network', href: `/${node}/jail/${jailName}/network` },
				{
					label: 'Snapshots',
					icon: 'carbon--ibm-cloud-vpc-block-storage-snapshots',
					href: `/${node}/jail/${jailName}/snapshots`
				},
				{ label: 'Options', icon: 'mdi--settings', href: `/${node}/jail/${jailName}/options` }
			];
		}

		return [
			{ label: 'Summary', icon: 'basil--document-outline', href: `/${node}/summary` },
			{ label: 'Notes', icon: 'mdi--notes', href: `/${node}/notes` },
			{ label: 'Terminal', icon: 'mdi--terminal', href: `/${node}/terminal` },
			{
				label: 'Network',
				icon: 'mdi--network',
				children: [
					{ label: 'Objects', icon: 'clarity--objects-solid', href: `/${node}/network/objects` },
					{
						label: 'Interfaces',
						icon: 'carbon--network-interface',
						href: `/${node}/network/interfaces`
					},
					{
						label: 'Switches',
						icon: 'clarity--network-switch-line',
						children: [
							{
								label: 'Manual',
								icon: 'streamline-sharp--router-wifi-network-solid',
								href: `/${node}/network/switches/manual`
							},
							{
								label: 'Standard',
								icon: 'mdi--router-network',
								href: `/${node}/network/switches/standard`
							}
						]
					},
					hasDHCP && {
						label: 'DHCP & DNS',
						icon: 'solar--server-path-bold',
						children: [
							{ label: 'Ranges', icon: 'memory--range', href: `/${node}/network/dhcp/ranges` },
							{
								label: 'Leases',
								icon: 'mdi--clipboard-list',
								href: `/${node}/network/dhcp/leases`
							},
							{ label: 'Config', icon: 'mdi--cog-outline', href: `/${node}/network/dhcp/config` }
						]
					}
				].filter(Boolean) as NodeItem[]
			},

			{
				label: 'Storage',
				icon: 'mdi--storage',
				children: [
					{ label: 'Explorer', icon: 'bxs--folder-open', href: `/${node}/storage/explorer` },
					{ label: 'Disks', icon: 'mdi--harddisk', href: `/${node}/storage/disks` },
					{
						label: 'ZFS',
						icon: 'file-icons--openzfs',
						children: [
							// Turned off dashboard for now
							// {
							// 	label: 'Dashboard',
							// 	icon: 'mdi--monitor-dashboard',
							// 	href: `/${node}/storage/zfs/dashboard`
							// },
							{ label: 'Pools', icon: 'bi--hdd-stack-fill', href: `/${node}/storage/zfs/pools` },
							{
								label: 'Datasets',
								icon: 'material-symbols--dataset',
								children: [
									{
										label: 'File Systems',
										icon: 'eos-icons--file-system',
										href: `/${node}/storage/zfs/datasets/fs`
									},
									{
										label: 'Volumes',
										icon: 'carbon--volume-block-storage',
										href: `/${node}/storage/zfs/datasets/volumes`
									},
									{
										label: 'Snapshots',
										icon: 'carbon--ibm-cloud-vpc-block-storage-snapshots',
										href: `/${node}/storage/zfs/datasets/snapshots`
									}
								]
							}
						]
					},
					hasSamba && {
						label: 'Samba',
						icon: 'material-symbols--smb-share',
						children: [
							{
								label: 'Shares',
								icon: 'mdi--folder-network',
								href: `/${node}/storage/samba/shares`
							},
							{
								label: 'Settings',
								icon: 'mdi--folder-settings-variant',
								href: `/${node}/storage/samba/settings`
							},
							{
								label: 'Audit Logs',
								icon: 'tabler--logs',
								href: `/${node}/storage/samba/audit-logs`
							}
						]
					}
				].filter(Boolean) as NodeItem[]
			},
			{
				label: 'Utilities',
				icon: 'mdi--tools',
				children: [
					{
						label: 'Cloud Init Templates',
						icon: 'mdi--cloud-upload-outline',
						href: `/${node}/utilities/cloud-init`
					},
					{
						label: 'Downloader',
						icon: 'material-symbols--download',
						href: `/${node}/utilities/downloader`
					}
				]
			},

			{
				label: 'Settings',
				icon: 'material-symbols--settings',
				children: [
					{
						label: 'Authentication',
						icon: 'mdi--shield-key',
						children: [
							{
								label: 'Users',
								icon: 'mdi--account',
								href: `/${node}/settings/authentication/users`
							},
							{
								label: 'Groups',
								icon: 'mdi--account-group',
								href: `/${node}/settings/authentication/groups`
							}
						]
					},
					{
						label: 'PCI Passthrough',
						icon: 'eos-icons--hardware-circuit',
						href: `/${node}/settings/device-passthrough`
					},
					{ label: 'System', icon: 'mdi--desktop-classic', href: `/${node}/settings/system` }
				]
			}
		];
	});

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	$effect(() => {
		if (page.url.pathname === `/${node}`) {
			goto(`/${node}/summary`);
		} else if (page.url.pathname.startsWith(`/${node}/vm`)) {
			const rid = page.url.pathname.split('/')[3];
			if (page.url.pathname === `/${node}/vm/${rid}`) {
				goto(`/${node}/vm/${rid}/summary`, { replaceState: true });
			}
		} else if (page.url.pathname.startsWith(`/${node}/jail`)) {
			const jailId = page.url.pathname.split('/')[3];
			if (page.url.pathname === `/${node}/jail/${jailId}`) {
				goto(`/${node}/jail/${jailId}/summary`, { replaceState: true });
			}
		}
	});

	let resizeKey = $state(0);
	let hasInitialized = false;
	let isConsoleRoute = $derived.by(() => page.url.pathname.endsWith('/console'));

	function handleResize() {
		if (hasInitialized) {
			resizeKey++;
		} else {
			hasInitialized = true;
		}
	}
	const debouncedResize = new Debounced(() => resizeKey, 150);
</script>

<svelte:window
	use:shortcut={{
		trigger: triggers as ShortcutTrigger[]
	}}
/>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center justify-between border-b p-2">
		<span>Node — <b>{node}</b></span>
		<div>
			<Button
				size="sm"
				class="h-6"
				onclick={() => window.open('https://discord.gg/bJB826JvXK', '_blank')}
				title="Discord"
			>
				<div class="flex items-center">
					<span class="icon-[lucide--circle-help] mr-2 h-5 w-5"></span>
					<span>Help</span>
				</div>
			</Button>

			<Button
				size="sm"
				class="h-6"
				onclick={() => {
					storage.openAbout = true;
				}}
				title="Sponsor"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--heart] h-5 w-5"></span>
				</div>
			</Button>
		</div>
	</div>

	<Resizable.PaneGroup
		direction="horizontal"
		class="h-full w-full"
		id="main-pane-auto"
		autoSaveId="main-pane-auto-save"
		onLayoutChange={handleResize}
	>
		<Resizable.Pane defaultSize={15}>
			<div class="h-full px-1.5">
				<div class="h-full overflow-y-auto">
					<nav aria-label="Difuse-sidebar" class="menu thin-scrollbar w-full">
						<ul>
							<ScrollArea orientation="both" class="h-full w-full">
								{#each nodeItems as item (item.label)}
									<NodeTreeView {item} onToggle={toggleCategory} bind:this={openCategories} />
								{/each}
							</ScrollArea>
						</ul>
					</nav>
				</div>
			</div>
		</Resizable.Pane>
		<Resizable.Handle withHandle />
		<Resizable.Pane>
			{#key debouncedResize.current}
				<div
					class="h-full w-full"
					class:overflow-hidden={isConsoleRoute}
					class:overflow-auto={!isConsoleRoute}
				>
					{@render children?.()}
				</div>
			{/key}
		</Resizable.Pane>
	</Resizable.PaneGroup>
</div>
