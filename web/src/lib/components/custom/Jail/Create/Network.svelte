<script lang="ts">
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { isValidIPv4, isValidIPv6 } from '$lib/utils/string';
	import {
		generateIPOptions,
		generateMACOptions,
		generateNetworkOptions
	} from '$lib/utils/network/object';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { watch } from 'runed';

	interface Props {
		switch: string;
		mac: number;
		inheritIPv4: boolean;
		inheritIPv6: boolean;
		ipv4: number;
		ipv4Gateway: number;
		ipv6: number;
		ipv6Gateway: number;
		dhcp: boolean;
		slaac: boolean;
		switches: SwitchList;
		networkObjects: NetworkObject[];
	}

	let {
		switch: nwSwitch = $bindable(),
		mac = $bindable(),
		inheritIPv4 = $bindable(),
		inheritIPv6 = $bindable(),
		ipv4 = $bindable(),
		ipv4Gateway = $bindable(),
		ipv6 = $bindable(),
		ipv6Gateway = $bindable(),
		dhcp = $bindable(),
		slaac = $bindable(),
		switches,
		networkObjects
	}: Props = $props();

	let usable = $derived({
		macs: networkObjects.filter(
			(obj) => obj.isUsed === false && obj.type === 'Mac' && obj.entries?.length === 1
		),
		ipv4: networkObjects.filter(
			(obj) =>
				obj.isUsed === false &&
				obj.type === 'Network' &&
				obj.entries?.length === 1 &&
				isValidIPv4(obj.entries[0].value, true)
		),
		ipv4Gateway: networkObjects.filter(
			(obj) => obj.type === 'Host' && obj.entries?.length === 1 && isValidIPv4(obj.entries[0].value)
		),
		ipv6: networkObjects.filter(
			(obj) =>
				obj.isUsed === false &&
				obj.type === 'Network' &&
				obj.entries?.length === 1 &&
				isValidIPv6(obj.entries[0].value, true)
		),
		ipv6Gateway: networkObjects.filter(
			(obj) => obj.type === 'Host' && obj.entries?.length === 1 && isValidIPv6(obj.entries[0].value)
		)
	});

	let comboBoxes = $state({
		mac: {
			open: false,
			value: '0'
		},
		ipv4: {
			open: false,
			value: '0'
		},
		ipv4Gateway: {
			open: false,
			value: '0'
		},
		ipv6: {
			open: false,
			value: '0'
		},
		ipv6Gateway: {
			open: false,
			value: '0'
		}
	});

	let checkBoxes = $state({
		dhcp: false,
		slaac: false
	});

	watch(
		() => nwSwitch,
		(current) => {
			if (current === 'None') {
				mac = 0;
				ipv4 = 0;
				ipv6 = 0;
				dhcp = false;
				slaac = false;
				checkBoxes.dhcp = false;
				checkBoxes.slaac = false;
			} else if (current !== 'Inherit') {
				inheritIPv4 = false;
				inheritIPv6 = false;
			}
		}
	);

	watch(
		() => checkBoxes.dhcp,
		(current) => {
			if (current) {
				comboBoxes.ipv4.value = '0';
				comboBoxes.ipv4Gateway.value = '0';
				dhcp = true;
			} else {
				if (comboBoxes.ipv4.value !== '0') {
					ipv4 = parseInt(comboBoxes.ipv4.value) || 0;
				}
				dhcp = false;
			}
		}
	);

	watch(
		() => checkBoxes.slaac,
		(current) => {
			if (current) {
				comboBoxes.ipv6.value = '0';
				comboBoxes.ipv6Gateway.value = '0';
				slaac = true;
			} else {
				if (comboBoxes.ipv6.value !== '0') {
					ipv6 = parseInt(comboBoxes.ipv6.value) || 0;
				}
				slaac = false;
			}
		}
	);

	watch(
		() => comboBoxes.mac.value,
		(current) => {
			if (current !== '0') {
				mac = parseInt(comboBoxes.mac.value) || 0;
			} else {
				mac = 0;
			}
		}
	);

	watch(
		[
			() => comboBoxes.ipv4.value,
			() => comboBoxes.ipv4Gateway.value,
			() => comboBoxes.ipv6.value,
			() => comboBoxes.ipv6Gateway.value
		],
		([v4, v4Gw, v6, v6Gw]) => {
			const parse = (val: string) => (val !== '0' ? parseInt(val) || 0 : 0);

			ipv4 = parse(v4);
			ipv4Gateway = parse(v4Gw);
			ipv6 = parse(v6);
			ipv6Gateway = parse(v6Gw);
		}
	);
