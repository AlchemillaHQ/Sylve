<script lang="ts">
	import type { Jail } from '$lib/types/jail/jail';
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
	import { addNetwork, updateNetwork as updateNetworkAPI } from '$lib/api/jail/jail';
	import { parseNumberOrZero } from '$lib/utils/string';
	import { watch } from 'runed';

	interface Props {
		open: boolean;
		jail: Jail;
		networkObjects: NetworkObject[];
		networkSwitches: SwitchList;
		reload: boolean;
		networkId: number | null;
	}

	let {
		open = $bindable(),
		jail,
		reload = $bindable(),
		networkObjects,
		networkSwitches,
		networkId
	}: Props = $props();

	let selectedNetwork = $derived.by(() => {
		if (networkId === null) return null;
		return jail.networks.find((net) => net.id === networkId) || null;
	});

	let selectedSwitchName = $derived.by(() => {
		if (!selectedNetwork) return '';
		if (selectedNetwork.switchType === 'standard') {
			return networkSwitches.standard?.find((sw) => sw.id === selectedNetwork.switchId)?.name || '';
		}

		return networkSwitches.manual?.find((sw) => sw.id === selectedNetwork.switchId)?.name || '';
	});

	let hasDefaultGateway = $derived.by(() => {
		return jail.networks.some(
			(net) => net.defaultGateway && (!selectedNetwork || net.id !== selectedNetwork.id)
		);
	});
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

	$inspect(generateMACOptions(networkObjects));

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

	let editOptions = {
		name: selectedNetwork ? selectedNetwork.name ?? '' : '',
		ipv4:
			selectedNetwork && !selectedNetwork.dhcp && selectedNetwork.ipv4Id
				? selectedNetwork.ipv4Id.toString()
				: '',
		ipv4gw:
			selectedNetwork && !selectedNetwork.dhcp && selectedNetwork.ipv4GwId
				? selectedNetwork.ipv4GwId.toString()
				: '',
		ipv6:
			selectedNetwork && !selectedNetwork.slaac && selectedNetwork.ipv6Id
				? selectedNetwork.ipv6Id.toString()
				: '',
		ipv6gw:
			selectedNetwork && !selectedNetwork.slaac && selectedNetwork.ipv6GwId
				? selectedNetwork.ipv6GwId.toString()
				: '',
		dhcp: selectedNetwork?.dhcp ?? false,
		slaac: selectedNetwork?.slaac ?? false,
		defaultGateway: selectedNetwork?.defaultGateway ?? false
	};

	let editProperties = $state(editOptions);
	let editComboBoxes = $state({
		sw: {
			open: false,
			value: selectedSwitchName
		},
		mac: {
			open: false,
			value: selectedNetwork?.macId ? selectedNetwork.macId.toString() : '',
			options: generateMACOptions(networkObjects)
		},
		ipv4: {
			open: false,
			value: editOptions.ipv4,
			options: generateNetworkOptions(networkObjects, 'ipv4')
		},
		ipv6: {
			open: false,
			value: editOptions.ipv6,
			options: generateNetworkOptions(networkObjects, 'ipv6')
		},
		ipv4Gw: {
			open: false,
			value: editOptions.ipv4gw,
			options: generateIPOptions(networkObjects, 'ipv4')
		},
		ipv6Gw: {
			open: false,
			value: editOptions.ipv6gw,
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

	function resetEditComboBoxes() {
		editComboBoxes.sw.value = selectedSwitchName;
		editComboBoxes.mac.value = selectedNetwork?.macId ? selectedNetwork.macId.toString() : '';
		editComboBoxes.ipv4.value = editOptions.ipv4;
		editComboBoxes.ipv4Gw.value = editOptions.ipv4gw;
		editComboBoxes.ipv6.value = editOptions.ipv6;
		editComboBoxes.ipv6Gw.value = editOptions.ipv6gw;
	}

	watch(
		() => properties.dhcp,
		(dhcp) => {
			if (dhcp) {
				comboBoxes.ipv4.value = '';
				comboBoxes.ipv4Gw.value = '';
				properties.ipv4 = '';
				properties.ipv4gw = '';
			}
		}
	);

	watch(
		() => properties.slaac,
		(slaac) => {
			if (slaac) {
				comboBoxes.ipv6.value = '';
				comboBoxes.ipv6Gw.value = '';
				properties.ipv6 = '';
				properties.ipv6gw = '';
			}
		}
	);

	watch([() => properties.dhcp, () => properties.slaac], ([dhcp, slaac]) => {
		if (dhcp && slaac) {
			properties.defaultGateway = false;
		}
	});

	watch(
		() => editProperties.dhcp,
		(dhcp) => {
			if (dhcp) {
				editComboBoxes.ipv4.value = '';
				editComboBoxes.ipv4Gw.value = '';
				editProperties.ipv4 = '';
				editProperties.ipv4gw = '';
			}
		}
	);

	watch(
		() => editProperties.slaac,
		(slaac) => {
			if (slaac) {
				editComboBoxes.ipv6.value = '';
				editComboBoxes.ipv6Gw.value = '';
				editProperties.ipv6 = '';
				editProperties.ipv6gw = '';
			}
		}
	);

	watch([() => editProperties.dhcp, () => editProperties.slaac], ([dhcp, slaac]) => {
		if (dhcp && slaac) {
			editProperties.defaultGateway = false;
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

		const isLinuxJail = jail.type === 'linux';

		const response = await addNetwork(
			jail.ctId,
			properties.name,
			comboBoxes.sw.value,
			parseNumberOrZero(comboBoxes.mac.value),
			isLinuxJail ? 0 : parseNumberOrZero(comboBoxes.ipv4.value),
			isLinuxJail ? 0 : parseNumberOrZero(comboBoxes.ipv4Gw.value),
			isLinuxJail ? 0 : parseNumberOrZero(comboBoxes.ipv6.value),
			isLinuxJail ? 0 : parseNumberOrZero(comboBoxes.ipv6Gw.value),
			isLinuxJail ? false : properties.dhcp,
			isLinuxJail ? false : properties.slaac,
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

	async function updateSwitch() {
		let toastOptions = {
			position: 'bottom-center' as const
		};

		if (!jail || !selectedNetwork) return;
		if (!editProperties.name || editProperties.name.trim() === '') {
			toast.error('Name is required', toastOptions);
			return;
		}

		if (
			jail.networks.some((net) => net.name === editProperties.name && net.id !== selectedNetwork.id)
		) {
			toast.error('Network name already exists', toastOptions);
			return;
		}

		if (!editComboBoxes.sw.value || editComboBoxes.sw.value.trim() === '') {
			toast.error('Switch is required', toastOptions);
			return;
		}

		if (editProperties.defaultGateway && hasDefaultGateway) {
			toast.error('Default gateway already exists', toastOptions);
			return;
		}

		const isLinuxJail = jail.type === 'linux';

		const response = await updateNetworkAPI(
			selectedNetwork.id,
			editProperties.name,
			editComboBoxes.sw.value,
			parseNumberOrZero(editComboBoxes.mac.value),
			isLinuxJail ? 0 : parseNumberOrZero(editComboBoxes.ipv4.value),
			isLinuxJail ? 0 : parseNumberOrZero(editComboBoxes.ipv4Gw.value),
			isLinuxJail ? 0 : parseNumberOrZero(editComboBoxes.ipv6.value),
			isLinuxJail ? 0 : parseNumberOrZero(editComboBoxes.ipv6Gw.value),
			isLinuxJail ? false : editProperties.dhcp,
			isLinuxJail ? false : editProperties.slaac,
			editProperties.defaultGateway
		);

		reload = true;
		if (response.error) {
			toast.error('Failed to update network', toastOptions);
		} else {
			toast.success('Network updated successfully', toastOptions);
			open = false;
			editProperties = editOptions;
			resetEditComboBoxes();
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="min-w-150">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex items-center justify-between text-left">
				<div class="flex items-center">
					<span class="icon-[mdi--network] mr-2 h-5 w-5"></span>
					<span>{selectedNetwork ? `Edit - ${selectedNetwork.name}` : 'New Network'}</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Reset'}
						onclick={() => {
							if (selectedNetwork) {
								editProperties = editOptions;
								resetEditComboBoxes();
							} else {
								properties = options;
								resetComboBoxes();
							}
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
							if (selectedNetwork) {
								editProperties = editOptions;
							} else {
								properties = options;
							}
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		{#if !selectedNetwork}
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
					data={generateMACOptions(networkObjects)}
					classes="flex-1 space-y-1"
					triggerWidth="w-full"
					width="w-full"
				/>
			</div>

			{#if jail.type === 'freebsd'}
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
		{:else}
			<div class="grid grid-cols-3 gap-4">
				<CustomValueInput
					label="Name"
					placeholder="Primary Network"
					bind:value={editProperties.name}
					classes="flex-1 space-y-1"
				/>

				<CustomComboBox
					bind:open={editComboBoxes.sw.open}
					label="Switch"
					placeholder="Select Switch"
					bind:value={editComboBoxes.sw.value}
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
					bind:open={editComboBoxes.mac.open}
					label="MAC Address"
					placeholder="Select MAC Address"
					bind:value={editComboBoxes.mac.value}
					data={generateMACOptions(networkObjects)}
					classes="flex-1 space-y-1"
					triggerWidth="w-full"
					width="w-full"
				/>
			</div>

			{#if jail.type === 'freebsd'}
				<div class="grid grid-cols-2 gap-4">
					<CustomComboBox
						bind:open={editComboBoxes.ipv4.open}
						label="IPv4 Address"
						placeholder="Select IPv4 Address"
						bind:value={editComboBoxes.ipv4.value}
						data={editComboBoxes.ipv4.options}
						classes="flex-1 space-y-1 w-full"
						triggerWidth="w-full"
						width="w-full"
						disabled={editProperties.dhcp}
					/>

					<CustomComboBox
						bind:open={editComboBoxes.ipv4Gw.open}
						label="IPv4 Gateway"
						placeholder="Select IPv4 Gateway"
						bind:value={editComboBoxes.ipv4Gw.value}
						data={editComboBoxes.ipv4Gw.options}
						classes="flex-1 space-y-1"
						triggerWidth="w-full"
						width="w-full"
						disabled={editProperties.dhcp}
					/>

					<CustomComboBox
						bind:open={editComboBoxes.ipv6.open}
						label="IPv6 Address"
						placeholder="Select IPv6 Address"
						bind:value={editComboBoxes.ipv6.value}
						data={editComboBoxes.ipv6.options}
						classes="flex-1 space-y-1"
						triggerWidth="w-full"
						width="w-full"
						disabled={editProperties.slaac}
					/>

					<CustomComboBox
						bind:open={editComboBoxes.ipv6Gw.open}
						label="IPv6 Gateway"
						placeholder="Select IPv6 Gateway"
						bind:value={editComboBoxes.ipv6Gw.value}
						data={editComboBoxes.ipv6Gw.options}
						classes="flex-1 space-y-1"
						triggerWidth="w-full"
						width="w-full"
						disabled={editProperties.slaac}
					/>
				</div>

				<div class="mt-2 flex items-center space-x-4">
					<CustomCheckbox
						bind:checked={editProperties.dhcp}
						label="DHCP"
						classes="flex items-center gap-2"
					/>

					<CustomCheckbox
						bind:checked={editProperties.slaac}
						label="SLAAC"
						classes="flex items-center gap-2"
					/>

					{#if !(editProperties.dhcp && editProperties.slaac)}
						<CustomCheckbox
							bind:checked={editProperties.defaultGateway}
							label="Default Gateway"
							classes="flex items-center gap-2"
							disabled={hasDefaultGateway}
						/>
					{/if}
				</div>
			{/if}
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button
					onclick={() => {
						if (selectedNetwork) {
							updateSwitch();
						} else {
							addSwitch();
						}
					}}
					type="submit"
					size="sm"
				>
					{selectedNetwork ? 'Save Changes' : 'Save'}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
