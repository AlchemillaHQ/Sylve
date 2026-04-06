<script lang="ts">
	import {
		deinitWireGuardServer,
		editWireGuardServer,
		getWireGuardServer,
		initWireGuardServer,
		toggleWireGuardServer,
		wireGuardServerPeers
	} from '$lib/api/network/wireguard';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import ExportModal from '$lib/components/custom/Network/WireGuard/Server/Export.svelte';
	import PeerList from '$lib/components/custom/Network/WireGuard/Server/PeerList.svelte';
	import PeerModal from '$lib/components/custom/Network/WireGuard/Server/Peer.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Iface } from '$lib/types/network/iface';
	import type { WireGuardServer, WireGuardServerPeer } from '$lib/types/network/wireguard';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { randomPrivateIPv4Range, randomPrivateIPv6Range } from '$lib/utils/inet';
	import { generateKeypair } from '$lib/utils/network/wireguard';
	import { convertDbTime, formatUptime } from '$lib/utils/time';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import { fade } from 'svelte/transition';

	function slideAndFade(
		node: HTMLElement,
		{ duration = 300, gap = 0 }: { duration?: number; gap?: number } = {}
	) {
		const style = getComputedStyle(node);
		const height = parseFloat(style.height);
		return {
			duration,
			css: (t: number) => `
				overflow: hidden;
				opacity: ${t};
				height: ${t * height}px;
				margin-bottom: ${(t - 1) * gap}px;
			`
		};
	}

	interface Data {
		server: WireGuardServer | APIResponse;
		interfaces: Iface[] | APIResponse;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const serverResource = resource(
		() => 'network-vpn-wireguard-server',
		async (key) => {
			const result = await getWireGuardServer();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.server as WireGuardServer | APIResponse
		}
	);

	useInterval(2000, {
		callback: async () => {
			await serverResource.refetch();
		}
	});

	let server = $derived.by(() => {
		if (isAPIResponse(serverResource.current)) {
			return null;
		}
		return serverResource.current as WireGuardServer;
	});

	let serviceDisabled = $derived.by(() => {
		return (
			isAPIResponse(serverResource.current) &&
			serverResource.current.message === 'wireguard_service_disabled'
		);
	});

	let notInitialized = $derived.by(() => {
		return (
			isAPIResponse(serverResource.current) &&
			serverResource.current.message === 'wireguard_server_not_initialized'
		);
	});

	let serverForm = $state({
		port: 61820,
		addresses: `${randomPrivateIPv4Range()}\n${randomPrivateIPv6Range()}`,
		mtu: 1280,
		privateKey: '',
		allowWireGuardPort: false,
		masqueradeIPv4Interface: '',
		masqueradeIPv6Interface: ''
	});

	const interfaces = $derived(Array.isArray(data.interfaces) ? (data.interfaces as Iface[]) : []);
	const interfaceOptions = $derived([
		{ value: '', label: 'Disabled' },
		...interfaces
			.filter((iface) => iface.name && iface.name !== 'wgs0')
			.map((iface) => ({
				value: iface.name,
				label: iface.description?.trim() ? `${iface.description} (${iface.name})` : iface.name
			}))
	]);

	watch(
		() => server,
		(nextServer, oldServer) => {
			if (!nextServer) {
				return;
			}

			if (
				oldServer &&
				nextServer.port === oldServer.port &&
				nextServer.addresses.join('\n') === oldServer.addresses.join('\n') &&
				nextServer.mtu === oldServer.mtu &&
				nextServer.privateKey === oldServer.privateKey &&
				nextServer.allowWireGuardPort === oldServer.allowWireGuardPort &&
				nextServer.masqueradeIPv4Interface === oldServer.masqueradeIPv4Interface &&
				nextServer.masqueradeIPv6Interface === oldServer.masqueradeIPv6Interface
			) {
				return;
			}

			serverForm.port = nextServer.port;
			serverForm.addresses = nextServer.addresses.join('\n');
			serverForm.mtu = nextServer.mtu;
			serverForm.privateKey = nextServer.privateKey;
			serverForm.allowWireGuardPort = nextServer.allowWireGuardPort ?? false;
			serverForm.masqueradeIPv4Interface = nextServer.masqueradeIPv4Interface ?? '';
			serverForm.masqueradeIPv6Interface = nextServer.masqueradeIPv6Interface ?? '';
		}
	);

	let peerModalOpen = $state(false);
	let peerModalId = $state<number | null>(null);

	let modals = $state({
		deinit: false,
		deletePeer: false
	});

	let targetPeerID = $state(0);
	let exportPeerID = $state<number | null>(null);
	let exportModalOpen = $state(false);

	let exportPeer = $derived.by(() => {
		if (!server || exportPeerID === null) return null;
		return server.peers.find((p) => p.id === exportPeerID) ?? null;
	});

	function splitLines(value: string): string[] {
		return value
			.split('\n')
			.map((item) => item.trim())
			.filter((item) => item.length > 0);
	}

	function openPeerEditor(peer?: WireGuardServerPeer) {
		peerModalId = peer ? peer.id : null;
		peerModalOpen = true;
	}

	function isValidWireGuardKey(key: string): boolean {
		if (!key) return false;
		try {
			const binary = atob(key);
			return binary.length === 32;
		} catch {
			return false;
		}
	}

	async function saveServer() {
		if (serviceDisabled) {
			toast.error('WireGuard service is disabled', { position: 'bottom-center' });
			return;
		}

		const trimmedKey = serverForm.privateKey.trim();
		if (trimmedKey && !isValidWireGuardKey(trimmedKey)) {
			toast.error('Invalid private key - must be a valid 32-byte base64-encoded WireGuard key', {
				position: 'bottom-center'
			});
			return;
		}

		const payload = {
			port: Number(serverForm.port),
			addresses: splitLines(serverForm.addresses),
			mtu: Number(serverForm.mtu),
			privateKey: trimmedKey || undefined,
			allowWireGuardPort: serverForm.allowWireGuardPort,
			masqueradeIPv4Interface: serverForm.masqueradeIPv4Interface || '',
			masqueradeIPv6Interface: serverForm.masqueradeIPv6Interface || ''
		};

		const response = notInitialized
			? await initWireGuardServer(payload)
			: await editWireGuardServer(payload);

		if (response.status === 'success') {
			toast.success(notInitialized ? 'WireGuard server initialized' : 'WireGuard server updated', {
				position: 'bottom-center'
			});
			await serverResource.refetch();
			return;
		}

		handleAPIError(response);
		toast.error(response.message || 'Failed to save WireGuard server', {
			position: 'bottom-center'
		});
	}

	async function toggleServer() {
		const response = await toggleWireGuardServer();
		if (response.status === 'success') {
			toast.success('WireGuard server toggled', { position: 'bottom-center' });
			await serverResource.refetch();
			return;
		}
		handleAPIError(response);
		toast.error(response.message || 'Failed to toggle WireGuard server', {
			position: 'bottom-center'
		});
	}

	async function confirmDeinitServer() {
		const response = await deinitWireGuardServer();
		if (response.status === 'success') {
			toast.success('WireGuard server deinitialized', { position: 'bottom-center' });
			modals.deinit = false;
			await serverResource.refetch();
			return;
		}
		handleAPIError(response);
		toast.error(response.message || 'Failed to deinitialize WireGuard server', {
			position: 'bottom-center'
		});
	}

	async function togglePeer(peerID: number) {
		const response = await wireGuardServerPeers.toggle(peerID);
		if (response.status === 'success') {
			toast.success('Peer toggled', { position: 'bottom-center' });
			await serverResource.refetch();
			return;
		}
		handleAPIError(response);
		toast.error(response.message || 'Failed to toggle peer', { position: 'bottom-center' });
	}

	async function deletePeer() {
		const response = await wireGuardServerPeers.remove(targetPeerID);
		if (response.status === 'success') {
			toast.success('Peer removed', { position: 'bottom-center' });
			modals.deletePeer = false;
			targetPeerID = 0;
			await serverResource.refetch();
			return;
		}

		handleAPIError(response);
		toast.error(response.message || 'Failed to remove peer', { position: 'bottom-center' });
	}
</script>

<div class="space-y-4 p-4">
	{#if serviceDisabled}
		<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
			WireGuard service is disabled. Enable it from System Settings to manage runtime configuration.
		</div>
	{/if}

	{#if !notInitialized}
		<div in:fade={{ duration: 200 }} out:slideAndFade={{ duration: 300, gap: 16 }}>
			<Card.Root>
				<Card.Header class="flex flex-row items-center justify-between">
					<div>
						<Card.Title>
							<!-- Peers -->
							<div class="flex items-center gap-2">
								<span class="icon-[mdi--account-group-outline] h-6 w-6 dark:text-white text-black"
								></span>
								<span>Peers</span>
							</div>
						</Card.Title>
					</div>
					<Button size="sm" onclick={() => openPeerEditor()} disabled={!server || serviceDisabled}>
						<span class="icon-[mdi--plus] mr-1 h-4 w-4"></span>
						Add Peer
					</Button>
				</Card.Header>
				<Card.Content class="space-y-4">
					{#if !server}
						<p class="text-sm text-muted-foreground">
							Initialize the server first to manage peers.
						</p>
					{:else}
						<PeerList
							peers={server.peers}
							onEdit={openPeerEditor}
							onToggle={(id) => void togglePeer(id)}
							onExport={(id) => {
								exportPeerID = id;
								exportModalOpen = true;
							}}
							onDelete={(id) => {
								targetPeerID = id;
								modals.deletePeer = true;
							}}
						/>
					{/if}
				</Card.Content>
			</Card.Root>
		</div>
	{/if}

	<Card.Root class="gap-1 mb-4 pb-4!">
		<Card.Header>
			<Card.Title>
				<div class="flex items-center gap-2">
					<span class="icon icon-[simple-icons--wireguard] h-6 w-6 dark:text-white text-black"
					></span>
					<span>WireGuard Server</span>
				</div>
			</Card.Title>
		</Card.Header>
		<Card.Content class="space-y-3">
			<div class="grid grid-cols-1 gap-4 md:grid-cols-5 md:items-end">
				<CustomValueInput
					label="Listen Port"
					type="number"
					bind:value={serverForm.port}
					placeholder="51820"
				/>
				<CustomValueInput
					label="MTU"
					type="number"
					bind:value={serverForm.mtu}
					placeholder="1420"
				/>
				<div class="md:col-span-3">
					<CustomValueInput
						label="Private Key"
						bind:value={serverForm.privateKey}
						placeholder="Auto-generated if empty"
						type="password"
						revealOnFocus
						topRightButton={{
							icon: 'icon-[mdi--dice-multiple-outline]',
							tooltip: 'Generate new keypair',
							function: async () => {
								const keypair = await generateKeypair();
								return keypair.privateKey;
							}
						}}
					/>
				</div>
			</div>

			<CustomValueInput
				label="Interface Addresses"
				type="textarea"
				bind:value={serverForm.addresses}
				placeholder="10.210.0.1/24"
				textAreaClasses="min-h-20"
				topRightButton={{
					icon: 'icon-[mdi--dice-multiple-outline]',
					tooltip: 'Generate random private addresses',
					function: async () => `${randomPrivateIPv4Range()}\n${randomPrivateIPv6Range()}`
				}}
			/>

			<div class="grid grid-cols-1 gap-3 md:grid-cols-3 md:items-end">
				<SimpleSelect
					label="Allow WireGuard Port"
					options={[
						{ value: 'true', label: 'Enabled' },
						{ value: 'false', label: 'Disabled' }
					]}
					value={serverForm.allowWireGuardPort ? 'true' : 'false'}
					onChange={(value) => (serverForm.allowWireGuardPort = value === 'true')}
				/>
				<SimpleSelect
					label="Masquerade IPv4"
					options={interfaceOptions}
					bind:value={serverForm.masqueradeIPv4Interface}
					onChange={(value) => (serverForm.masqueradeIPv4Interface = value)}
				/>
				<SimpleSelect
					label="Masquerade IPv6"
					options={interfaceOptions}
					bind:value={serverForm.masqueradeIPv6Interface}
					onChange={(value) => (serverForm.masqueradeIPv6Interface = value)}
				/>
			</div>

			<div class="flex items-center justify-between gap-2">
				<div>
					{#if !notInitialized}
						<div class="flex gap-2">
							<Button size="sm" variant="outline" onclick={toggleServer} disabled={serviceDisabled}>
								<span class="icon-[ri--toggle-line] mr-1 h-4 w-4"></span>
								{server?.enabled ? 'Disable Server' : 'Enable Server'}
							</Button>
							<Button
								size="sm"
								variant="outline"
								class="text-red-500"
								onclick={() => {
									modals.deinit = true;
								}}
								disabled={serviceDisabled}
							>
								<span class="icon-[mdi--trash-can-outline] mr-1 h-4 w-4"></span>
								Deinitialize
							</Button>
						</div>
					{/if}
				</div>
				<Button size="sm" onclick={saveServer} disabled={serviceDisabled}>
					<span class="icon-[mdi--content-save-outline] mr-1 h-4 w-4"></span>
					{notInitialized ? 'Initialize' : 'Save'}
				</Button>
			</div>

			{#if server}
				<div class="grid grid-cols-1 gap-2 rounded-md border p-3 text-xs md:grid-cols-4">
					<div>
						<span class="text-muted-foreground block">Status</span>
						<span class={server.enabled ? 'text-green-500' : 'text-red-500'}>
							{server.enabled ? 'Enabled' : 'Disabled'}
						</span>
					</div>
					<div>
						<span class="text-muted-foreground block">Uptime</span>
						<span>{formatUptime(server.uptime || 0)}</span>
					</div>
					<div>
						<span class="text-muted-foreground block">RX / TX</span>
						<span>{formatBytesBinary(server.rx)} / {formatBytesBinary(server.tx)}</span>
					</div>
					<div>
						<span class="text-muted-foreground block">Last Restart</span>
						<span>{convertDbTime(server.restartedAt)}</span>
					</div>
				</div>
			{/if}
		</Card.Content>
	</Card.Root>
</div>

<AlertDialog
	bind:open={modals.deinit}
	names={{ parent: 'WireGuard Server', element: '' }}
	customTitle="This will remove the WireGuard server runtime state and peer records. Continue?"
	actions={{
		onConfirm: async () => {
			await confirmDeinitServer();
		},
		onCancel: () => {
			modals.deinit = false;
		}
	}}
/>

<AlertDialog
	bind:open={modals.deletePeer}
	names={{ parent: 'WireGuard Peer', element: targetPeerID ? String(targetPeerID) : '' }}
	customTitle="This action removes the selected peer. Continue?"
	actions={{
		onConfirm: async () => {
			await deletePeer();
		},
		onCancel: () => {
			modals.deletePeer = false;
		}
	}}
/>

{#if server}
	<PeerModal
		{server}
		bind:open={peerModalOpen}
		id={peerModalId}
		afterChange={async () => {
			await serverResource.refetch();
		}}
	/>
{/if}

{#if server && exportPeer}
	<ExportModal {server} peer={exportPeer} bind:open={exportModalOpen} />
{/if}
