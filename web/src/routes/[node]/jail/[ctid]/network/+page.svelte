<script lang="ts">
	import { deleteNetwork, getJailByCTID } from '$lib/api/jail/jail';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Form from '$lib/components/custom/Jail/Network/Form.svelte';
	import Inherit from '$lib/components/custom/Jail/Network/Inherit.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import type { NetworkObject } from '$lib/types/network/object';
	import { emptySwitchList, isSwitchList, type SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { ipGatewayFormatter, macFormatter } from '$lib/utils/jail/network';
	import { escapeHTML } from '$lib/utils/string';
	import { resource } from 'runed';
	import { getContext, onMount } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Data {
		node: string;
		ctId: number;
		jail: Jail | null;
		jailError: APIResponse | null;
		switches: SwitchList | APIResponse;
		networkObjects: NetworkObject[] | APIResponse;
	}

	interface JailResourceValue {
		identity: string;
		value: Jail | null;
	}

	interface SwitchResourceValue {
		identity: string;
		value: SwitchList;
	}

	interface ObjectResourceValue {
		identity: string;
		value: NetworkObject[];
	}

	let { data }: { data: Data } = $props();
	const initialNode = () => data.node;
	const initialIdentity = () => `${data.node}:${data.ctId}`;
	const initialJail = () => data.jail;
	const initialSwitches = () => (isSwitchList(data.switches) ? data.switches : emptySwitchList());
	const initialObjects = () => (Array.isArray(data.networkObjects) ? data.networkObjects : []);
	let lastJailIdentity = initialIdentity();
	let lastJail = initialJail();

	const jailResource = resource(
		[() => data.node, () => data.ctId],
		async ([hostname, ctId], _, { signal }): Promise<JailResourceValue> => {
			const identity = `${hostname}:${ctId}`;
			if (lastJailIdentity !== identity) {
				lastJailIdentity = identity;
				lastJail = identity === initialIdentity() ? data.jail : null;
			}
			const result = await getJailByCTID(ctId, {
				hostname,
				signal
			});
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return { identity, value: lastJail };
			}
			lastJail = result;
			await updateCache(`jail-${ctId}`, result, hostname);
			return { identity, value: result };
		},
		{
			initialValue: { identity: initialIdentity(), value: initialJail() }
		}
	);

	const networkSwitches = resource(
		() => data.node,
		async (hostname): Promise<SwitchResourceValue> => {
			const result = await getSwitches(hostname);
			if (!isSwitchList(result)) {
				handleAPIError(result);
				return {
					identity: hostname,
					value: hostname === data.node ? initialSwitches() : emptySwitchList()
				};
			}
			await updateCache('network-switches', result, hostname);
			return { identity: hostname, value: result };
		},
		{ initialValue: { identity: initialNode(), value: initialSwitches() } }
	);

	const networkObjects = resource(
		() => data.node,
		async (hostname): Promise<ObjectResourceValue> => {
			const result = await getNetworkObjects(hostname);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return {
					identity: hostname,
					value: hostname === data.node ? initialObjects() : []
				};
			}
			await updateCache('network-objects', result, hostname);
			return { identity: hostname, value: result };
		},
		{ initialValue: { identity: initialNode(), value: initialObjects() } }
	);

	const jailState = getContext<{ current: JailState | null; refetch(): void }>('jailState');
	let currentJail = $derived(
		jailResource.current.identity === initialIdentity() ? jailResource.current.value : data.jail
	);
	let currentSwitches = $derived(
		networkSwitches.current.identity === data.node
			? networkSwitches.current.value
			: emptySwitchList()
	);
	let currentObjects = $derived(
		networkObjects.current.identity === data.node ? networkObjects.current.value : []
	);
	let inherited = $derived(!!(currentJail?.inheritIPv4 || currentJail?.inheritIPv6));
	let inactive = $derived(
		jailState.current?.ctId === data.ctId &&
			jailState.current.state === 'INACTIVE' &&
			!jailState.current.pendingAction
	);

	onMount(() => {
		if (data.jailError) handleAPIError(data.jailError);
		if (isAPIResponse(data.switches)) handleAPIError(data.switches);
		if (isAPIResponse(data.networkObjects)) handleAPIError(data.networkObjects);
	});

	async function reloadNetworkData() {
		await Promise.all([
			jailResource.refetch(),
			networkSwitches.refetch(),
			networkObjects.refetch()
		]);
		jailState.refetch();
	}

	let modals = $state({
		create: false,
		inherit: false,
		delete: false,
		edit: {
			open: false,
			id: null as number | null
		}
	});

	let table = $derived.by(() => {
		const columns: Column[] = [
			{ title: 'Name', field: 'name' },
			{ title: 'Switch', field: 'switch' },
			{ title: 'MAC', field: 'mac' },
			{ title: 'VLAN', field: 'vlan' },
			{ title: 'IPv4', field: 'ipv4', formatter: 'html' },
			{ title: 'IPv6', field: 'ipv6', formatter: 'html' }
		];
		if (!currentJail || inherited) return { rows: [], columns };

		const rows: Row[] = currentJail.networks.map((network) => {
			const switchName =
				network.switchType === 'standard'
					? currentSwitches.standard.find((item) => item.id === network.switchId)?.name || '-'
					: currentSwitches.manual.find((item) => item.id === network.switchId)?.name || '-';
			return {
				id: network.id,
				name: network.name,
				switch: switchName,
				mac: macFormatter(currentObjects, network.macId || 0),
				vlan: network.vlan === 0 ? '-' : network.vlan,
				ipv4: network.dhcp
					? 'DHCP'
					: network.ipv4Id
						? ipGatewayFormatter(currentObjects, network.ipv4Id, network.ipv4GwId)
						: '-',
				ipv6: network.slaac
					? 'SLAAC'
					: network.ipv6Id
						? ipGatewayFormatter(currentObjects, network.ipv6Id, network.ipv6GwId)
						: '-'
			};
		});
		return { rows, columns };
	});

	let activeRows: Row[] = $state([]);
	let activeRow = $derived(activeRows.length === 1 ? activeRows[0] : null);
	let query = $state('');
	let deleting = $state(false);

	async function handleSwitchDelete() {
		if (!currentJail || !activeRow || deleting || !inactive) return;
		const hostname = data.node;
		const ctId = data.ctId;
		const networkId = Number(activeRow.id);
		if (!Number.isSafeInteger(networkId) || networkId <= 0) return;

		deleting = true;
		try {
			const response = await deleteNetwork(ctId, networkId, {
				hostname
			});
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to delete network', { position: 'bottom-center' });
				return;
			}
			toast.success('Network deleted', { position: 'bottom-center' });
			modals.delete = false;
			activeRows = [];
			await reloadNetworkData();
		} catch {
			toast.error('Failed to delete network', { position: 'bottom-center' });
		} finally {
			deleting = false;
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		{#if !inherited}
			<Button
				size="sm"
				class="h-6"
				disabled={!inactive}
				title={inactive ? 'New network' : 'Stop the jail before changing its network'}
				onclick={() => (modals.create = true)}
			>
				<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-1" title="New" />
			</Button>
		{/if}

		<Button
			onclick={() => (modals.inherit = true)}
			size="sm"
			variant="outline"
			disabled={!inactive}
			title={inactive ? 'Change network inheritance' : 'Stop the jail before changing its network'}
			class="h-6.5 {activeRows.length > 0 ? 'hidden' : ''}"
		>
			<SpanWithIcon
				icon={inherited ? 'icon-[mdi--close-network]' : 'icon-[mdi--plus-network]'}
				size="h-4 w-4"
				gap="gap-1"
				title={inherited ? 'Change Network Inheritance' : 'Inherit Network'}
			/>
		</Button>

		{#if activeRow}
			<Button
				size="sm"
				class="h-6"
				variant="outline"
				disabled={!inactive}
				title={inactive ? 'Edit network' : 'Stop the jail before changing its network'}
				onclick={() => {
					modals.edit.id = Number(activeRow?.id);
					modals.edit.open = true;
				}}
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit" />
			</Button>

			<Button
				size="sm"
				class="h-6"
				variant="outline"
				disabled={!inactive || deleting}
				title={inactive ? 'Delete network' : 'Stop the jail before changing its network'}
				onclick={() => (modals.delete = true)}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-1" title="Delete" />
			</Button>
		{/if}

		{#if currentJail && !inactive}
			<span class="text-muted-foreground ml-auto text-xs">Stop the jail to change networking.</span>
		{/if}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		{#if currentJail}
			<TreeTable
				data={table}
				name="jail-networks-tt"
				bind:parentActiveRow={activeRows}
				multipleSelect={false}
				bind:query
			/>
		{:else}
			<div class="text-muted-foreground flex h-full items-center justify-center text-sm">
				Unable to load this jail's network configuration.
			</div>
		{/if}
	</div>
</div>

{#if modals.inherit && currentJail}
	<Inherit
		bind:open={modals.inherit}
		jail={currentJail}
		hostname={data.node}
		onSaved={reloadNetworkData}
	/>
{/if}

{#if modals.delete && activeRow}
	<AlertDialog
		bind:open={modals.delete}
		customTitle={`This will detach the jail from the switch <b>${escapeHTML(String(activeRow.switch || '-'))}</b>.`}
		loading={deleting}
		loadingLabel="Deleting..."
		keepOpenOnConfirm={true}
		actions={{
			onConfirm: handleSwitchDelete,
			onCancel: () => (modals.delete = false)
		}}
	/>
{/if}

{#if modals.create && currentJail}
	<Form
		bind:open={modals.create}
		jail={currentJail}
		hostname={data.node}
		networkObjects={currentObjects}
		networkSwitches={currentSwitches}
		networkId={null}
		onSaved={reloadNetworkData}
	/>
{/if}

{#if modals.edit.open && modals.edit.id !== null && currentJail}
	<Form
		bind:open={modals.edit.open}
		jail={currentJail}
		hostname={data.node}
		networkObjects={currentObjects}
		networkSwitches={currentSwitches}
		networkId={modals.edit.id}
		onSaved={reloadNetworkData}
	/>
{/if}
