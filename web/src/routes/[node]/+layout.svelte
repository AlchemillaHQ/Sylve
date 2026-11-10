<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { storage } from '$lib';
	import NotificationBell from '$lib/components/custom/Notifications/Bell.svelte';
	import TreeView from '$lib/components/custom/TreeView.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Resizable from '$lib/components/ui/resizable';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import {
		getEnabledServicesForHostname,
		loadEnabledServicesForHostname,
		syncActiveEnabledServices
	} from '$lib/utils/enabled-services';
	import { watch } from 'runed';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import { getGuestSections, type GuestSection } from '$lib/guest-navigation';
	import { isSuperuser, nodePath, visibleItems, type Need } from '$lib/stores/permissions.svelte';

	let node = $derived(page.url.pathname.split('/')[1] || '');

	watch(
		() => node,
		(curr, prev) => {
			if (!curr || curr === prev) {
				return;
			}

			storage.hostname = curr;
			syncActiveEnabledServices(`/${curr}`);
			void loadEnabledServicesForHostname(curr);
		}
	);

	interface NodeItem {
		label: string;
		icon: string;
		href?: string;
		children?: NodeItem[];
		need?: Need;
	}

	let nodeItems: NodeItem[] = $derived.by(() => {
		const enabledServices = getEnabledServicesForHostname(node);
		const hasDHCP = enabledServices.includes('dhcp-server');
		const hasSamba = enabledServices.includes('samba-server');
		const hasFirewall = enabledServices.includes('firewall');
		const hasWireGuard = enabledServices.includes('wireguard');
		const hasIscsi = enabledServices.includes('iscsi');
		const hasNfs = enabledServices.includes('nfs');
		const hasMdns = enabledServices.includes('mdns');
		const hasContainers = enabledServices.includes('containers');

		const guestType = page.params.rid
			? 'vm'
			: page.params.ctid
				? 'jail'
				: page.params.ocid
					? 'container'
					: null;
		if (guestType) {
			const guestId = page.params.rid ?? page.params.ctid ?? page.params.ocid;
			const jailType =
				guestType === 'jail' && page.data.node === node && page.data.ctId === Number(guestId)
					? page.data.jail?.type
					: undefined;
			const sections: Record<GuestSection, Pick<NodeItem, 'label' | 'icon'>> = {
				summary: { label: 'Summary', icon: 'basil--document-outline' },
				console: { label: 'Console', icon: 'mdi--monitor' },
				storage: { label: 'Storage', icon: 'mdi--storage' },
				hardware: { label: 'Hardware', icon: 'ix--hardware-cabinet' },
				network: { label: 'Network', icon: 'mdi--network' },
				packages: { label: 'Packages', icon: 'mdi--package-variant' },
				snapshots: { label: 'Snapshots', icon: 'carbon--ibm-cloud-vpc-block-storage-snapshots' },
				backups: { label: 'Backups', icon: 'mdi--backup-restore' },
				options: { label: 'Options', icon: 'mdi--settings' },
				permissions: { label: 'Permissions', icon: 'mdi--shield-account' }
			};
			const needs: Partial<Record<GuestSection, Need>> = {
				console: ['Guest.Console', `/${guestType}/${guestId}`],
				// Backups are host-level for now.
				backups: ['Sys.Audit', '/'],
				permissions: ['Sys.Audit', '/access']
			};
			return visibleItems(
				getGuestSections(guestType, jailType).map((section) => ({
					...sections[section],
					href: `/${node}/${guestType}/${guestId}/${section}`,
					need: needs[section]
				}))
			);
		}

		const host: Need = ['Sys.Audit', nodePath(node)];
		return visibleItems(
			[
				{ label: 'Summary', icon: 'basil--document-outline', href: `/${node}/summary` },
				{ label: 'Notes', icon: 'mdi--notes', href: `/${node}/notes`, need: host },
				{
					label: 'Terminal',
					icon: 'mdi--terminal',
					href: `/${node}/terminal`,
					need: ['Sys.Console', nodePath(node)]
				},
				{
					label: 'Network',
					icon: 'mdi--network',
					need: host,
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
						hasMdns && {
							label: 'mDNS',
							icon: 'mdi--broadcast',
							children: [
								{
									label: 'Records',
									icon: 'mdi--format-list-bulleted',
									href: `/${node}/network/mdns/records`
								},
								{
									label: 'Settings',
									icon: 'mdi--cog-outline',
									href: `/${node}/network/mdns/settings`
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
						},
						{
							label: 'Global Configuration',
							icon: 'mdi--cog-outline',
							href: `/${node}/network/global-config`
						}
					].filter(Boolean) as NodeItem[]
				},

				{
					label: 'Storage',
					icon: 'mdi--storage',
					need: host,
					children: [
						...(isSuperuser()
							? [{ label: 'Explorer', icon: 'bxs--folder-open', href: `/${node}/storage/explorer` }]
							: []),
						{ label: 'Disks', icon: 'mdi--harddisk', href: `/${node}/storage/disks` },
						{
							label: 'ZFS',
							icon: 'file-icons--openzfs',
							children: [
								{
									label: 'Dashboard',
									icon: 'mdi--monitor-dashboard',
									href: `/${node}/storage/zfs/dashboard`
								},
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
						hasNfs && {
							label: 'NFS',
							icon: 'mdi--folder-network',
							children: [
								{
									label: 'Shares',
									icon: 'mdi--folder-network',
									href: `/${node}/storage/nfs/shares`
								},
								{
									label: 'Settings',
									icon: 'mdi--folder-settings-variant',
									href: `/${node}/storage/nfs/settings`
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
				hasContainers && {
					label: 'Containers',
					icon: 'lucide--container',
					need: host,
					children: [
						{ label: 'Networks', icon: 'mdi--lan', href: `/${node}/containers/networks` },
						{
							label: 'Images',
							icon: 'mdi--layers-triple-outline',
							href: `/${node}/containers/images`
						},
						{
							label: 'Registries',
							icon: 'mdi--key-chain-variant',
							href: `/${node}/containers/registries`
						}
					]
				},
				{
					label: 'Utilities',
					icon: 'mdi--tools',
					need: ['Sys.Audit', '/'],
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
					label: 'Services',
					icon: 'material-symbols--design-services-outline-rounded',
					need: host,
					children: [
						{
							label: 'Certificates',
							icon: 'mdi--certificate-outline',
							href: `/${node}/services/certificates`
						},
						{
							label: 'Dynamic DNS',
							icon: 'mdi--dns',
							href: `/${node}/services/dynamic-dns`
						}
					]
				},
				{
					label: 'Directory Services',
					icon: 'carbon--directory-domain',
					need: host,
					children: [
						hasSamba && {
							label: 'Active Directory',
							icon: 'mdi--server-network',
							href: `/${node}/directory-services/active-directory`
						},
						{
							label: 'Kerberos',
							icon: 'mdi--key-chain',
							children: [
								{
									label: 'Realms',
									icon: 'mdi--earth',
									href: `/${node}/directory-services/kerberos/realms`
								},
								{
									label: 'Keytabs',
									icon: 'mdi--key-chain',
									href: `/${node}/directory-services/kerberos/keytabs`
								},
								{
									label: 'Settings',
									icon: 'mdi--cog-outline',
									href: `/${node}/directory-services/kerberos/settings`
								}
							]
						}
					].filter(Boolean) as NodeItem[]
				},
				{
					label: 'Authentication',
					icon: 'mdi--shield-key',
					need: ['User.Modify', '/access'],
					children: [
						{
							label: 'Users',
							icon: 'mdi--account',
							href: `/${node}/authentication/users/pam`,
							// PAM users are host accounts.
							need: host
						},
						{
							label: 'Unix Groups',
							icon: 'mdi--account-group',
							href: `/${node}/authentication/groups`,
							need: host
						}
					]
				},
				{
					label: 'System',
					icon: 'mdi--desktop-classic',
					need: host,
					children: [
						{
							label: 'Notifications',
							icon: 'mdi--bell-ring-outline',
							need: ['Sys.Audit', '/'],
							children: [
								{
									label: 'Transports',
									icon: 'mdi--swap-horizontal-bold',
									href: `/${node}/system/notifications/transports`
								},
								{
									label: 'Rules',
									icon: 'mdi--format-list-checks',
									href: `/${node}/system/notifications/rules`
								}
							]
						},
						{
							label: 'Updates',
							icon: 'mdi--update',
							children: [
								{
									label: 'Packages',
									icon: 'mdi--package-variant',
									href: `/${node}/system/updates/packages`
								},
								{
									label: 'Sylve',
									icon: 'sylve',
									href: `/${node}/system/updates/sylve`
								},
								{
									label: 'Audit',
									icon: 'mdi--shield-search',
									href: `/${node}/system/updates/audit`
								},
								{
									label: 'History',
									icon: 'mdi--history',
									href: `/${node}/system/updates/history`
								}
							]
						},
						{
							label: 'Services',
							icon: 'material-symbols--design-services-outline-rounded',
							href: `/${node}/system/services`
						},
						{
							label: 'Tunables',
							icon: 'mdi--tune-variant',
							href: `/${node}/system/tunables`
						},
						{
							label: 'PCI Passthrough',
							icon: 'eos-icons--hardware-circuit',
							href: `/${node}/system/device-passthrough`
						}
					]
				}
			].filter(Boolean) as NodeItem[]
		);
	});

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	watch([() => page.url.pathname], ([pathName]) => {
		if (pathName === `/${node}`) {
			useSafeGoto(
				resolve('/[node]/summary', {
					node: node
				})
			);
		} else if (pathName.startsWith(`/${node}/vm`)) {
			const rid = pathName.split('/')[3];
			if (pathName === `/${node}/vm/${rid}`) {
				useSafeGoto(`/${node}/vm/${rid}/summary`, { replaceState: true });
			}
		} else if (pathName.startsWith(`/${node}/jail`)) {
			const ctId = pathName.split('/')[3];
			if (pathName === `/${node}/jail/${ctId}`) {
				useSafeGoto(`/${node}/jail/${ctId}/summary`, { replaceState: true });
			}
		} else if (pathName.startsWith(`/${node}/container/`)) {
			const ocId = pathName.split('/')[3];
			if (pathName === `/${node}/container/${ocId}`) {
				useSafeGoto(`/${node}/container/${ocId}/summary`, { replaceState: true });
			}
		}
	});

	let isConsoleRoute = $derived.by(() => page.url.pathname.endsWith('/console'));
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center justify-between border-b p-2">
		<span>Node — <b>{node}</b></span>
		<div class="flex items-center gap-2">
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
		<!-- A lone Summary is the lock page for users without host access; no menu for that. -->
		{#if nodeItems.length > 1}
			<Resizable.Pane defaultSize={15} order={1}>
				<div class="h-full px-1.5">
					<nav aria-label="Difuse-sidebar" class="menu thin-scrollbar h-full min-h-0 w-full">
						<ul class="h-full min-h-0">
							<ScrollArea
								orientation="both"
								class="h-full w-full"
								scrollbarXClasses="hidden"
								scrollbarYClasses="hidden"
								fade
							>
								{#each nodeItems as item (item.href ?? item.icon)}
									<TreeView {item} />
								{/each}
							</ScrollArea>
						</ul>
					</nav>
				</div>
			</Resizable.Pane>
			<Resizable.Handle withHandle gripPreferenceKey="main-navigation" />
		{/if}
		<Resizable.Pane order={2}>
			{#if isConsoleRoute}
				<div class="h-full w-full overflow-hidden">
					{@render children?.()}
				</div>
			{:else}
				<div class="h-full w-full overflow-y-auto">
					{@render children?.()}
				</div>
			{/if}
		</Resizable.Pane>
	</Resizable.PaneGroup>
</div>
