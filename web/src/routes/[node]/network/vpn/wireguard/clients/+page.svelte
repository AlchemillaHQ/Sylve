<script lang="ts">
	import { getWireGuardClients, wireGuardClients } from '$lib/api/network/wireguard';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import ClientForm from '$lib/components/custom/Network/WireGuard/Client/Form.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Card from '$lib/components/ui/card/index.js';
	import type { APIResponse } from '$lib/types/common';
	import { type WireGuardClient, wireGuardClientStatus } from '$lib/types/network/wireguard';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, useInterval } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		clients: WireGuardClient[] | APIResponse;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const clientsResource = resource(
		() => 'network-vpn-wireguard-clients',
		async (key) => {
			const result = await getWireGuardClients();
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.clients as WireGuardClient[] | APIResponse
		}
	);

	useInterval(2000, {
		callback: async () => {
			await clientsResource.refetch();
		}
	});

	let serviceDisabled = $derived.by(() => {
		return (
			isAPIResponse(clientsResource.current) &&
			clientsResource.current.message === 'wireguard_service_disabled'
		);
	});

	let clients = $derived.by(() => {
		if (isAPIResponse(clientsResource.current)) {
			return [] as WireGuardClient[];
		}
		return clientsResource.current as WireGuardClient[];
	});

	let modals = $state({
		create: false,
		edit: false,
		delete: false,
		toggle: false,
		data: null as WireGuardClient | null
	});

	async function confirmDelete() {
		if (!modals.data?.id) return;
		const name = modals.data.name;
		const response = await wireGuardClients.remove(modals.data.id);
		await clientsResource.refetch();
		if (response.status === 'success') {
			toast.success(`Client "${name}" removed`, { position: 'bottom-center' });
			modals.delete = false;
			modals.data = null;
		} else {
			handleAPIError(response);
			toast.error(response.message || 'Failed to remove client', { position: 'bottom-center' });
		}
	}

	async function confirmToggle() {
		if (!modals.data?.id) return;
		const name = modals.data.name;
		const status = wireGuardClientStatus(modals.data);
		const response = await wireGuardClients.toggle(modals.data.id);
		await clientsResource.refetch();
		if (response.status === 'success') {
			toast.success(`Client "${name}" ${status === 'disabled' ? 'enabled' : 'disabled'}`, {
				position: 'bottom-center'
			});
			modals.toggle = false;
			modals.data = null;
		} else {
			handleAPIError(response);
			toast.error(response.message || 'Failed to toggle client', { position: 'bottom-center' });
		}
	}

	function statusBarColor(status: string): string {
		if (status === 'active') return 'bg-emerald-600';
		if (status === 'idle') return 'bg-yellow-500';
		if (status === 'disconnected') return 'bg-orange-500';
		return 'bg-muted-foreground/40';
	}

	function statusBadgeClasses(status: string): string {
		if (status === 'active') return 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30';
		if (status === 'idle') return 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30';
		if (status === 'disconnected') return 'bg-orange-500/20 text-orange-400 border-orange-500/30';
		return 'bg-muted/50 text-muted-foreground border-border';
	}

	function statusIcon(status: string): string {
		if (status === 'active') return 'icon-[mdi--check-circle]';
		if (status === 'idle') return 'icon-[mdi--clock-outline]';
		if (status === 'disconnected') return 'icon-[mdi--connection]';
		return 'icon-[mdi--block-helper]';
	}

	function formatHandshake(time: string): string {
		if (!time || time.startsWith('0001')) return 'Never';
		return convertDbTime(time);
	}

	let toggleLabel = $derived.by(() => {
		if (!modals.data) return 'Continue';
		return wireGuardClientStatus(modals.data) === 'disabled' ? 'Enable' : 'Disable';
	});

	let toggleTitle = $derived.by(() => {
		if (!modals.data) return '';
		const status = wireGuardClientStatus(modals.data);
		return status === 'disabled'
			? `Enable WireGuard client "${modals.data.name}"?`
			: `Disable WireGuard client "${modals.data.name}"?`;
	});
</script>

