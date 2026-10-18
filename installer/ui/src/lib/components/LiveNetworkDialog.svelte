<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import Combobox from '$lib/components/Combobox.svelte';
	import Globe from '@lucide/svelte/icons/globe';

	let { onApply, onClose }: { onApply: () => void; onClose: () => void } = $props();
	type Interface = { name: string; mac: string; addresses: string[] };
	let interfaces = $state<Interface[]>([]);
	let iface = $state('');
	let address = $state('');
	let gateway = $state('');
	let dns = $state('');
	let loading = $state(true);
	let applying = $state(false);
	let error = $state('');
	let reconnectURL = $state('');
	const options = $derived(interfaces.map((nic) => ({ value: nic.name, label: `${nic.name} · ${nic.mac}` })));
	const valid = $derived(iface !== '' && address.trim() !== '' && gateway.trim() !== '' && dns.trim() !== '');

	onMount(async () => {
		try {
			const res = await fetch('/api/network/live');
			const data = await res.json();
			if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
			interfaces = data.interfaces ?? [];
			iface = data.defaultInterface || interfaces[0]?.name || '';
			address = interfaces.find((nic) => nic.name === iface)?.addresses[0] ?? '';
			gateway = data.gateway ?? '';
			dns = (data.dns ?? []).filter((server: string) => !server.includes(':')).join(', ');
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			loading = false;
		}
	});

	async function apply() {
		if (!valid || applying) return;
		applying = true;
		error = '';
		reconnectURL = `http://${address.trim().split('/')[0]}:8080`;
		try {
			const res = await fetch('/api/network/live', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					interface: iface,
					address: address.trim(),
					gateway: gateway.trim(),
					dns: dns.split(/[\s,]+/).filter(Boolean)
				})
			});
			const data = await res.json();
			if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
			onApply();
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			applying = false;
		}
	}
</script>

<Dialog.Root open onOpenChange={(value) => { if (!value && !applying) onClose(); }}>
	<Dialog.Content class="max-h-[90vh] gap-3 overflow-y-auto p-5 sm:max-w-lg" showCloseButton={false} escapeKeydownBehavior={applying ? 'ignore' : 'close'}>
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2 text-base"><Globe class="size-4" /> Live Network</Dialog.Title>
			<Dialog.Description class="text-xs">
				Temporary static IPv4 for this installer session only. Configure the installed system separately in the Network step.
			</Dialog.Description>
		</Dialog.Header>
		<form class="space-y-3" onsubmit={(event) => { event.preventDefault(); apply(); }}>
			<fieldset class="space-y-3" disabled={loading || applying}>
				<div class="space-y-1">
					<Label for="live-interface">Interface</Label>
					<Combobox id="live-interface" bind:value={iface} {options} placeholder={loading ? 'Loading interfaces…' : 'Select an interface'} />
				</div>
				<div class="grid gap-3 sm:grid-cols-2">
					<div class="space-y-1">
						<Label for="live-address">IPv4 Address (CIDR)</Label>
						<Input id="live-address" bind:value={address} placeholder="192.168.1.20/24" class="h-9" required />
					</div>
					<div class="space-y-1">
						<Label for="live-gateway">Gateway</Label>
						<Input id="live-gateway" bind:value={gateway} placeholder="192.168.1.1" class="h-9" required />
					</div>
				</div>
				<div class="space-y-1">
					<Label for="live-dns">DNS Servers</Label>
					<Input id="live-dns" bind:value={dns} placeholder="1.1.1.1, 9.9.9.9" class="h-9" required />
				</div>
			</fieldset>
			<p class="text-xs text-muted-foreground">DHCP stops on the selected interface. Rebooting the live media restores DHCP.</p>
			{#if error}
				<p role="alert" class="text-xs text-destructive">{error}</p>
				{#if reconnectURL}
					<p class="text-xs text-muted-foreground">If remote access disconnected, use the local console or reconnect at {reconnectURL}</p>
				{/if}
			{/if}
			<Dialog.Footer>
				<Button type="button" variant="outline" disabled={applying} onclick={onClose}>Cancel</Button>
				<Button type="submit" disabled={loading || applying || !valid}>{applying ? 'Applying…' : 'Apply and Re-run Checks'}</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
