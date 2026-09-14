<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { addNetwork, updateNetwork } from '$lib/api/jail/jail';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import VLANPolicyEditor from '$lib/components/custom/Network/VLANPolicyEditor.svelte';
	import type { Jail } from '$lib/types/jail/jail';
	import type { NetworkObject } from '$lib/types/network/object';
	import { isSwitchRuntimeAvailable, type SwitchList } from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import {
		generateIPOptions,
		generateMACOptions,
		generateNetworkOptions
	} from '$lib/utils/network/object';
	import { parseVLANPolicyDraft } from '$lib/utils/network/vlan';
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
			vlanPolicyMode: selectedNetwork?.vlanPolicy.mode ?? ('' as '' | 'access' | 'trunk'),
			vlanPolicyUntagged:
				selectedNetwork?.vlanPolicy.untaggedVlan === undefined
					? ''
					: String(selectedNetwork.vlanPolicy.untaggedVlan),
			vlanPolicyTagged: selectedNetwork?.vlanPolicy.taggedVlans.join(',') ?? ''
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
	let selectedConfiguredSwitch = $derived(
		[...networkSwitches.standard, ...networkSwitches.manual].find(
			(candidate) => candidate.name === values.switchName
		) ?? null
	);
	let selectedSwitchIsFiltered = $derived(selectedConfiguredSwitch?.vlanFiltering ?? false);
	let selectedSwitchIsAvailable = $derived(
		selectedConfiguredSwitch !== null && isSwitchRuntimeAvailable(selectedConfiguredSwitch)
	);
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

	watch(
		() => values.switchName,
		() => {
			if (selectedSwitchIsFiltered) return;
			properties.vlanPolicyMode = '';
			properties.vlanPolicyUntagged = '';
			properties.vlanPolicyTagged = '';
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
		if (!selectedSwitchIsAvailable) {
			toast.error('The selected switch runtime state is unavailable', {
				position: 'bottom-center'
			});
			return;
		}
		if (properties.defaultGateway && hasOtherDefaultGateway) {
			toast.error('Default gateway already exists', { position: 'bottom-center' });
			return;
		}

		let vlanPolicy;
		if (selectedSwitchIsFiltered) {
			const parsedPolicy = parseVLANPolicyDraft(
				properties.vlanPolicyMode,
				properties.vlanPolicyUntagged,
				properties.vlanPolicyTagged
			);
			if (!parsedPolicy) {
				toast.error('A valid access or trunk VLAN policy is required', {
					position: 'bottom-center'
				});
				return;
			}
			vlanPolicy = parsedPolicy;
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
			vlanPolicy
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
		class="flex h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] flex-col overflow-hidden p-5 sm:h-auto sm:max-h-[calc(100dvh-4rem)] sm:max-w-4xl! sm:p-6"
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

		<div class="min-h-0 flex-1 space-y-4 overflow-y-auto pr-2">
			<div class="grid grid-cols-1 items-end gap-4 sm:grid-cols-3">
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
						label: !isSwitchRuntimeAvailable(item)
							? `${item.name} (runtime state unavailable)`
							: item.vlanFiltering
								? `${item.name} (VLAN filtered)`
								: item.name,
						value: item.name,
						disabled: !isSwitchRuntimeAvailable(item)
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
			</div>

			{#if selectedSwitchIsFiltered}
				<VLANPolicyEditor
					title="Jail bridge-member policy"
					bind:mode={properties.vlanPolicyMode}
					bind:untagged={properties.vlanPolicyUntagged}
					bind:tagged={properties.vlanPolicyTagged}
					idPrefix="jail-network"
				/>
			{/if}

			<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
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

			<div class="mt-2 flex flex-wrap items-center gap-4">
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
		</div>

		<Dialog.Footer class="flex shrink-0 justify-end pt-4">
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