</script>

{#snippet radioItem(
	id: number,
	name: string,
	type: 'standard' | 'manual' | 'inherit' | 'none' = 'standard'
)}
	{@const i = `radio-${type}-${id}`}
	<div class="mb-2 flex items-center space-x-3 rounded-lg border p-4">
		<RadioGroup.Item value={name} id={i} />
		<Label for={i} class="flex flex-col items-start gap-2">
			<p class="">{name}</p>
			<p class="text-muted-foreground text-sm">
				{#if type === 'standard'}
					Standard switch
				{:else if type === 'manual'}
					Manual switch
				{:else if type === 'inherit'}
					Inherit network from the host
				{:else if type === 'none'}
					No network switch will be allocated now, you can add it later
				{/if}
			</p>
		</Label>
	</div>
{/snippet}

<div class="flex flex-col gap-4 p-4">
	<RadioGroup.Root bind:value={nwSwitch} class="border p-2">
		<ScrollArea orientation="vertical" class="h-64 w-full max-w-full">
			{#if switches && switches.standard}
				{#each switches.standard ?? [] as sw}
					{@render radioItem(sw.id, sw.name, 'standard')}
				{/each}
			{/if}

			{#if switches && switches.manual}
				{#each switches.manual ?? [] as sw}
					{@render radioItem(sw.id, sw.name, 'manual')}
				{/each}
			{/if}

			{@render radioItem(-1, 'Inherit', 'inherit')}
			{@render radioItem(0, 'None', 'none')}
		</ScrollArea>
	</RadioGroup.Root>

	{#if nwSwitch !== 'None' && nwSwitch !== 'Inherit'}
		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
			<CustomComboBox
				bind:open={comboBoxes.ipv4.open}
				label="IPv4 Network"
				bind:value={comboBoxes.ipv4.value}
				data={generateNetworkOptions(usable.ipv4, 'IPv4')}
				classes="flex-1 space-y-1"
				placeholder="Select IPv4"
				width="w-full"
				disabled={usable.ipv4.length === 0 || checkBoxes.dhcp}
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.ipv4Gateway.open}
				label="IPv4 Gateway"
				bind:value={comboBoxes.ipv4Gateway.value}
				data={generateIPOptions(usable.ipv4Gateway, 'IPv4')}
				classes="flex-1 space-y-1"
				placeholder="Select IPv4 Gateway"
				width="w-full"
				disabled={usable.ipv4Gateway.length === 0 || checkBoxes.dhcp}
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.ipv6.open}
				label="IPv6 Network"
				bind:value={comboBoxes.ipv6.value}
				data={generateNetworkOptions(usable.ipv6, 'IPv6')}
				classes="flex-1 space-y-1"
				placeholder="Select IPv6"
				width="w-full"
				disabled={usable.ipv6.length === 0 || checkBoxes.slaac}
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.ipv6Gateway.open}
				label="IPv6 Gateway"
				bind:value={comboBoxes.ipv6Gateway.value}
				data={generateIPOptions(usable.ipv6Gateway, 'IPv6')}
				classes="flex-1 space-y-1"
				placeholder="Select IPv6 Gateway"
				width="w-full"
				disabled={usable.ipv6Gateway.length === 0 || checkBoxes.slaac}
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.mac.open}
				label="MAC Address"
				bind:value={comboBoxes.mac.value}
				data={generateMACOptions(usable.macs)}
				classes="flex-1 space-y-1"
				placeholder="Select MAC"
				width="w-full"
			></CustomComboBox>
		</div>

		<div class="mt-1 flex flex-row gap-4">
			<CustomCheckbox label="DHCP" bind:checked={checkBoxes.dhcp} classes="flex items-center gap-2"
			></CustomCheckbox>
			<CustomCheckbox
				label="SLAAC"
				bind:checked={checkBoxes.slaac}
				classes="flex items-center gap-2"
			></CustomCheckbox>
		</div>
	{:else if nwSwitch === 'Inherit'}
		<div class="mt-1 flex flex-row gap-4">
			<CustomCheckbox label="IPv4" bind:checked={inheritIPv4} classes="flex items-center gap-2"
			></CustomCheckbox>
			<CustomCheckbox label="IPv6" bind:checked={inheritIPv6} classes="flex items-center gap-2"
			></CustomCheckbox>
		</div>
	{/if}
</div>
