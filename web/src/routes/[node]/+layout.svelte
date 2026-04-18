<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { storage } from '$lib';
	import NotificationBell from '$lib/components/custom/Notifications/Bell.svelte';
	import NodeTreeView from '$lib/components/custom/NodeTreeView.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Resizable from '$lib/components/ui/resizable';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	let openCategories: { [key: string]: boolean } = $state({});
	import { watch } from 'runed';
	import { fade } from 'svelte/transition';

	const toggleCategory = (label: string) => {
		openCategories[label] = !openCategories[label];
	};

	let node = $derived(page.url.pathname.split('/')[1] || '');

	watch(
		() => node,
		(curr, prev) => {
			if (curr !== prev) {
				storage.hostname = node;
			}
		}
	);

	interface NodeItem {
		label: string;
		icon: string;
		href?: string;
		children?: NodeItem[];
	}

	let nodeItems: NodeItem[] = $derived.by(() => {
		const hasDHCP = storage.enabledServices?.includes('dhcp-server');
		const hasSamba = storage.enabledServices?.includes('samba-server');
		const hasFirewall = storage.enabledServices?.includes('firewall');
		const hasWireGuard = storage.enabledServices?.includes('wireguard');
		const hasIscsi = storage.enabledServices?.includes('iscsi');

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
				{
					label: 'Snapshots',
					icon: 'carbon--ibm-cloud-vpc-block-storage-snapshots',
					href: `/${node}/vm/${vmName}/snapshots`
				},
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
					{
						label: 'Routes',
						icon: 'mdi--routes',
						href: `/${node}/network/routes`
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
					},
					hasFirewall && {
						label: 'Firewall',
						icon: 'mdi--firewall',
						children: [
							{
								label: 'Logs',
								icon: 'mdi--text-box-search-outline',
								href: `/${node}/network/firewall/logs`
							},
							{
								label: 'Traffic Rules',
								icon: 'mdi--transit-connection-horizontal',
								href: `/${node}/network/firewall/traffic`
							},
							{
								label: 'NAT Rules',
								icon: 'mdi--swap-horizontal-bold',
								href: `/${node}/network/firewall/nat`
							},
							{
								label: 'Advanced',
								icon: 'mdi--cog-outline',
								href: `/${node}/network/firewall/advanced`
							}
						]
					},
					hasWireGuard && {
						label: 'VPN',
						icon: 'mdi--vpn',
						children: [
							{
								label: 'WireGuard',
								icon: 'simple-icons--wireguard',
								children: [
									{
										label: 'Server',
										icon: 'mdi--dns',
										href: `/${node}/network/vpn/wireguard/server`
									},
									{
										label: 'Clients',
										icon: 'mdi--account',
										href: `/${node}/network/vpn/wireguard/clients`
									}
								]
							}
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
					},
					hasIscsi && {
						label: 'iSCSI',
						icon: 'carbon--block-storage-alt',
						children: [
							{
								label: 'Initiators',
								icon: 'material-symbols--outbound-outline-rounded',
								href: `/${node}/storage/iscsi/initiators`
							},
							{
								label: 'Targets',
								icon: 'mdi--server',
								href: `/${node}/storage/iscsi/targets`
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
						label: 'System',
						icon: 'mdi--desktop-classic',
						children: [
							{
								label: 'Notifications',
								icon: 'mdi--bell-ring-outline',
								href: `/${node}/settings/system/notifications`
							},
							{
								label: 'Services',
								icon: 'material-symbols--design-services-outline-rounded',
								href: `/${node}/settings/system/services`
							}
						]
					},
					{
						label: 'PCI Passthrough',
						icon: 'eos-icons--hardware-circuit',
						href: `/${node}/settings/device-passthrough`
					},
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
					}
				]
			}
		];
	});

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	watch([() => page.url.pathname], ([pathName]) => {
		if (pathName === `/${node}`) {
			goto(
				resolve('/[node]/summary', {
					node: node
				})
			);
		} else if (pathName.startsWith(`/${node}/vm`)) {
			const rid = pathName.split('/')[3];
			if (pathName === `/${node}/vm/${rid}`) {
				// eslint-disable-next-line svelte/no-navigation-without-resolve
				goto(`/${node}/vm/${rid}/summary`, { replaceState: true });
			}
		} else if (pathName.startsWith(`/${node}/jail`)) {
			const ctId = pathName.split('/')[3];
			if (pathName === `/${node}/jail/${ctId}`) {
				// eslint-disable-next-line svelte/no-navigation-without-resolve
				goto(`/${node}/jail/${ctId}/summary`, { replaceState: true });
			}
		}
	});

	let isConsoleRoute = $derived.by(() => page.url.pathname.endsWith('/console'));
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center justify-between border-b p-2">
		<span>Node — <b>{node}</b></span>
		<div class="flex items-center gap-1">
            <NotificationBell />
			<Button
				size="sm"
				class="h-6"
				onclick={() => window.open('https://sylve.io/docs', '_blank')}
				title="Documentation"
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
			{#if isConsoleRoute}
				<div class="h-full w-full overflow-hidden" transition:fade>
					{@render children?.()}
				</div>
			{:else}
				<div class="h-full w-full overflow-y-auto" transition:fade>
					{@render children?.()}
				</div>
			{/if}
		</Resizable.Pane>
	</Resizable.PaneGroup>
</div>
