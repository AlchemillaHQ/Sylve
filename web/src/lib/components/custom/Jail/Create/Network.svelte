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
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { watch } from 'runed';
	import SimpleSelect from '../../SimpleSelect.svelte';
	import { dnsConfigPresets } from '$lib/utils/jail/jail';
	import NetworkObjectCreator from '$lib/components/custom/Network/Objects/CreateOrEdit.svelte';
	import { getDashedDate } from '$lib/utils/time.svelte';

	interface Props {
		name: string;
		ctId: number;
		switch: string;
		mac: number;
		macRaw: string;
		inheritIPv4: boolean;
		inheritIPv6: boolean;
		ipv4: number;
		ipv4Raw: string;
		ipv4Gateway: number;
		ipv4GatewayRaw: string;
		ipv6: number;
		ipv6Raw: string;
		ipv6Gateway: number;
		ipv6GatewayRaw: string;
		dhcp: boolean;
		slaac: boolean;
		resolvConf: string;
		vlan: number;
		switches: SwitchList;
		networkObjects: NetworkObject[];
		jailType: 'freebsd' | 'linux';
		refetch: boolean;
	}

	let {
		name,
		ctId,
		switch: nwSwitch = $bindable(),
		mac = $bindable(),
		macRaw = $bindable(),
		inheritIPv4 = $bindable(),
		inheritIPv6 = $bindable(),
		ipv4 = $bindable(),
		ipv4Raw = $bindable(),
		ipv4Gateway = $bindable(),
		ipv4GatewayRaw = $bindable(),
		ipv6 = $bindable(),
		ipv6Raw = $bindable(),
		ipv6Gateway = $bindable(),
		ipv6GatewayRaw = $bindable(),
		dhcp = $bindable(),
		slaac = $bindable(),
		resolvConf = $bindable(),
		vlan = $bindable(),
		switches,
		networkObjects,
		jailType,
		refetch = $bindable()
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
			value: ''
		},
		ipv4: {
			open: false,
			value: ''
		},
		ipv4Gateway: {
			open: false,
			value: ''
		},
		ipv6: {
			open: false,
			value: ''
		},
		ipv6Gateway: {
			open: false,
			value: ''
		}
	});

	let checkBoxes = $state({
		dhcp: false,
		slaac: false,
		resolvConf: false
	});

	watch(
		() => resolvConf,
		(current) => {
			if (current.trim().length > 0) {
				checkBoxes.resolvConf = true;
			}
		}
	);

	watch(
		() => checkBoxes.resolvConf,
		(current) => {
			if (!current) {
				resolvConf = '';
			}
		}
	);

	watch(
		() => nwSwitch,
		(current) => {
			if (current === 'None') {
				mac = 0;
				macRaw = '';
				ipv4 = 0;
				ipv4Raw = '';
				ipv4Gateway = 0;
				ipv4GatewayRaw = '';
				ipv6 = 0;
				ipv6Raw = '';
				ipv6Gateway = 0;
				ipv6GatewayRaw = '';
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

	function resolveField(
		val: string,
		usable: NetworkObject[]
	): { id: number; raw: string } {
		if (!val) return { id: 0, raw: '' };
		const obj = usable.find((o) => String(o.id) === val);
		if (obj) return { id: obj.id, raw: '' };
		return { id: 0, raw: val };
	}

	watch(
		() => checkBoxes.dhcp,
		(current) => {
			if (current) {
				comboBoxes.ipv4.value = '';
				comboBoxes.ipv4Gateway.value = '';
				ipv4Raw = '';
				ipv4GatewayRaw = '';
				dhcp = true;
			} else {
				if (comboBoxes.ipv4.value) {
					const r = resolveField(comboBoxes.ipv4.value, usable.ipv4);
					ipv4 = r.id;
					ipv4Raw = r.raw;
				}
				dhcp = false;
			}
		}
	);

	watch(
		() => checkBoxes.slaac,
		(current) => {
			if (current) {
				comboBoxes.ipv6.value = '';
				comboBoxes.ipv6Gateway.value = '';
				ipv6Raw = '';
				ipv6GatewayRaw = '';
				slaac = true;
			} else {
				if (comboBoxes.ipv6.value) {
					const r = resolveField(comboBoxes.ipv6.value, usable.ipv6);
					ipv6 = r.id;
					ipv6Raw = r.raw;
				}
				slaac = false;
			}
		}
	);

	watch(
		() => comboBoxes.mac.value,
		(current) => {
			const r = resolveField(current, usable.macs);
			mac = r.id;
			macRaw = r.raw;
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
			const r4 = resolveField(v4, usable.ipv4);
			const r4Gw = resolveField(v4Gw, usable.ipv4Gateway);
			const r6 = resolveField(v6, usable.ipv6);
			const r6Gw = resolveField(v6Gw, usable.ipv6Gateway);

			ipv4 = r4.id;
			ipv4Raw = r4.raw;
			ipv4Gateway = r4Gw.id;
			ipv4GatewayRaw = r4Gw.raw;
			ipv6 = r6.id;
			ipv6Raw = r6.raw;
			ipv6Gateway = r6Gw.id;
			ipv6GatewayRaw = r6Gw.raw;
		}
	);

	watch(
		() => nwSwitch,
		() => {
			comboBoxes.mac.value = '';
			comboBoxes.ipv4.value = '';
			comboBoxes.ipv4Gateway.value = '';
			comboBoxes.ipv6.value = '';
			comboBoxes.ipv6Gateway.value = '';
		}
	);

	let selectedDnsPreset = $state('');

	watch(
		() => jailType,
		(current) => {
			if (current === 'linux') {
				checkBoxes.dhcp = false;
				checkBoxes.slaac = false;
				dhcp = false;
				slaac = false;
			}
		}
	);

	let objectCreator = $state({
		open: false,
		name: '',
		type: 'Network(s)' as 'Network(s)' | 'Host(s)' | 'MAC(s)' | 'DUID(s)',
		ocType: 'ipv4-net' as 'ipv4-net' | 'ipv6-net' | 'ipv4-gw' | 'ipv6-gw' | 'mac',
		value: ''
	});
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
				{#each switches.standard ?? [] as sw (sw.name)}
					{@render radioItem(sw.id, sw.name, 'standard')}
				{/each}
			{/if}

			{#if switches && switches.manual}
				{#each switches.manual ?? [] as sw (sw.name)}
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
				placeholder="Select or type an IPv4 CIDR"
				width="w-full"
				allowCustom={true}
				disabled={checkBoxes.dhcp}
				topRightButton={{
					icon: 'icon-[oui--generate]',
					tooltip: 'Create new IPv4 Network',
					function: async () => {
						objectCreator.name = name
							? `${name} IPv4`
							: ctId
								? `Jail ${ctId} IPv4`
								: `IPv4 Network ${getDashedDate()}`;
						objectCreator.type = 'Network(s)';
						objectCreator.ocType = 'ipv4-net';
						objectCreator.open = true;

						return '';
					}
				}}
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.ipv4Gateway.open}
				label="IPv4 Gateway"
				bind:value={comboBoxes.ipv4Gateway.value}
				data={generateIPOptions(usable.ipv4Gateway, 'IPv4')}
				classes="flex-1 space-y-1"
				placeholder="Select or type an IPv4 GW"
				width="w-full"
				allowCustom={true}
				disabled={checkBoxes.dhcp}
				topRightButton={{
					icon: 'icon-[oui--generate]',
					tooltip: 'Create new IPv4 Gateway',
					function: async () => {
						objectCreator.name = name
							? `${name} IPv4 GW`
							: ctId
								? `Jail ${ctId} IPv4 GW`
								: `IPv4 Gateway ${getDashedDate()}`;
						objectCreator.type = 'Host(s)';
						objectCreator.ocType = 'ipv4-gw';
						objectCreator.open = true;

						return '';
					}
				}}
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.ipv6.open}
				label="IPv6 Network"
				bind:value={comboBoxes.ipv6.value}
				data={generateNetworkOptions(usable.ipv6, 'IPv6')}
				classes="flex-1 space-y-1"
				placeholder="Select or type an IPv6 CIDR"
				width="w-full"
				allowCustom={true}
				disabled={checkBoxes.slaac}
				topRightButton={{
					icon: 'icon-[oui--generate]',
					tooltip: 'Create new IPv6 Network',
					function: async () => {
						objectCreator.name = name
							? `${name} IPv6`
							: ctId
								? `Jail ${ctId} IPv6`
								: `IPv6 Network ${getDashedDate()}`;
						objectCreator.type = 'Network(s)';
						objectCreator.ocType = 'ipv6-net';
						objectCreator.open = true;

						return '';
					}
				}}
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.ipv6Gateway.open}
				label="IPv6 Gateway"
				bind:value={comboBoxes.ipv6Gateway.value}
				data={generateIPOptions(usable.ipv6Gateway, 'IPv6')}
				classes="flex-1 space-y-1"
				placeholder="Select or type an IPv6 GW"
				width="w-full"
				allowCustom={true}
				disabled={checkBoxes.slaac}
				topRightButton={{
					icon: 'icon-[oui--generate]',
					tooltip: 'Create new IPv6 Gateway',
					function: async () => {
						objectCreator.name = name
							? `${name} IPv6 GW`
							: ctId
								? `Jail ${ctId} IPv6 GW`
								: `IPv6 Gateway ${getDashedDate()}`;
						objectCreator.type = 'Host(s)';
						objectCreator.ocType = 'ipv6-gw';
						objectCreator.open = true;

						return '';
					}
				}}
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.mac.open}
				label="MAC Address"
				bind:value={comboBoxes.mac.value}
				data={generateMACOptions(usable.macs)}
				classes="flex-1 space-y-1"
				placeholder="Select or type a MAC"
				width="w-full"
				allowCustom={true}
				topRightButton={{
					icon: 'icon-[oui--generate]',
					tooltip: 'Create new MAC Address',
					function: async () => {
						objectCreator.name = name
							? `${name} MAC`
							: ctId
								? `Jail ${ctId} MAC`
								: `MAC ${getDashedDate()}`;
						objectCreator.type = 'MAC(s)';
						objectCreator.ocType = 'mac';
						objectCreator.open = true;

						return '';
					}
				}}
			></CustomComboBox>

			<CustomValueInput
				label="VLAN"
				placeholder="0"
				bind:value={vlan}
				classes="flex-1 space-y-1"
				type="number"
			/>
		</div>

		{#if jailType === 'freebsd'}
			<div class="mt-1 flex flex-row gap-4">
				<CustomCheckbox
					label="DHCP"
					bind:checked={checkBoxes.dhcp}
					classes="flex items-center gap-2"
				></CustomCheckbox>
				<CustomCheckbox
					label="SLAAC"
					bind:checked={checkBoxes.slaac}
					classes="flex items-center gap-2"
				></CustomCheckbox>
			</div>
		{/if}
	{:else if nwSwitch === 'Inherit'}
		<div class="mt-1 flex flex-row gap-4">
			<CustomCheckbox label="IPv4" bind:checked={inheritIPv4} classes="flex items-center gap-2"
			></CustomCheckbox>
			<CustomCheckbox label="IPv6" bind:checked={inheritIPv6} classes="flex items-center gap-2"
			></CustomCheckbox>
		</div>
	{/if}

	<div class="mt-1">
		<CustomCheckbox
			label="Populate DNS Resolver Configuration"
			bind:checked={checkBoxes.resolvConf}
			classes="flex items-center gap-2"
		/>

		{#if checkBoxes.resolvConf}
			<div class="mt-2 space-y-2">
				<SimpleSelect
					label="DNS Preset"
					placeholder="Select DNS"
					options={[
						{ value: 'manual', label: 'Manual' },
						{ value: 'cloudflare', label: 'Cloudflare' },
						{ value: 'google', label: 'Google' },
						{ value: 'quad9', label: 'Quad9' }
					]}
					value={selectedDnsPreset}
					onChange={(v) => {
						selectedDnsPreset = v;

						if (v === 'manual') {
							resolvConf = '';
							return;
						}

						resolvConf = dnsConfigPresets(v as unknown as 'cloudflare' | 'google' | 'quad9');
					}}
				/>

				<CustomValueInput
					label=""
					placeholder="nameserver 1.1.1.1\nnameserver 8.8.8.8\nsearch localdomain"
					type="textarea"
					textAreaClasses="min-h-28 text-xs/6"
					bind:value={resolvConf}
					classes="flex-1 space-y-1 text-xs/6"
				/>
			</div>
		{/if}
	</div>
</div>

{#if objectCreator.open}
	<NetworkObjectCreator
		bind:open={objectCreator.open}
		bind:prefill={objectCreator}
		{networkObjects}
		edit={false}
		afterChange={() => {
			refetch = false;
			refetch = true;
			setTimeout(() => {
				if (objectCreator.ocType === 'ipv4-net') {
					const createdObj = networkObjects.find((obj) => obj.id === Number(objectCreator.value));
					if (createdObj) {
						comboBoxes.ipv4.value = createdObj.id.toString();
					}
				} else if (objectCreator.ocType === 'ipv4-gw') {
					const createdObj = networkObjects.find((obj) => obj.id === Number(objectCreator.value));
					if (createdObj) {
						comboBoxes.ipv4Gateway.value = createdObj.id.toString();
					}
				} else if (objectCreator.ocType === 'ipv6-net') {
					const createdObj = networkObjects.find((obj) => obj.id === Number(objectCreator.value));
					if (createdObj) {
						comboBoxes.ipv6.value = createdObj.id.toString();
					}
				} else if (objectCreator.ocType === 'ipv6-gw') {
					const createdObj = networkObjects.find((obj) => obj.id === Number(objectCreator.value));
					if (createdObj) {
						comboBoxes.ipv6Gateway.value = createdObj.id.toString();
					}
				} else if (objectCreator.ocType === 'mac') {
					const createdObj = networkObjects.find((obj) => obj.id === Number(objectCreator.value));
					if (createdObj) {
						comboBoxes.mac.value = createdObj.id.toString();
					}
				}
			}, 500);
		}}
	/>
{/if}
