<script lang="ts">
	import { createNetworkObject, updateNetworkObject } from '$lib/api/network/object';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBoxBindable from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { NetworkObject } from '$lib/types/network/object';
	import { handleAPIError } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { objectLists } from '$lib/utils/network/object';
	import {
		generateUnicastMAC,
		isValidDUID,
		isValidIPv4,
		isValidIPv6,
		isValidMACAddress,
		isValidPortNumber
	} from '$lib/utils/string';

	const predefinedListOptions = [
		...objectLists('firehol'),
		...objectLists('cloudflare'),
		...objectLists('abusedb')
	];
	import { toast } from 'svelte-sonner';
	import { SvelteSet } from 'svelte/reactivity';

	const objectTypeOptions = [
		'Host(s)',
		'Network(s)',
		'Port(s)',
		'MAC(s)',
		'DUID(s)',
		'FQDN(s)',
		'List(s)'
	];

	function isValidFQDN(value: string): boolean {
		const v = value.trim();
		if (!v || v.length > 253) return false;
		const labels = v.split('.');
		if (labels.length < 2) return false;
		return labels.every(
			(label) =>
				/^[a-zA-Z0-9-]{1,63}$/.test(label) && !label.startsWith('-') && !label.endsWith('-')
		);
	}

	function isValidListSourceURL(value: string): boolean {
		try {
			const parsed = new URL(value.trim());
			return parsed.protocol === 'http:' || parsed.protocol === 'https:';
		} catch {
			return false;
		}
	}

	function isValidPortToken(value: string): boolean {
		const v = value.trim();
		if (!v || /[\s,]/.test(v)) return false;

		if (v.includes(':')) {
			const parts = v.split(':');
			if (parts.length !== 2) return false;
			const start = parts[0].trim();
			const end = parts[1].trim();
			if (!/^\d+$/.test(start) || !/^\d+$/.test(end)) return false;
			if (!isValidPortNumber(start) || !isValidPortNumber(end)) return false;
			return Number(start) <= Number(end);
		}

		if (!/^\d+$/.test(v)) return false;
		return isValidPortNumber(v);
	}

	interface Props {
		open: boolean;
		edit: boolean;
		id?: number;
		networkObjects: NetworkObject[];
		afterChange: () => void;
		prefill?: {
			name: string;
			type: string;
			value: string;
		};
	}

	let {
		open = $bindable(),
		edit = false,
		id,
		networkObjects,
		afterChange,
		prefill = $bindable()
	}: Props = $props();

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
				case 'Port':
					return 'Port(s)';
				case 'Mac':
					return 'MAC(s)';
				case 'DUID':
					return 'DUID(s)';
				case 'FQDN':
					return 'FQDN(s)';
				case 'List':
					return 'List(s)';
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
		name: editingObject ? editingObject.name : prefill ? prefill.name : '',
		type: {
			combobox: {
				open: false,
				value: editingObject ? oType : prefill ? prefill.type : '',
				options: generateComboboxOptions(objectTypeOptions as string[])
			}
		},
		hosts: {
			combobox: {
				open: false,
				value: editingObject
					? optionsSelected
					: prefill
						? prefill.type === 'Host(s)'
							? optionsSelected
							: ([] as string[])
						: ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		networks: {
			combobox: {
				open: false,
				value: editingObject
					? optionsSelected
					: prefill
						? prefill.type === 'Network(s)'
							? optionsSelected
							: ([] as string[])
						: ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		ports: {
			combobox: {
				open: false,
				value: editingObject
					? optionsSelected
					: prefill
						? prefill.type === 'Port(s)'
							? optionsSelected
							: ([] as string[])
						: ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		macs: {
			combobox: {
				open: false,
				value: editingObject
					? optionsSelected
					: prefill
						? prefill.type === 'MAC(s)'
							? optionsSelected
							: ([] as string[])
						: ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		duids: {
			combobox: {
				open: false,
				value: editingObject
					? optionsSelected
					: prefill
						? prefill.type === 'DUID(s)'
							? optionsSelected
							: ([] as string[])
						: ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		fqdns: {
			combobox: {
				open: false,
				value: editingObject
					? optionsSelected
					: prefill
						? prefill.type === 'FQDN(s)'
							? optionsSelected
							: ([] as string[])
						: ([] as string[]),
				options: editingObject
					? [...generateComboboxOptions(optionsSelected)]
					: ([] as { label: string; value: string }[])
			}
		},
		lists: {
			combobox: {
				open: false,
				value: editingObject
					? optionsSelected
					: prefill
						? prefill.type === 'List(s)'
							? optionsSelected
							: ([] as string[])
						: ([] as string[]),
				options: editingObject
					? (() => {
							const predefinedByValue = new Map(predefinedListOptions.map((p) => [p.value, p]));
							const result: { label: string; value: string }[] = [];
							const addedValues = new SvelteSet<string>();
							for (const url of optionsSelected) {
								result.push(predefinedByValue.get(url) ?? { label: url, value: url });
								addedValues.add(url);
							}
							for (const p of predefinedListOptions) {
								if (!addedValues.has(p.value)) result.push(p);
							}
							return result;
						})()
					: [...predefinedListOptions]
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
			properties.type.combobox.value === 'Port(s)' &&
			properties.ports.combobox.value.length === 0
		) {
			error = 'At least one port must be selected';
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
		} else if (
			properties.type.combobox.value === 'FQDN(s)' &&
			properties.fqdns.combobox.value.length === 0
		) {
			error = 'At least one FQDN must be selected';
		} else if (
			properties.type.combobox.value === 'List(s)' &&
			properties.lists.combobox.value.length === 0
		) {
			error = 'At least one list URL must be selected';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
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

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
				return;
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

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
				return;
			}

			values = networks;
			return values;
		}

		if (properties.type.combobox.value === 'Port(s)') {
			const ports = Array.from(
				new Set(properties.ports.combobox.value.map((v) => v.trim()).filter(Boolean))
			);
			properties.ports.combobox.value = ports;

			if (ports.length === 0) {
				toast.error('At least one port must be selected', {
					position: 'bottom-center'
				});
				return;
			}

			for (const port of ports) {
				if (!isValidPortToken(port)) {
					error = `Invalid port token: ${port}`;
					break;
				}
			}

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
				return;
			}

			values = ports;
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

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
				return;
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

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
				return;
			}

			values = duids;
			return values;
		}

		if (properties.type.combobox.value === 'FQDN(s)') {
			const fqdns = Array.from(new Set(properties.fqdns.combobox.value.map((v) => v.trim())));
			properties.fqdns.combobox.value = fqdns;

			for (const fqdn of fqdns) {
				if (!isValidFQDN(fqdn)) {
					error = `Invalid FQDN: ${fqdn}`;
					break;
				}
			}

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
				return;
			}

			values = fqdns;
			return values;
		}

		if (properties.type.combobox.value === 'List(s)') {
			const lists = Array.from(new Set(properties.lists.combobox.value.map((v) => v.trim())));
			properties.lists.combobox.value = lists;

			for (const entry of lists) {
				if (!isValidListSourceURL(entry)) {
					error = `Invalid list source URL: ${entry}`;
					break;
				}
			}

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
				return;
			}

			values = lists;
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
			case 'Port(s)':
				oType = 'Port';
				break;
			case 'MAC(s)':
				oType = 'Mac';
				break;
			case 'DUID(s)':
				oType = 'DUID';
				break;
			case 'FQDN(s)':
				oType = 'FQDN';
				break;
			case 'List(s)':
				oType = 'List';
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

		const response = (await createNetworkObject(properties.name, oType, values as string[])) as
			| APIResponse
			| number;

		if (typeof response !== 'number') {
			handleAPIError(response);

			let message = 'Failed to create network object';

			if (
				typeof response.error === 'string' &&
				response.error.startsWith('object_with_name_already')
			) {
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

			if (prefill && response && !isNaN(response as number)) {
				prefill.value = (response as number).toString();
			}

			afterChange();
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

		if (response.error) {
			handleAPIError(response);
			let error = '';

			if (!Array.isArray(response.error) && response.error.startsWith('object_with_name_already')) {
				error = 'Object with this name already exists';
			} else if (
				!Array.isArray(response.error) &&
				response.error.includes('please ensure only one IP is provided')
			) {
				error = 'Host object used in switch, only one IP is allowed';
			} else if (!Array.isArray(response.error) && response.error.includes('no_detected_changes')) {
				error = 'No changes detected';
			} else if (
				!Array.isArray(response.error) &&
				response.error.includes('cannot_change_object_type')
			) {
				error = 'Cannot change type of object that is in use';
			} else if (
				!Array.isArray(response.error) &&
				response.error.includes('cannot_change_object_of_active_vm')
			) {
				error = 'Cannot change object of active VM';
			} else {
				error = 'Failed to update network object';
			}

			if (error) {
				toast.error(error, {
					position: 'bottom-center'
				});
			}
		} else {
			toast.success('Updated object', {
				position: 'bottom-center'
			});

			afterChange();
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
					title="Reset"
					onclick={() => (properties = options)}
				>
					<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Reset</span>
				</Button>
				<Button size="sm" variant="link" class="h-4" title="Close" onclick={() => (open = false)}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">Close</span>
				</Button>
			</div>
		</div>

		<div class="flex gap-4">
			<CustomValueInput
				label="Name"
				placeholder="Windows"
				bind:value={properties.name}
				classes="flex-1 space-y-1.5"
				type="text"
			/>

			<ComboBox
				bind:open={properties.type.combobox.open}
				label="Type"
				bind:value={properties.type.combobox.value}
				data={properties.type.combobox.options}
				classes="flex-1 space-y-1"
				placeholder="Select type"
				width="w-3/4"
			></ComboBox>
		</div>

		{#if properties.type.combobox.value !== ''}
			<div class="flex gap-4 overflow-auto">
				{#if properties.type.combobox.value === 'Host(s)' || properties.type.combobox.value === 'Network(s)' || properties.type.combobox.value === 'Port(s)' || properties.type.combobox.value === 'MAC(s)' || properties.type.combobox.value === 'DUID(s)' || properties.type.combobox.value === 'FQDN(s)' || properties.type.combobox.value === 'List(s)'}
					{#if properties.type.combobox.value === 'Host(s)'}
						<ComboBoxBindable
							bind:open={properties.hosts.combobox.open}
							label="Hosts"
							bind:value={properties.hosts.combobox.value}
							data={properties.hosts.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="192.168.1.1, fd01:beef::1"
							width="w-full"
							multiple={true}
						></ComboBoxBindable>
					{:else if properties.type.combobox.value === 'Network(s)'}
						<ComboBoxBindable
							bind:open={properties.networks.combobox.open}
							label="Networks"
							bind:value={properties.networks.combobox.value}
							data={properties.networks.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="192.168.1.128/24, fd:beef::/64"
							width="w-full"
							multiple={true}
						></ComboBoxBindable>
					{:else if properties.type.combobox.value === 'Port(s)'}
						<ComboBoxBindable
							bind:open={properties.ports.combobox.open}
							label="Ports"
							bind:value={properties.ports.combobox.value}
							data={properties.ports.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="80 or 8000:9000 (one token per entry)"
							width="w-full"
							multiple={true}
						></ComboBoxBindable>
					{:else if properties.type.combobox.value === 'MAC(s)'}
						<div class="flex w-full items-center space-x-2">
							<ComboBoxBindable
								bind:open={properties.macs.combobox.open}
								label="MACs"
								bind:value={properties.macs.combobox.value}
								data={properties.macs.combobox.options}
								classes="flex-1 space-y-1 w-full"
								placeholder="00:1A:2B:3C:4D:5E"
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
							label="DUIDs"
							bind:value={properties.duids.combobox.value}
							data={properties.duids.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="00:01:00:01:23:45:67:89:AB:CD:EF:01"
							width="w-full"
							multiple={true}
						></ComboBoxBindable>
					{:else if properties.type.combobox.value === 'FQDN(s)'}
						<ComboBoxBindable
							bind:open={properties.fqdns.combobox.open}
							label="FQDNs"
							bind:value={properties.fqdns.combobox.value}
							data={properties.fqdns.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="example.com"
							width="w-full"
							multiple={true}
						></ComboBoxBindable>
					{:else if properties.type.combobox.value === 'List(s)'}
						<ComboBoxBindable
							bind:open={properties.lists.combobox.open}
							label="List URLs"
							bind:value={properties.lists.combobox.value}
							data={properties.lists.combobox.options}
							classes="flex-1 space-y-1"
							placeholder="https://example.com/blocklist.txt"
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
