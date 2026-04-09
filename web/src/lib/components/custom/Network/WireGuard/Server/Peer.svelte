<script lang="ts">
	import { wireGuardServerPeers } from '$lib/api/network/wireguard';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { WireGuardServer, WireGuardServerPeer } from '$lib/types/network/wireguard';
	import { handleAPIError } from '$lib/utils/http';
	import {
		generateKeypair,
		generateNextClientIPs,
		generatePresharedKey
	} from '$lib/utils/network/wireguard';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';

	interface Props {
		server: WireGuardServer;
		open: boolean;
		id: number | null;
		afterChange: () => void;
	}

	let { server, open = $bindable(), id = null, afterChange }: Props = $props();

	let peer = $derived.by((): WireGuardServerPeer | null => {
		if (id !== null) return server.peers.find((p) => p.id === id) ?? null;
		return null;
	});

	let form = $state({
		name: '',
		enabled: true,
		persistentKeepalive: false,
		privateKey: '',
		publicKey: '',
		preSharedKey: '',
		clientIPs: '',
		routableIPs: '',
		routeIPs: false
	});

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				form = {
					name: peer?.name ?? '',
					enabled: peer?.enabled ?? true,
					persistentKeepalive: peer?.persistentKeepalive ?? false,
					privateKey: peer?.privateKey ?? '',
					publicKey: peer?.publicKey ?? '',
					preSharedKey: peer?.preSharedKey ?? '',
					clientIPs: (peer?.clientIPs ?? []).join('\n'),
					routableIPs: (peer?.routableIPs ?? []).join('\n'),
					routeIPs: peer?.routeIPs ?? false
				};
			}
		}
	);

	function reset() {
		form = {
			name: peer?.name ?? '',
			enabled: peer?.enabled ?? true,
			persistentKeepalive: peer?.persistentKeepalive ?? false,
			privateKey: peer?.privateKey ?? '',
			publicKey: peer?.publicKey ?? '',
			preSharedKey: peer?.preSharedKey ?? '',
			clientIPs: (peer?.clientIPs ?? []).join('\n'),
			routableIPs: (peer?.routableIPs ?? []).join('\n'),
			routeIPs: peer?.routeIPs ?? false
		};
	}

	function close() {
		open = false;
	}

	function splitLines(value: string): string[] {
		return value
			.split('\n')
			.map((s) => s.trim())
			.filter((s) => s.length > 0);
	}

	async function save() {
		if (!form.name.trim()) {
			toast.error('Peer name is required', { position: 'bottom-center' });
			return;
		}

		const clientIPs = splitLines(form.clientIPs);
		if (clientIPs.length === 0) {
			toast.error('At least one client IP is required', { position: 'bottom-center' });
			return;
		}

		const payload = {
			id: id ?? undefined,
			name: form.name.trim(),
			enabled: form.enabled,
			persistentKeepalive: form.persistentKeepalive,
			privateKey: form.privateKey.trim() || undefined,
			preSharedKey: form.preSharedKey.trim() || undefined,
			clientIPs,
			routableIPs: splitLines(form.routableIPs),
			routeIPs: form.routeIPs
		};

		const response =
			id !== null
				? await wireGuardServerPeers.edit(payload)
				: await wireGuardServerPeers.add(payload);

		if (response.status === 'success') {
			toast.success(id !== null ? 'Peer updated' : 'Peer added', { position: 'bottom-center' });
			close();
			afterChange();
			return;
		}

		handleAPIError(response);
		toast.error(response.message || 'Failed to save peer', { position: 'bottom-center' });
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="gap-0 border-border/50 bg-card sm:max-w-137.5">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between text-xl">
				<div class="flex items-center gap-2">
					{#if id !== null}
						<span>Edit Peer - {peer?.name ?? ''}</span>
					{:else}
						<span>Add New Peer</span>
					{/if}
				</div>
				<div class="flex items-center gap-0.5">
					{#if id !== null}
						<Button size="sm" variant="link" title="Reset" class="h-4" onclick={reset}>
							<span class="icon pointer-events-none icon-[radix-icons--reset] size-4"></span>
							<span class="sr-only">Reset</span>
						</Button>
					{/if}
					<Button size="sm" variant="link" class="h-4" title="Close" onclick={close}>
						<span class="icon pointer-events-none icon-[material-symbols--close-rounded] size-5"
						></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
			<Dialog.Description class="text-xs text-muted-foreground">
				Configure identity and routing for a WireGuard peer connection.
			</Dialog.Description>
		</Dialog.Header>

		<Tabs.Root value="basic" class="mt-4 flex h-80 flex-col">
			<Tabs.List class="mb-1 w-full justify-start bg-accent/50">
				<Tabs.Trigger value="basic" class="flex-1 gap-2">
					<span class="icon icon-[ic--outline-shield] size-3.5"></span>
					Basic
				</Tabs.Trigger>
				<Tabs.Trigger value="keys" class="flex-1 gap-2">
					<span class="icon icon-[mynaui--key] size-3.5"></span>
					Keys
				</Tabs.Trigger>
				<Tabs.Trigger value="routing" class="flex-1 gap-2">
					<span class="icon icon-[ph--network-light] size-3.5"></span>
					Routing
				</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="basic" class="flex-1 overflow-y-auto space-y-4">
				<CustomValueInput
					label="Peer Name"
					placeholder="Randy's Phone"
					bind:value={form.name}
					classes="flex-1 space-y-1"
				/>
				<div class="flex items-center space-x-3 rounded-lg border border-border bg-muted/40 p-4">
					<Checkbox bind:checked={form.enabled} class="border-muted-foreground/40 bg-background" />
					<div class="grid gap-1.5 leading-none">
						<span class="text-sm font-medium leading-none">Enabled</span>
						<p class="text-[10px] italic text-muted-foreground">
							Disabled peers cannot connect to the server.
						</p>
					</div>
				</div>
				<div class="flex items-center space-x-3 rounded-lg border border-border bg-muted/40 p-4">
					<Checkbox
						bind:checked={form.persistentKeepalive}
						class="border-muted-foreground/40 bg-background"
					/>
					<div class="grid gap-1.5 leading-none">
						<span class="text-sm font-medium leading-none">Persistent Keepalive</span>
						<p class="text-[10px] italic text-muted-foreground">
							Recommended for peers behind NAT to maintain a stable connection.
						</p>
					</div>
				</div>
			</Tabs.Content>

			<Tabs.Content value="keys" class="flex-1 overflow-y-auto space-y-4">
				<CustomValueInput
					label="Private Key"
					bind:value={form.privateKey}
					type="password"
					revealOnFocus
					placeholder="Leave empty to auto generate"
				/>
				<CustomValueInput
					label="Public Key"
					value={form.publicKey}
					placeholder="Computed from private key"
					disabled
				/>
				<CustomValueInput
					label="Pre-shared Key"
					bind:value={form.preSharedKey}
					type="password"
					revealOnFocus
					placeholder="Optional"
				/>
				<Button
					variant="outline"
					class="w-full gap-2 border-primary/30 bg-transparent hover:bg-primary/10"
					onclick={async () => {
						const keypair = await generateKeypair();
						form.privateKey = keypair.privateKey;
						form.publicKey = keypair.publicKey;
						form.preSharedKey = generatePresharedKey();
					}}
				>
					<span class="icon icon-[oui--generate] size-4 text-muted-foreground"></span>
					Auto Generate All Keys
				</Button>
			</Tabs.Content>

			<Tabs.Content value="routing" class="flex-1 overflow-y-auto space-y-4">
				<CustomValueInput
					label="Client IPs"
					placeholder="10.210.0.2/32"
					bind:value={form.clientIPs}
					type="textarea"
					classes="flex-1 space-y-1"
					textAreaClasses="min-h-20 max-h-20"
					topRightButton={{
						icon: 'icon-[ix--ai]',
						tooltip: 'Auto-generate next available IPs from server subnet',
						function: async () => generateNextClientIPs(server).join('\n')
					}}
				/>
				<CustomValueInput
					label="Routable IPs"
					placeholder="192.168.1.0/24"
					bind:value={form.routableIPs}
					type="textarea"
					classes="flex-1 space-y-1"
					textAreaClasses="min-h-20 max-h-24"
					hint="Optional, one CIDR per line"
				/>
				<CustomCheckbox label="Route above IPs" bind:checked={form.routeIPs} />
			</Tabs.Content>
		</Tabs.Root>

		<Dialog.Footer>
			<div class="flex w-full justify-end pt-2">
				<Button size="sm" class="h-8" onclick={save}>
					{id !== null ? 'Save Changes' : 'Create Peer'}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