<div class="flex h-full w-full flex-col gap-4 p-4">
	{#if serviceDisabled}
		<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-500">
			WireGuard service is disabled. Enable it from System Settings to manage clients.
		</div>
	{/if}

	<div class="flex items-start justify-between">
		<div>
			<h1 class="text-lg font-semibold tracking-tight">
				<span class="icon icon-[boxicons--network-chart] size-5 text-primary"></span>
				<span>Outbound Clients</span>
			</h1>
			<p class="text-sm text-muted-foreground">
				Manage independent WireGuard instances that connect to remote endpoints.
			</p>
		</div>
		<Button
			size="sm"
			class="h-8 gap-2"
			onclick={() => (modals.create = true)}
			disabled={serviceDisabled}
		>
			<span class="icon icon-[mdi--plus] size-4"></span>
			New Client
		</Button>
	</div>

	<div class="grid gap-4 [grid-template-columns:repeat(auto-fill,minmax(300px,1fr))]">
		{#each clients as client (client.id)}
			{@const status = wireGuardClientStatus(client)}
			<Card.Root
				class="group flex flex-col gap-0 overflow-hidden pb-0 transition-all duration-300 hover:border-muted-foreground/30"
			>
				<Card.Header class="pb-3">
					<div class="flex items-start justify-between">
						<div class="flex w-full items-center gap-3">
							<div
								class="flex size-10 shrink-0 items-center justify-center rounded-lg bg-accent/50"
							>
								<span class="icon icon-[mdi--network] size-5 text-muted-foreground"></span>
							</div>
							<div class="w-full min-w-0">
								<Card.Title
									class="flex w-full flex-wrap items-center justify-between gap-2 font-semibold"
								>
									<span class="truncate">{client.name}</span>
									<span
										class="flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium {statusBadgeClasses(
											status
										)}"
									>
										<span class="icon {statusIcon(status)} size-2.5"></span>
										{status.charAt(0).toUpperCase() + status.slice(1)}
									</span>
								</Card.Title>
								<Card.Description class="mt-1 flex items-center gap-1 truncate text-xs">
									<span class="icon icon-[solar--global-outline] size-3 shrink-0"></span>
									{client.endpointHost}:{client.endpointPort}
								</Card.Description>
							</div>
						</div>
					</div>
				</Card.Header>

				<Card.Content class="flex flex-1 flex-col gap-4 pb-4">
					<div class="grid grid-cols-2 gap-4 rounded-md bg-muted/70 px-3 py-2">
						<div class="flex flex-col gap-0.5">
							<span class="text-[10px] font-semibold uppercase text-muted-foreground"
								>Downloaded</span
							>
							<span class="flex items-center gap-1 font-mono text-xs font-bold text-emerald-400">
								<span class="icon icon-[tabler--arrow-down-left] size-3 text-emerald-400"></span>
								{formatBytesBinary(client.rx)}
							</span>
						</div>
						<div class="flex flex-col gap-0.5">
							<span class="text-[10px] font-semibold uppercase text-muted-foreground">Uploaded</span
							>
							<span class="flex items-center gap-1 font-mono text-xs font-bold text-primary">
								<span class="icon icon-[tabler--arrow-up-right] size-3 text-primary"></span>
								{formatBytesBinary(client.tx)}
							</span>
						</div>
					</div>

					<div class="space-y-1.5">
						<div class="flex items-center justify-between text-xs">
							<span class="flex items-center gap-1.5 text-muted-foreground">
								<span class="icon icon-[mdi--tag-outline] size-3"></span>
								Interface
							</span>
							<span class="font-mono font-medium">wgc{client.id}</span>
						</div>
						<div class="flex items-center justify-between text-xs">
							<span class="flex items-center gap-1.5 text-muted-foreground">
								<span class="icon icon-[mdi--handshake-outline] size-3"></span>
								Last Handshake
							</span>
							<span class="font-medium">{formatHandshake(client.lastHandshake)}</span>
						</div>
					</div>

					<div class="flex items-center gap-1.5 pt-1">
						<Button
							variant="secondary"
							size="sm"
							class="h-9 flex-1 gap-2"
							disabled={serviceDisabled}
							onclick={() => {
								modals.data = client;
								modals.toggle = true;
							}}
						>
							{#if status === 'disabled'}
								<span class="icon icon-[mdi--play] size-4"></span>
								Enable
							{:else}
								<span class="icon icon-[mdi--pause] size-4"></span>
								Disable
							{/if}
						</Button>
						<Button
							variant="outline"
							size="icon"
							class="h-9 w-9 border-border/50 bg-transparent"
							disabled={serviceDisabled}
							onclick={() => {
								modals.data = client;
								modals.edit = true;
							}}
						>
							<span class="icon icon-[mdi--pencil-outline] size-4"></span>
						</Button>
						<Button
							variant="outline"
							size="icon"
							class="h-9 w-9 border-border/50 bg-transparent text-destructive hover:bg-destructive/10"
							onclick={() => {
								modals.data = client;
								modals.delete = true;
							}}
						>
							<span class="icon icon-[mdi--trash-can-outline] size-4"></span>
						</Button>
					</div>
				</Card.Content>

				<div class="h-1 w-full {statusBarColor(status)}"></div>
			</Card.Root>
		{/each}

		<Button
			variant="ghost"
			disabled={serviceDisabled}
			class="group flex h-full flex-col items-center justify-center gap-4 rounded-xl border-2 border-dashed border-border p-8 transition-all hover:border-primary/50 hover:bg-accent/20"
			onclick={() => (modals.create = true)}
		>
			<div
				class="flex size-12 items-center justify-center rounded-full bg-accent/50 transition-transform group-hover:scale-110"
			>
				<span class="icon icon-[mdi--plus] size-6 text-muted-foreground group-hover:text-primary"
				></span>
			</div>
			<div class="w-full overflow-hidden text-center">
				<span class="mb-2 block text-sm font-bold">Add New Client</span>
				<span class="mt-2 block whitespace-normal wrap-break-word text-xs text-muted-foreground">
					Configure a new independent tunnel to a remote WireGuard endpoint
				</span>
			</div>
		</Button>
	</div>
</div>

<AlertDialog
	bind:open={modals.delete}
	customTitle="This action removes the selected WireGuard client. Continue?"
	actions={{
		onConfirm: confirmDelete,
		onCancel: () => {
			modals.delete = false;
			modals.data = null;
		}
	}}
/>

<AlertDialog
	bind:open={modals.toggle}
	customTitle={toggleTitle}
	confirmLabel={toggleLabel}
	actions={{
		onConfirm: confirmToggle,
		onCancel: () => {
			modals.toggle = false;
			modals.data = null;
		}
	}}
/>

{#if modals.create}
	<ClientForm
		bind:open={modals.create}
		client={null}
		onSaved={async () => {
			await clientsResource.refetch();
		}}
	/>
{/if}

{#if modals.edit && modals.data}
	<ClientForm
		bind:open={modals.edit}
		client={modals.data}
		onSaved={async () => {
			await clientsResource.refetch();
			modals.data = null;
		}}
	/>
{/if}
