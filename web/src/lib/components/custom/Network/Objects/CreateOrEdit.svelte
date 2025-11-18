<script lang="ts">
	import { createNetworkObject, updateNetworkObject } from '$lib/api/network/object';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBoxBindable from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { NetworkObject } from '$lib/types/network/object';
	import { handleAPIError } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import {
		generateUnicastMAC,
		isValidDUID,
		isValidIPv4,
		isValidIPv6,
		isValidMACAddress
	} from '$lib/utils/string';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		edit: boolean;
		id?: number;
		networkObjects: NetworkObject[];
		afterChange: () => void;
	}

	let { open = $bindable(), edit = false, id, networkObjects, afterChange }: Props = $props();
	let editingObject: NetworkObject | null = $derived.by(() => {
		if (edit && id) {
			const obj = networkObjects.find((o) => o.id === id);
			if (obj) {
				return obj;
			}
		}

		return null;
	});

	let oType = $derived.by(() => {
		if (editingObject) {
			switch (editingObject.type) {
				case 'Host':
					return 'Host(s)';
				case 'Network':
					return 'Network(s)';
				case 'Mac':
					return 'MAC(s)';
				case 'DUID':
					return 'DUID(s)';
				default:
					return '';
			}
		}
		return '';
	});

	let optionsSelected = $derived.by(() => {
		if (editingObject && editingObject.entries && editingObject.entries.length > 0) {
			return editingObject.entries.map((e) => e.value);
		}

		return [];
	});

	let options = $derived({
		name: editingObject ? editingObject.name : '',
		type: {
			combobox: {
				open: false,
				value: editingObject ? oType : '',
				options: generateComboboxOptions(['Host(s)', 'Network(s)', 'MAC(s)', 'DUID(s)'] as string[])
			}
		},
		hosts: {
			combobox: {
				open: false,
				value: editingObject ? optionsSelected : ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		networks: {
			combobox: {
				open: false,
				value: editingObject ? optionsSelected : ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		macs: {
			combobox: {
				open: false,
				value: editingObject ? optionsSelected : ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		duids: {
			combobox: {
				open: false,
				value: editingObject ? optionsSelected : ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		}
	});

	/* svelte-ignore state_referenced_locally */
	let properties = $state(options);

	async function basicTests() {
		let error = '';

		if (properties.name === '') {
			error = 'Name is required';
		}

		if (properties.type.combobox.value === '') {
			error = 'Type is required';
		}

		if (
			properties.type.combobox.value === 'Host(s)' &&
			properties.hosts.combobox.value.length === 0
		) {
			error = 'At least one host must be selected';
		} else if (
			properties.type.combobox.value === 'Network(s)' &&
			properties.networks.combobox.value.length === 0
		) {
			error = 'At least one network must be selected';
		} else if (
			properties.type.combobox.value === 'MAC(s)' &&
			properties.macs.combobox.value.length === 0
		) {
			error = 'At least one MAC must be selected';
		} else if (
			properties.type.combobox.value === 'DUID(s)' &&
			properties.duids.combobox.value.length === 0
		) {
			error = 'At least one DUID must be selected';
		}

		let values = [] as string[];

		if (properties.type.combobox.value === 'Host(s)') {
			const hosts = Array.from(new Set(properties.hosts.combobox.value));
			properties.hosts.combobox.value = hosts;

			let hasIPv4 = false;
			let hasIPv6 = false;

			for (const host of hosts) {
				if (isValidIPv4(host)) {
					hasIPv4 = true;
				} else if (isValidIPv6(host)) {
					hasIPv6 = true;
				} else {
					error = `Invalid host IP: ${host}`;
					break;
				}
			}

			if (!error && hasIPv4 && hasIPv6) {
				error = 'Cannot mix IPv4 and IPv6 addresses';
			}

			values = hosts;
			return values;
		}

		if (properties.type.combobox.value === 'Network(s)') {
			const networks = Array.from(new Set(properties.networks.combobox.value));
			properties.networks.combobox.value = networks;

			let hasIPv4 = false;
			let hasIPv6 = false;

			for (const net of networks) {
				if (isValidIPv4(net, true)) {
					hasIPv4 = true;
				} else if (isValidIPv6(net, true)) {
					hasIPv6 = true;
				} else {
					error = `Invalid network CIDR: ${net}`;
					break;
				}
			}

			if (!error && hasIPv4 && hasIPv6) {
				error = 'Cannot mix IPv4 and IPv6 networks';
			}

			values = networks;
			return values;
		}

		if (properties.type.combobox.value === 'MAC(s)') {
			const macs = Array.from(new Set(properties.macs.combobox.value));
			properties.macs.combobox.value = macs;

			for (const mac of macs) {
				if (!isValidMACAddress(mac)) {
					error = `Invalid MAC address: ${mac}`;
					break;
				}
			}

			values = macs;
			return values;
		}

		if (properties.type.combobox.value === 'DUID(s)') {
			const duids = Array.from(new Set(properties.duids.combobox.value));
			properties.duids.combobox.value = duids;

			for (const duid of duids) {
				if (duid.trim() === '') {
					error = `DUID cannot be empty`;
					break;
				}

				if (isValidDUID(duid)) {
					continue;
				} else {
					error = `Invalid DUID: ${duid}`;
					break;
				}
			}

			values = duids;
			return values;
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		return true;
	}

	function getOType() {
		let oType = '';

		switch (properties.type.combobox.value) {
			case 'Host(s)':
				oType = 'Host';
				break;
			case 'Network(s)':
				oType = 'Network';
				break;
			case 'MAC(s)':
				oType = 'Mac';
				break;
			case 'DUID(s)':
				oType = 'DUID';
				break;
			default:
				oType = properties.type.combobox.value;
		}

		return oType;
	}

	async function create() {
		const values = await basicTests();
		if (!values) {
			return;
		}

		let oType = getOType();

		const response = await createNetworkObject(properties.name, oType, values as string[]);
		afterChange();
		if (response.error) {
			handleAPIError(response);

			let message = 'Failed to create network object';

			if (response.error.startsWith('object_with_name_already')) {
				message = 'Object with this name already exists';
			}

			toast.error(message, {
				position: 'bottom-center'
			});

			return;
		} else {
			toast.success('Created object', {
				position: 'bottom-center'
			});

			open = false;
		}
	}

	async function editObject() {
		const values = await basicTests();
		if (!values) {
			return;
		}

		let oType = getOType();

		const response = await updateNetworkObject(
			editingObject?.id || 0,
			properties.name,
			oType,
			values as string[]
		);
		afterChange();

		if (response.error) {
			handleAPIError(response);
			let error = '';

			if (response.error.startsWith('object_with_name_already')) {
				error = 'Object with this name already exists';
			} else if (response.error.includes('please ensure only one IP is provided')) {
				error = 'Host object used in switch, only one IP is allowed';
			} else if (response.error.includes('no_detected_changes')) {
				error = 'No changes detected';
			} else if (response.error.includes('cannot_change_object_type')) {
				error = 'Cannot change type of object that is in use';
			} else if (response.error.includes('cannot_change_object_of_active_vm')) {
				error = 'Cannot change object of active VM';
			} else {
				error = 'Failed to update network object';
			}

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
			}

			open = false;
		} else {
			toast.success('Updated object', {
				position: 'bottom-center'
			});

			open = false;
		}
	}

	function addRandomMAC() {
		const newMac = generateUnicastMAC();
		properties.macs.combobox.options.push({ label: newMac, value: newMac });
		properties.macs.combobox.value.push(newMac);
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center">
						<span class="icon-[clarity--objects-solid] mr-2 h-6 w-6"></span>

						{#if editingObject}
							<span class="text-lg font-semibold">Edit Object - {editingObject.name}</span>
						{:else}
							<span class="text-lg font-semibold">Create Object</span>
						{/if}
					</div>
				</Dialog.Title>
			</Dialog.Header>

			<div class="flex items-center gap-0.5">
				<Button
					size="sm"
					variant="link"
					class="h-4"
					title={'Reset'}
					onclick={() => (properties = options)}
				>
					<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">{'Reset'}</span>
				</Button>
				<Button size="sm" variant="link" class="h-4" title={'Close'} onclick={() => (open = false)}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">{'Close'}</span>
				</Button>
			</div>
		</div>

		<div class="flex gap-4">
			<CustomValueInput
				label={'Name'}
				placeholder="Windows"
				bind:value={properties.name}
				classes="flex-1 space-y-1.5"
				type="text"
			/>

			<ComboBox
				bind:open={properties.type.combobox.open}
				label={'Type'}
				bind:value={properties.type.combobox.value}
				data={properties.type.combobox.options}
				classes="flex-1 space-y-1"
				placeholder="Select type"
				width="w-3/4"
			></ComboBox>
		</div>

		{#if properties.type.combobox.value !== ''}
			<div class="flex gap-4 overflow-auto">
				{#if properties.type.combobox.value === 'Host(s)' || properties.type.combobox.value === 'Network(s)' || properties.type.combobox.value === 'MAC(s)' || properties.type.combobox.value === 'DUID(s)'}
					{#if properties.type.combobox.value === 'Host(s)'}
						<ComboBoxBindable
							bind:open={properties.hosts.combobox.open}
							label={'Hosts'}
							bind:value={properties.hosts.combobox.value}
							data={properties.hosts.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="Select hosts"
							width="w-full"
							multiple={true}
						></ComboBoxBindable>
					{:else if properties.type.combobox.value === 'Network(s)'}
						<ComboBoxBindable
							bind:open={properties.networks.combobox.open}
							label={'Networks'}
							bind:value={properties.networks.combobox.value}
							data={properties.networks.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="Select networks"
							width="w-full"
							multiple={true}
						></ComboBoxBindable>
					{:else if properties.type.combobox.value === 'MAC(s)'}
						<div class="flex w-full items-center space-x-2">
							<ComboBoxBindable
								bind:open={properties.macs.combobox.open}
								label={'MACs'}
								bind:value={properties.macs.combobox.value}
								data={properties.macs.combobox.options}
								classes="flex-1 space-y-1 w-full"
								placeholder="Select MACs"
								width="w-full"
								multiple={true}
							></ComboBoxBindable>

							<div class="mt-1 space-y-1">
								<Label class="invisible">1</Label>
								<Button size="sm" class="h-9.5" onclick={addRandomMAC}>
									<span class="icon-[fad--random-2dice] h-5 w-5"></span>
								</Button>
							</div>
						</div>
					{:else if properties.type.combobox.value === 'DUID(s)'}
						<ComboBoxBindable
							bind:open={properties.duids.combobox.open}
							label={'DUIDs'}
							bind:value={properties.duids.combobox.value}
							data={properties.duids.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="Select DUIDs"
							width="w-full"
							multiple={true}
						></ComboBoxBindable>
					{/if}
				{/if}
			</div>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if edit}
					<Button onclick={editObject} type="submit" size="sm">Save</Button>
				{:else}
					<Button onclick={create} type="submit" size="sm">Create</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
