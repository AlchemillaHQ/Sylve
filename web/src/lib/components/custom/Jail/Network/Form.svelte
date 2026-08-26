<script lang="ts">
	import { addNetwork, updateNetwork } from '$lib/api/jail/jail';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Jail } from '$lib/types/jail/jail';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import {
		generateIPOptions,
		generateMACOptions,
		generateNetworkOptions
	} from '$lib/utils/network/object';
	import { parseNumberOrZero } from '$lib/utils/string';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		hostname: string;
		networkObjects: NetworkObject[];
		networkSwitches: SwitchList;
		networkId: number | null;
		onSaved: () => void | Promise<void>;
	}

	let {
		open = $bindable(),
		jail,
		hostname,
		networkObjects,
		networkSwitches,
		networkId,
		onSaved
	}: Props = $props();

	let selectedNetwork = $derived(
		networkId === null ? null : jail.networks.find((network) => network.id === networkId) || null
	);
	let selectedSwitchName = $derived.by(() => {
		if (!selectedNetwork) return '';
		const collection =
			selectedNetwork.switchType === 'standard' ? networkSwitches.standard : networkSwitches.manual;
		return collection.find((item) => item.id === selectedNetwork?.switchId)?.name || '';
	});
	let hasOtherDefaultGateway = $derived(
		jail.networks.some(
			(network) => network.defaultGateway && network.id !== (selectedNetwork?.id ?? 0)
		)
	);
	let currentObjectIDs = $derived(
		new Set(
			[selectedNetwork?.macId, selectedNetwork?.ipv4Id, selectedNetwork?.ipv6Id].filter(
				(id): id is number => typeof id === 'number' && id > 0
			)
		)
	);
	let selectableObjects = $derived(
		networkObjects.filter(
			(object) =>
				object.entries?.length === 1 &&
				(object.type === 'Host' || !object.isUsed || currentObjectIDs.has(object.id))
		)
	);
	let macOptions = $derived(generateMACOptions(selectableObjects));
	let ipv4Options = $derived(generateNetworkOptions(selectableObjects, 'ipv4'));
	let ipv6Options = $derived(generateNetworkOptions(selectableObjects, 'ipv6'));
	let ipv4GatewayOptions = $derived(generateIPOptions(selectableObjects, 'ipv4'));
	let ipv6GatewayOptions = $derived(generateIPOptions(selectableObjects, 'ipv6'));

	function initialProperties() {
		return {
			name: selectedNetwork?.name || '',
			dhcp: selectedNetwork?.dhcp ?? false,
			slaac: selectedNetwork?.slaac ?? false,
			defaultGateway: selectedNetwork?.defaultGateway ?? false,
			vlan: selectedNetwork?.vlan ?? 0
		};
	}

	function initialValues() {
		return {
			switchName: selectedSwitchName,
			mac: selectedNetwork?.macId?.toString() || '',
			ipv4:
				selectedNetwork && !selectedNetwork.dhcp ? selectedNetwork.ipv4Id?.toString() || '' : '',
			ipv4Gateway:
				selectedNetwork && !selectedNetwork.dhcp ? selectedNetwork.ipv4GwId?.toString() || '' : '',
			ipv6:
				selectedNetwork && !selectedNetwork.slaac ? selectedNetwork.ipv6Id?.toString() || '' : '',
			ipv6Gateway:
				selectedNetwork && !selectedNetwork.slaac ? selectedNetwork.ipv6GwId?.toString() || '' : ''
		};
	}

	let properties = $state(initialProperties());
	let values = $state(initialValues());
	let comboOpen = $state({
		switchName: false,
		mac: false,
		ipv4: false,
		ipv4Gateway: false,
		ipv6: false,
		ipv6Gateway: false
	});
	let saving = $state(false);

	function resetForm() {
		properties = initialProperties();
		values = initialValues();
	}

	watch(
		() => `${jail.ctId}:${networkId ?? 'new'}:${selectedNetwork?.id ?? 0}`,
		() => resetForm()
	);

	watch(
		() => properties.dhcp,
		(dhcp) => {
			if (!dhcp) return;
			values.ipv4 = '';
			values.ipv4Gateway = '';
		}
	);

	watch(
		() => properties.slaac,
		(slaac) => {
			if (!slaac) return;
			values.ipv6 = '';
			values.ipv6Gateway = '';
		}
	);

	watch([() => properties.dhcp, () => properties.slaac], ([dhcp, slaac]) => {
		if (dhcp && slaac) properties.defaultGateway = false;
	});

	function resolveField(value: string, type: NetworkObject['type']) {
		if (!value) return { id: 0, raw: '' };
		const object = selectableObjects.find(
			(candidate) => candidate.type === type && candidate.id.toString() === value
		);
		const existingID = Number(value);
		if (!object && Number.isSafeInteger(existingID) && currentObjectIDs.has(existingID)) {
			return { id: existingID, raw: '' };
		}
		return object ? { id: object.id, raw: '' } : { id: 0, raw: value.trim() };
	}

	async function save() {
		if (saving) return;
		const name = properties.name.trim();
		const switchName = values.switchName.trim();
		if (!name) {
			toast.error('Name is required', { position: 'bottom-center' });
			return;
		}
		if (
			jail.networks.some(
				(network) => network.name === name && network.id !== (selectedNetwork?.id ?? 0)
			)
		) {
			toast.error('Network name already exists', { position: 'bottom-center' });
			return;
		}
		if (!switchName) {
			toast.error('Switch is required', { position: 'bottom-center' });
			return;
		}
		if (properties.defaultGateway && hasOtherDefaultGateway) {
			toast.error('Default gateway already exists', { position: 'bottom-center' });
			return;
		}

		const mac = resolveField(values.mac, 'Mac');
		const ipv4 = resolveField(values.ipv4, 'Network');
		const ipv4Gateway = resolveField(values.ipv4Gateway, 'Host');
		const ipv6 = resolveField(values.ipv6, 'Network');
		const ipv6Gateway = resolveField(values.ipv6Gateway, 'Host');
		const payload = {
			name,
			switchName,
			macId: mac.id,
			macRaw: mac.raw,
			ip4: ipv4.id,
			ip4Raw: ipv4.raw,
			ip4gw: ipv4Gateway.id,
			ip4gwRaw: ipv4Gateway.raw,
			ip6: ipv6.id,
			ip6Raw: ipv6.raw,
			ip6gw: ipv6Gateway.id,
			ip6gwRaw: ipv6Gateway.raw,
			dhcp: properties.dhcp,
			slaac: properties.slaac,
			defaultGateway: properties.defaultGateway,
			vlan: parseNumberOrZero(String(properties.vlan))
		};
		const requestIdentity = `${hostname}:${jail.ctId}:${networkId ?? 'new'}`;

		saving = true;
		try {
			const response = selectedNetwork
				? await updateNetwork(jail.ctId, selectedNetwork.id, payload, {
						hostname
					})
				: await addNetwork(jail.ctId, payload, { hostname });
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error(selectedNetwork ? 'Failed to update network' : 'Failed to add network', {
					position: 'bottom-center'
				});
				return;
			}
			toast.success(
				selectedNetwork ? 'Network updated successfully' : 'Network added successfully',
				{
					position: 'bottom-center'
				}
			);
			if (`${hostname}:${jail.ctId}:${networkId ?? 'new'}` === requestIdentity) {
				await onSaved();
				open = false;
			}
		} catch {
			toast.error(selectedNetwork ? 'Failed to update network' : 'Failed to add network', {
				position: 'bottom-center'
			});
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="min-w-150"
		showResetButton={true}
		onReset={resetForm}
		onClose={() => {
			if (!saving) {
				resetForm();
				open = false;
			}
		}}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="text-left">
				<SpanWithIcon
					icon="icon-[mdi--network]"
					size="h-5 w-5"
					gap="gap-2"
					title={selectedNetwork ? `Edit - ${selectedNetwork.name}` : 'New Network'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-4 items-end gap-4">
			<CustomValueInput
				label="Name"
				placeholder="Primary Network"
				bind:value={properties.name}
				classes="flex-1 space-y-1"
			/>
			<CustomComboBox
				bind:open={comboOpen.switchName}
				label="Switch"
				placeholder="Select Switch"
				bind:value={values.switchName}
				data={[...networkSwitches.standard, ...networkSwitches.manual].map((item) => ({
					label: item.name,
					value: item.name
				}))}
				classes="flex-1 space-y-1"
				triggerWidth="w-full"
				width="w-full"
			/>
			<CustomComboBox
				bind:open={comboOpen.mac}
				label="MAC Address"
				placeholder="Select or type a MAC"
				bind:value={values.mac}
				data={macOptions}
				classes="flex-1 space-y-1"
				triggerWidth="w-full"
				width="w-full"
				allowCustom={true}
			/>
			<CustomValueInput
				label="VLAN"
				placeholder="0"
				bind:value={properties.vlan}
				classes="flex-1 space-y-1"
				type="number"
			/>
		</div>

		<div class="grid grid-cols-2 gap-4">
			<CustomComboBox
				bind:open={comboOpen.ipv4}
				label="IPv4 Address"
				placeholder="Select or type an IPv4 CIDR"
				bind:value={values.ipv4}
				data={ipv4Options}
				classes="w-full flex-1 space-y-1"
				triggerWidth="w-full"
				width="w-full"
				disabled={properties.dhcp}
				allowCustom={true}
			/>
			<CustomComboBox
				bind:open={comboOpen.ipv4Gateway}
				label="IPv4 Gateway"
				placeholder="Select or type an IPv4 gateway"
				bind:value={values.ipv4Gateway}
				data={ipv4GatewayOptions}
				classes="flex-1 space-y-1"
				triggerWidth="w-full"
				width="w-full"
				disabled={properties.dhcp}
				allowCustom={true}
			/>
			<CustomComboBox
				bind:open={comboOpen.ipv6}
				label="IPv6 Address"
				placeholder="Select or type an IPv6 CIDR"
				bind:value={values.ipv6}
				data={ipv6Options}
				classes="flex-1 space-y-1"
				triggerWidth="w-full"
				width="w-full"
				disabled={properties.slaac}
				allowCustom={true}
			/>
			<CustomComboBox
				bind:open={comboOpen.ipv6Gateway}
				label="IPv6 Gateway"
				placeholder="Select or type an IPv6 gateway"
				bind:value={values.ipv6Gateway}
				data={ipv6GatewayOptions}
				classes="flex-1 space-y-1"
				triggerWidth="w-full"
				width="w-full"
				disabled={properties.slaac}
				allowCustom={true}
			/>
		</div>

		<div class="mt-2 flex items-center space-x-4">
			{#if jail.type === 'freebsd'}
				<CustomCheckbox
					bind:checked={properties.dhcp}
					label="DHCP"
					classes="flex items-center gap-2"
				/>
				<CustomCheckbox
					bind:checked={properties.slaac}
					label="SLAAC"
					classes="flex items-center gap-2"
				/>
			{/if}
			{#if jail.type !== 'freebsd' || !(properties.dhcp && properties.slaac)}
				<CustomCheckbox
					bind:checked={properties.defaultGateway}
					label="Default Gateway"
					classes="flex items-center gap-2"
					disabled={hasOtherDefaultGateway}
				/>
			{/if}
		</div>

		<Dialog.Footer class="flex justify-end">
			<Button onclick={save} type="submit" size="sm" disabled={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					Saving...
				{:else}
					{selectedNetwork ? 'Save Changes' : 'Save'}
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
