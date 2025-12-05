<script lang="ts">
	import type { Jail, JailNetwork } from '$lib/types/jail/jail';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import type { NetworkObject } from '$lib/types/network/object';
	import {
		generateIPOptions,
		generateMACOptions,
		generateNetworkOptions
	} from '$lib/utils/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import { toast } from 'svelte-sonner';
	import { addNetwork } from '$lib/api/jail/jail';
	import { parseNumberOrZero } from '$lib/utils/string';

	interface Props {
		open: boolean;
		jail: Jail;
		networkObjects: NetworkObject[];
		networkSwitches: SwitchList;
		reload: boolean;
	}

	let {
		open = $bindable(),
		jail,
		reload = $bindable(),
		networkObjects,
		networkSwitches
	}: Props = $props();

	let hasDefaultGateway = $derived(jail.networks.some((net) => net.defaultGateway));
	let options = {
		name: '',
		ipv4: '',
		ipv4gw: '',
		ipv6: '',
		ipv6gw: '',
		dhcp: false,
		slaac: false,
		defaultGateway: false
	};

	let properties = $state(options);
	let comboBoxes = $state({
		sw: {
			open: false,
			value: ''
		},
		mac: {
			open: false,
			value: '',
			options: generateMACOptions(networkObjects)
		},
		ipv4: {
			open: false,
			value: '',
			options: generateNetworkOptions(networkObjects, 'ipv4')
		},
		ipv6: {
			open: false,
			value: '',
			options: generateNetworkOptions(networkObjects, 'ipv6')
		},
		ipv4Gw: {
			open: false,
			value: '',
			options: generateIPOptions(networkObjects, 'ipv4')
		},
		ipv6Gw: {
			open: false,
			value: '',
			options: generateIPOptions(networkObjects, 'ipv6')
		}
	});

	function resetComboBoxes() {
		comboBoxes.mac.value = '';
		comboBoxes.ipv4.value = '';
		comboBoxes.ipv4Gw.value = '';
		comboBoxes.ipv6.value = '';
		comboBoxes.ipv6Gw.value = '';
	}

	$effect(() => {
		if (properties.dhcp) {
			comboBoxes.ipv4.value = '';
			comboBoxes.ipv4Gw.value = '';
			properties.ipv4 = '';
			properties.ipv4gw = '';
		}

		if (properties.slaac) {
			comboBoxes.ipv6.value = '';
			comboBoxes.ipv6Gw.value = '';
			properties.ipv6 = '';
			properties.ipv6gw = '';
		}

		if (properties.dhcp && properties.slaac) {
			properties.defaultGateway = false;
		}
	});

	async function addSwitch() {
		let toastOptions = {
			position: 'bottom-center' as const
		};

		if (!jail) return;
		if (!properties.name || properties.name.trim() === '') {
			toast.error('Name is required', toastOptions);
			return;
		}

		if (jail.networks.some((net) => net.name === properties.name)) {
			toast.error('Network name already exists', toastOptions);
			return;
		}

		if (!comboBoxes.sw.value || comboBoxes.sw.value.trim() === '') {
			toast.error('Switch is required', toastOptions);
			return;
		}

		if (properties.defaultGateway && hasDefaultGateway) {
			toast.error('Default gateway already exists', toastOptions);
			return;
		}

		const response = await addNetwork(
			jail.ctId,
			properties.name,
			comboBoxes.sw.value,
			parseNumberOrZero(comboBoxes.mac.value),
			parseNumberOrZero(comboBoxes.ipv4.value),
			parseNumberOrZero(comboBoxes.ipv4Gw.value),
			parseNumberOrZero(comboBoxes.ipv6.value),
			parseNumberOrZero(comboBoxes.ipv6Gw.value),
			properties.dhcp,
			properties.slaac,
			properties.defaultGateway
		);

		reload = true;
		if (response.error) {
			toast.error('Failed to add network', toastOptions);
		} else {
			toast.success('Network added successfully', toastOptions);
			open = false;
			properties = options;
			resetComboBoxes();
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="min-w-[600px]">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<span class="icon-[mdi--network] mr-2 h-5 w-5"></span>
					<span>New Network</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Reset'}
						onclick={() => {
							properties = options;
							resetComboBoxes();
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							open = false;
							properties = options;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-3 gap-4">
			<CustomValueInput
				label="Name"
				placeholder="Primary Network"
				bind:value={properties.name}
				classes="flex-1 space-y-1"
			/>

			<CustomComboBox
				bind:open={comboBoxes.sw.open}
				label="Switch"
				placeholder="Select Switch"
				bind:value={comboBoxes.sw.value}
				data={[...(networkSwitches.standard || []), ...(networkSwitches.manual || [])].map(
					(sw) => ({
						label: sw.name,
						value: sw.name
					})
				)}
				classes="flex-1 space-y-1"
				triggerWidth="w-full"
				width="w-full"
			/>

			<CustomComboBox
				bind:open={comboBoxes.mac.open}
				label="MAC Address"
				placeholder="Select MAC Address"
				bind:value={comboBoxes.mac.value}
				data={comboBoxes.mac.options}
				classes="flex-1 space-y-1"
				triggerWidth="w-full"
				width="w-full"
			/>
		</div>

		{#if jail.type === 'freebsd' || jail.type === 'linux'}
			<div class="grid grid-cols-2 gap-4">
				<CustomComboBox
					bind:open={comboBoxes.ipv4.open}
					label="IPv4 Address"
					placeholder="Select IPv4 Address"
					bind:value={comboBoxes.ipv4.value}
					data={comboBoxes.ipv4.options}
					classes="flex-1 space-y-1 w-full"
					triggerWidth="w-full"
					width="w-full"
					disabled={properties.dhcp}
				/>

				<CustomComboBox
					bind:open={comboBoxes.ipv4Gw.open}
					label="IPv4 Gateway"
					placeholder="Select IPv4 Gateway"
					bind:value={comboBoxes.ipv4Gw.value}
					data={comboBoxes.ipv4Gw.options}
					classes="flex-1 space-y-1"
					triggerWidth="w-full"
					width="w-full"
					disabled={properties.dhcp}
				/>

				<CustomComboBox
					bind:open={comboBoxes.ipv6.open}
					label="IPv6 Address"
					placeholder="Select IPv6 Address"
					bind:value={comboBoxes.ipv6.value}
					data={comboBoxes.ipv6.options}
					classes="flex-1 space-y-1"
					triggerWidth="w-full"
					width="w-full"
					disabled={properties.slaac}
				/>

				<CustomComboBox
					bind:open={comboBoxes.ipv6Gw.open}
					label="IPv6 Gateway"
					placeholder="Select IPv6 Gateway"
					bind:value={comboBoxes.ipv6Gw.value}
					data={comboBoxes.ipv6Gw.options}
					classes="flex-1 space-y-1"
					triggerWidth="w-full"
					width="w-full"
					disabled={properties.slaac}
				/>
			</div>

			<div class="mt-2 flex items-center space-x-4">
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

				{#if !(properties.dhcp && properties.slaac)}
					<CustomCheckbox
						bind:checked={properties.defaultGateway}
						label="Default Gateway"
						classes="flex items-center gap-2"
						disabled={hasDefaultGateway}
					/>
				{/if}
			</div>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={addSwitch} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
