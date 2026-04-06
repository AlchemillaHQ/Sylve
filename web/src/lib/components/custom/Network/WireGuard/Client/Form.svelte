<script lang="ts">
	import { wireGuardClients, type WireGuardClientRequest } from '$lib/api/network/wireguard';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Accordion from '$lib/components/ui/accordion/index.js';
	import type { WireGuardClient } from '$lib/types/network/wireguard';
	import { handleAPIError } from '$lib/utils/http';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		client: WireGuardClient | null;
		onSaved: () => Promise<void>;
	}

	let { open = $bindable(), client, onSaved }: Props = $props();

	function defaultForm() {
		return {
			name: client?.name ?? '',
			endpointHost: client?.endpointHost ?? '',
			endpointPort: client?.endpointPort ?? 51820,
			listenPort: client?.listenPort ?? 0,
			privateKey: client?.privateKey ?? '',
			peerPublicKey: client?.peerPublicKey ?? '',
			preSharedKey: client?.preSharedKey ?? '',
			allowedIPs: (client?.allowedIPs ?? []).join('\n'),
			addresses: (client?.addresses ?? []).join('\n'),
			routeAllowedIPs: client?.routeAllowedIPs ?? true,
			persistentKeepalive: client?.persistentKeepalive ?? false,
			mtu: client?.mtu ?? 1420,
			metric: client?.metric ?? 0,
			fib: client?.fib ?? 0,
			importedFileName: ''
		};
	}

	let form = $state(defaultForm());
	let fileInput: HTMLInputElement | undefined = $state();

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				form = defaultForm();
			}
		}
	);

	function splitLines(value: string): string[] {
		return value
			.split('\n')
			.map((s) => s.trim())
			.filter((s) => s.length > 0);
	}

	function close() {
		open = false;
	}

	function reset() {
		form = defaultForm();
	}

	async function parseConfigFile(file: File) {
		try {
			const text = await file.text();
			const lines = text.split('\n');
			let section = '';
			for (const line of lines) {
				const trimmed = line.trim();
				if (trimmed.startsWith('#') || trimmed === '') continue;
				if (trimmed.startsWith('[')) {
					section = trimmed.toLowerCase();
				} else if (trimmed.includes('=')) {
					const idx = trimmed.indexOf('=');
					const key = trimmed.slice(0, idx).trim();
					const value = trimmed.slice(idx + 1).trim();
					if (section === '[interface]') {
						if (key === 'PrivateKey') form.privateKey = value;
						if (key === 'Address')
							form.addresses = value
								.split(',')
								.map((ip) => ip.trim())
								.join('\n');
						if (key === 'ListenPort') form.listenPort = Number(value);
						if (key === 'MTU') form.mtu = Number(value);
					} else if (section === '[peer]') {
						if (key === 'PublicKey') form.peerPublicKey = value;
						if (key === 'Endpoint') {
							const lastColon = value.lastIndexOf(':');
							if (lastColon !== -1) {
								form.endpointHost = value.slice(0, lastColon);
								form.endpointPort = Number(value.slice(lastColon + 1));
							}
						}
						if (key === 'AllowedIPs')
							form.allowedIPs = value
								.split(',')
								.map((ip) => ip.trim())
								.join('\n');
						if (key === 'PresharedKey') form.preSharedKey = value;
						if (key === 'PersistentKeepalive') form.persistentKeepalive = value !== '0';
					}
				}
			}
			form.importedFileName = file.name;
			toast.success(`Config imported from ${file.name}`, { position: 'bottom-center' });
		} catch {
			toast.error('Failed to parse config file', { position: 'bottom-center' });
		}
	}

	function onFileChange(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		const ext = file.name.substring(file.name.lastIndexOf('.')).toLowerCase();
		if (ext !== '.conf' && ext !== '.txt') {
			toast.error('Please upload a .conf or .txt file', { position: 'bottom-center' });
			input.value = '';
			return;
		}
		void parseConfigFile(file);
		input.value = '';
	}

	function validate(): boolean {
		if (!form.name.trim()) {
			toast.error('Instance name is required', { position: 'bottom-center' });
			return false;
		}
		if (!form.endpointHost.trim()) {
			toast.error('Remote endpoint host is required', { position: 'bottom-center' });
			return false;
		}
		if (!form.peerPublicKey.trim()) {
			toast.error('Peer public key is required', { position: 'bottom-center' });
			return false;
		}
		if (!form.privateKey.trim()) {
			toast.error('Private key is required', { position: 'bottom-center' });
			return false;
		}
		if (!form.allowedIPs.trim()) {
			toast.error('At least one allowed IP is required', { position: 'bottom-center' });
			return false;
		}
		return true;
	}

	async function save() {
		if (!validate()) return;

		const payload: WireGuardClientRequest = {
			id: client?.id,
			name: form.name.trim(),
			endpointHost: form.endpointHost.trim(),
			endpointPort: Number(form.endpointPort),
			listenPort: Number(form.listenPort) || undefined,
			privateKey: form.privateKey.trim(),
			peerPublicKey: form.peerPublicKey.trim(),
			preSharedKey: form.preSharedKey.trim() || undefined,
			allowedIPs: splitLines(form.allowedIPs),
			addresses: splitLines(form.addresses),
			routeAllowedIPs: form.routeAllowedIPs,
			persistentKeepalive: form.persistentKeepalive,
			mtu: Number(form.mtu) || undefined,
			metric: Number(form.metric) || undefined,
			fib: Number(form.fib) || undefined
		};

		const response = client
			? await wireGuardClients.edit(payload)
			: await wireGuardClients.create(payload);

		if (response.status === 'success') {
			toast.success(client ? `Client "${form.name}" updated` : `Client "${form.name}" created`, {
				position: 'bottom-center'
			});
			close();
			await onSaved();
			return;
		}

		handleAPIError(response);
		toast.error(response.message || 'Failed to save client', { position: 'bottom-center' });
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-h-[90vh] gap-0 overflow-y-auto border-border/50 bg-card sm:max-w-150 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
		showCloseButton={false}
	>
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between text-xl">
				<div class="flex items-center gap-2">
					<span class="icon icon-[mdi--network-outline] size-5 text-primary"></span>
					{client ? `Edit Client - ${client.name}` : 'New Outbound Client'}
				</div>
				<div class="flex items-center gap-0.5">
					{#if client}
						<Button size="sm" variant="link" title="Reset" class="h-4" onclick={reset}>
							<span class="icon pointer-events-none icon-[radix-icons--reset] size-4"></span>
							<span class="sr-only">Reset</span>
						</Button>
					{/if}
					<Button size="sm" variant="link" title="Close" class="h-4" onclick={close}>
						<span class="icon pointer-events-none icon-[material-symbols--close-rounded] size-5"
						></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
			<Dialog.Description class="text-xs text-muted-foreground">
				Configure an independent connection to a remote WireGuard endpoint.
			</Dialog.Description>
		</Dialog.Header>

		<div class="flex flex-col gap-4 py-4">
			<!-- Config file import -->
			<input
				bind:this={fileInput}
				type="file"
				accept=".conf,.txt"
				class="hidden"
				onchange={onFileChange}
			/>
			{#if form.importedFileName}
				<div
					class="flex items-center justify-between rounded-lg border border-border/50 bg-accent/20 p-3"
				>
					<div class="flex items-center gap-3">
						<span class="icon icon-[mdi--file-check] size-5 text-primary"></span>
						<div>
							<p class="text-sm font-medium">{form.importedFileName}</p>
							<p class="text-xs text-muted-foreground">Config auto-populated from file</p>
						</div>
					</div>
					<Button
						variant="ghost"
						size="icon"
						class="hover:text-destructive"
						onclick={() => {
							form.importedFileName = '';
						}}
					>
						<span class="icon icon-[mdi--close] size-4"></span>
					</Button>
				</div>
			{:else}
				<button
					type="button"
					class="flex h-20 w-full cursor-pointer flex-col items-center justify-center gap-1.5 rounded-lg border border-dashed border-border/60 bg-accent/10 text-sm text-muted-foreground transition-colors hover:border-primary/40 hover:bg-accent/20 hover:text-foreground"
					onclick={() => fileInput?.click()}
				>
					<span class="icon icon-[mdi--file-upload-outline] size-5"></span>
					<span>Import .conf file to auto-populate fields</span>
				</button>
			{/if}

			<!-- Basic configuration -->
			<div>
				<h3
					class="mb-3 flex items-center gap-2 text-xs font-semibold uppercase text-muted-foreground"
				>
					<span class="icon icon-[ic--outline-shield] size-3.5"></span>
					Basic Configuration
				</h3>
				<div class="space-y-3">
					<CustomValueInput
						label="Instance Name"
						placeholder="Office — Dubai"
						bind:value={form.name}
						classes="space-y-1"
					/>

					<div class="grid grid-cols-2 gap-3">
						<CustomValueInput
							label="Remote Host"
							placeholder="vpn.example.com"
							bind:value={form.endpointHost}
							classes="space-y-1"
						/>
						<CustomValueInput
							label="Remote Port"
							placeholder="51820"
							type="number"
							bind:value={form.endpointPort}
							classes="space-y-1"
						/>
					</div>

					<div class="grid grid-cols-2 gap-3">
						<CustomValueInput
							label="Peer Public Key"
							placeholder="Peer's public key…"
							bind:value={form.peerPublicKey}
							classes="space-y-1"
						/>
						<CustomValueInput
							label="Your Private Key"
							placeholder="Your private key…"
							type="password"
							revealOnFocus
							bind:value={form.privateKey}
							classes="space-y-1"
						/>
					</div>

					<div class="grid grid-cols-2 gap-3">
						<div class="space-y-1">
							<CustomValueInput
								label="Allowed IPs"
								placeholder="0.0.0.0/0&#10;::/0"
								bind:value={form.allowedIPs}
								type="textarea"
								classes="space-y-1"
								textAreaClasses="min-h-16 max-h-24"
							/>
							<p class="text-[10px] text-muted-foreground">One CIDR per line</p>
						</div>
						<div class="space-y-1">
							<CustomValueInput
								label="Addresses"
								placeholder="10.0.0.2/32&#10;fd00::2/128"
								bind:value={form.addresses}
								type="textarea"
								classes="space-y-1"
								textAreaClasses="min-h-16 max-h-24"
							/>
							<p class="text-[10px] text-muted-foreground">One CIDR per line</p>
						</div>
					</div>

					<CustomCheckbox label="Route Allowed IPs" bind:checked={form.routeAllowedIPs} />
				</div>
			</div>

			<!-- Advanced options via Accordion -->
			<Accordion.Root type="multiple" class="w-full">
				<Accordion.Item value="advanced" class="border-border/40">
					<Accordion.Trigger
						class="flex w-full items-center justify-between py-2 text-xs font-semibold uppercase text-muted-foreground hover:text-foreground"
					>
						<span class="flex items-center gap-2">
							<span class="icon icon-[material-symbols--settings-outline-rounded] size-3.5"></span>
							Advanced Options
						</span>
					</Accordion.Trigger>
					<Accordion.Content class="pt-2">
						<div class="space-y-3">
							<div class="grid grid-cols-3 gap-3">
								<div class="space-y-1">
									<CustomValueInput
										label="Listen Port"
										placeholder="0 (auto)"
										type="number"
										bind:value={form.listenPort}
										classes="space-y-1"
									/>
									<p class="text-[10px] text-muted-foreground">0 = random</p>
								</div>
								<div class="space-y-1">
									<CustomValueInput
										label="MTU"
										placeholder="1420"
										type="number"
										bind:value={form.mtu}
										classes="space-y-1"
									/>
									<p class="text-[10px] text-muted-foreground">Bytes</p>
								</div>
								<CustomValueInput
									label="Interface Metric"
									placeholder="0"
									type="number"
									bind:value={form.metric}
									classes="space-y-1"
								/>
							</div>

							<div class="grid grid-cols-2 gap-3">
								<CustomValueInput
									label="FIB"
									placeholder="0"
									type="number"
									bind:value={form.fib}
									classes="space-y-1"
								/>
								<CustomValueInput
									label="Pre-Shared Key"
									placeholder="Optional PSK…"
									type="password"
									revealOnFocus
									bind:value={form.preSharedKey}
									classes="space-y-1"
								/>
							</div>

							<CustomCheckbox
								label="Persistent Keepalive (25s)"
								bind:checked={form.persistentKeepalive}
							/>
						</div>
					</Accordion.Content>
				</Accordion.Item>
			</Accordion.Root>
		</div>

		<Dialog.Footer>
			<div class="flex w-full justify-end gap-2 pt-1">
				<Button variant="secondary" size="sm" class="h-8" onclick={close}>Cancel</Button>
				<Button size="sm" class="h-8" onclick={save}>
					{client ? 'Save Changes' : 'Initialize Client'}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
