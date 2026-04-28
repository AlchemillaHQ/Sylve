<script lang="ts">
	import QRCode from '$lib/components/custom/QRCode.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import Combobox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { WireGuardServer, WireGuardServerPeer } from '$lib/types/network/wireguard';
	import { canvasToPNGDownload } from '$lib/utils/img';
	import { generatePeerConfig, getWireGuardSubnets } from '$lib/utils/network/wireguard';
	import { stringToTextDownload } from '$lib/utils/string';
	import { watch, watchOnce } from 'runed';
	import { onMount } from 'svelte';

	interface Props {
		server: WireGuardServer;
		peer: WireGuardServerPeer;
		open: boolean;
	}

	let { server, peer, open = $bindable() }: Props = $props();

	let allowedIps = $state('');
	let allTraffic = $state(false);
	let keepAlive = $state(false);
	let showQR = $state(true);
	let activeTab = $state('edit');

	let endpointOpen = $state(false);
	let endpointValue = $state('');
	let endpointOptions = $state<{ label: string; value: string }[]>([]);

	let dnsOpen = $state(false);
	let dnsValue = $state(['1.1.1.1']);
	const dnsOptions = [
		{ label: '1.1.1.1 (Cloudflare)', value: '1.1.1.1' },
		{ label: '8.8.8.8 (Google)', value: '8.8.8.8' },
		{ label: '9.9.9.9 (Quad9)', value: '9.9.9.9' },
		{ label: '2606:4700:4700::1111 (Cloudflare IPv6)', value: '2606:4700:4700::1111' },
		{ label: '2001:4860:4860::8888 (Google IPv6)', value: '2001:4860:4860::8888' },
		{ label: '2620:fe::fe (Quad9 IPv6)', value: '2620:fe::fe' }
	];

	onMount(() => {
		const defaultEndpoint = `${window.location.hostname}:${server.port}`;
		endpointOptions = [{ label: defaultEndpoint, value: defaultEndpoint }];
		endpointValue = defaultEndpoint;
	});

	let wireGuardSubnets = $derived(getWireGuardSubnets(server));

	watchOnce(
		() => wireGuardSubnets,
		(val) => {
			if (val.length > 0) {
				allowedIps = val.join('\n');
			}
		}
	);

	// Reset state each time the dialog opens
	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				showQR = true;
				activeTab = 'edit';
				allTraffic = false;
				keepAlive = false;
				allowedIps = wireGuardSubnets.join('\n');
			}
		}
	);

	watch(
		() => allTraffic,
		(val) => {
			if (val) {
				allowedIps = '0.0.0.0/0\n::/0';
			} else {
				const ips = allowedIps
					.split('\n')
					.map((s) => s.trim())
					.filter(Boolean);
				if (ips.length === 2 && ips.includes('0.0.0.0/0') && ips.includes('::/0')) {
					allowedIps = wireGuardSubnets.join('\n');
				}
			}
		}
	);

	let fullConfig = $derived.by(() => {
		const allowedIpsList = allowedIps
			.split('\n')
			.map((s) => s.trim())
			.filter(Boolean);
		return generatePeerConfig(
			server,
			peer,
			dnsValue,
			endpointValue.trim(),
			allowedIpsList,
			keepAlive
		);
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="gap-0 border-border/50 bg-card sm:max-w-137.5"
		showCloseButton={true}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--user-star-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Export Peer - {peer.name}"
				/>
			</Dialog.Title>
			<Dialog.Description class="text-xs text-muted-foreground">
				Review, edit, and export the peer configuration for distribution.
			</Dialog.Description>
		</Dialog.Header>

		<Tabs.Root bind:value={activeTab} class="mt-4">
			<Tabs.List class="w-full justify-start bg-accent/50">
				<Tabs.Trigger value="edit" class="flex-1 gap-2">
					<span class="icon icon-[ic--twotone-edit] size-3.5"></span>
					Edit
				</Tabs.Trigger>
				<Tabs.Trigger
					value="export"
					class="flex-1 gap-2"
					disabled={fullConfig === ''}
					title={fullConfig === '' ? 'Fill in all required fields first' : ''}
				>
					<span class="icon icon-[uil--export] size-3.5"></span>
					Export
				</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="edit" class="space-y-4 pt-2">
				<Combobox
					label="Endpoint Address"
					bind:value={endpointValue}
					bind:open={endpointOpen}
					placeholder="host.example.com:51820"
					triggerWidth="w-full"
					width="w-full"
					allowCustom={true}
					data={endpointOptions}
				/>

				<Combobox
					label="DNS"
					bind:value={dnsValue}
					bind:open={dnsOpen}
					placeholder="1.1.1.1"
					triggerWidth="w-full"
					width="w-full"
					multiple={true}
					data={dnsOptions}
				/>

				<CustomValueInput
					label="Allowed IPs"
					placeholder="10.10.0.0/24&#10;fd00::/64"
					bind:value={allowedIps}
					type="textarea"
					classes="flex-1 space-y-1"
					textAreaClasses="min-h-24 max-h-40"
					topRightButton={{
						icon: 'icon-[mdi--restart]',
						tooltip: 'Reset to WireGuard subnets',
						function: async () => {
							allTraffic = false;
							return wireGuardSubnets.join('\n');
						}
					}}
				/>

				<div class="flex flex-row gap-2">
					<CustomCheckbox label="Route All Traffic" bind:checked={allTraffic} />
					<CustomCheckbox label="Persistent Keepalive (25s)" bind:checked={keepAlive} />
				</div>
			</Tabs.Content>

			<Tabs.Content value="export" class="space-y-4 pt-2">
				<div class="relative min-h-60">
					<div
						class="absolute inset-0 flex flex-col items-center gap-3 transition-opacity duration-200"
						class:opacity-0={!showQR}
						class:pointer-events-none={!showQR}
					>
						<QRCode id="wg-peer-qr" value={fullConfig} size={230} logo="/logo/black.svg" />
					</div>

					<div
						class="absolute inset-0 flex flex-col transition-opacity duration-200"
						class:opacity-0={showQR}
						class:pointer-events-none={showQR}
					>
						<CustomValueInput
							placeholder="[Interface]..."
							bind:value={fullConfig}
							type="textarea"
							classes="flex-1 space-y-1"
							textAreaClasses="min-h-60"
							topRightButton={{
								icon: 'icon-[mdi--content-copy]',
								tooltip: 'Copy configuration',
								function: async () => {
									await navigator.clipboard.writeText(fullConfig);
									return fullConfig;
								}
							}}
						/>
					</div>
				</div>

				<div class="flex w-full flex-col gap-1 -mt-2">
					{#if showQR}
						<Button
							onclick={() => (showQR = false)}
							variant="outline"
							class="w-full gap-2 bg-transparent"
						>
							<span class="icon icon-[lucide--file-text] size-3.5"></span>
							View Configuration Text
						</Button>
					{:else}
						<Button
							onclick={() => (showQR = true)}
							variant="outline"
							class="w-full gap-2 bg-transparent"
						>
							<span class="icon icon-[lucide--qr-code] size-3.5"></span>
							View QR Code
						</Button>
					{/if}

					<div class="flex w-full gap-1">
						<Button
							onclick={() => stringToTextDownload(fullConfig, `WireGuard-${peer.name}.conf`)}
							variant="outline"
							class="flex-1 gap-2 bg-transparent"
						>
							<span class="icon icon-[lucide--download] size-3.5"></span>
							Download Config
						</Button>
						<Button
							onclick={() => canvasToPNGDownload('wg-peer-qr', `WireGuard-${peer.name}.png`)}
							variant="outline"
							class="flex-1 gap-2 bg-transparent"
						>
							<span class="icon icon-[lucide--download] size-3.5"></span>
							Download QR
						</Button>
					</div>
				</div>
			</Tabs.Content>
		</Tabs.Root>
	</Dialog.Content>
</Dialog.Root>
