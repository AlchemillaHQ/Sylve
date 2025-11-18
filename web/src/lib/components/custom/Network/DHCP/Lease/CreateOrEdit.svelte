<script lang="ts">
	import type { DHCPConfig, DHCPRange, DHCPStaticLease, Leases } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { NetworkObject } from '$lib/types/network/object';
	import {
		generateDUIDOptions,
		generateIPOptions,
		generateMACOptions
	} from '$lib/utils/network/object';
	import { toast } from 'svelte-sonner';
	import { isValidIPv4, validateDnsmasqHostname } from '$lib/utils/string';
	import { createDHCPLease, modifyDHCPLease } from '$lib/api/network/dhcp';
	import { handleAPIError } from '$lib/utils/http';

	interface Props {
		open: boolean;
		reload: boolean;
		networkInterfaces: Iface[];
		networkSwitches: SwitchList;
		dhcpConfig: DHCPConfig;
		dhcpRanges: DHCPRange[];
		dhcpLeases: Leases;
		selectedLease: string | null | number;
		networkObjects: NetworkObject[];
	}

	let {
		open = $bindable(),
		reload = $bindable(),
		networkInterfaces,
		networkSwitches,
		dhcpRanges,
		dhcpConfig,
		dhcpLeases,
		selectedLease = null,
		networkObjects
	}: Props = $props();

	let editing = $derived({
		lease: selectedLease
			? dhcpLeases.db.find((lease) => lease.id === Number(selectedLease)) || null
			: null
	});

	let options = $derived({
		hostname: editing.lease ? editing.lease.hostname : '',
		dhcpRange: {
			combobox: {
				value: editing.lease ? editing.lease.dhcpRangeId.toString() : '',
				options: dhcpRanges.map((range) => ({
					value: range.id.toString(),
					label: `${range.startIp} - ${range.endIp} (${range.manualSwitch ? range.manualSwitch.bridge : range.standardSwitch?.name})`
				})),
				open: false
			}
		},
		identifier: {
			combobox: {
				value: selectedLease
					? editing.lease?.macObjectId
						? `mac-${editing.lease.macObjectId}`
						: editing.lease?.duidObjectId
							? `duid-${editing.lease.duidObjectId}`
							: ''
					: '',
				options: [
					...generateMACOptions(networkObjects, true),
					...generateDUIDOptions(networkObjects, true)
				],
				open: false
			}
		},
		ip: {
			combobox: {
				value: selectedLease
					? editing.lease?.ipObjectId
						? `ip-${editing.lease.ipObjectId}`
						: ''
					: '',
				options: [] as { value: string; label: string }[],
				open: false
			}
		},
		comments: selectedLease ? editing.lease?.comments || '' : ''
	});

	let properties = $state(options);

	$effect(() => {
		if (properties.identifier.combobox.value.startsWith('mac')) {
			properties.ip.combobox.options = generateIPOptions(networkObjects, 'ipv4', true);
			properties.ip.combobox.value = selectedLease
				? editing.lease?.ipObjectId
					? `ip-${editing.lease.ipObjectId}`
					: ''
				: '';
		} else if (properties.identifier.combobox.value.startsWith('duid')) {
			properties.ip.combobox.options = generateIPOptions(networkObjects, 'ipv6', true);
			properties.ip.combobox.value = selectedLease
				? editing.lease?.ipObjectId
					? `ip-${editing.lease.ipObjectId}`
					: ''
				: '';
		} else {
			properties.ip.combobox.options = [];
			properties.ip.combobox.value = '';
		}
	});

	function basicTests(): boolean {
		if (!validateDnsmasqHostname(properties.hostname)) {
			toast.error('Invalid hostname', {
				position: 'bottom-center'
			});
			return false;
		}

		if (!properties.dhcpRange.combobox.value) {
			toast.error('Range is required', {
				position: 'bottom-center'
			});
			return false;
		}

		if (!properties.identifier.combobox.value) {
			toast.error('Identifier is required', {
				position: 'bottom-center'
			});
			return false;
		}

		if (!properties.ip.combobox.value) {
			toast.error('IP Address is required', {
				position: 'bottom-center'
			});
			return false;
		}

		let range = dhcpRanges.find((r) => r.id.toString() === properties.dhcpRange.combobox.value);
		let identifier = networkObjects.find(
			(obj) => obj.id.toString() === properties.identifier.combobox.value.split('-')[1]
		);
		let ip = networkObjects.find(
			(obj) => obj.id.toString() === properties.ip.combobox.value.split('-')[1]
		);

		if (!range || !identifier || !ip) {
			toast.error('Invalid selection', {
				position: 'bottom-center'
			});
			return false;
		}

		if (range.type === 'ipv4') {
			if (!properties.identifier.combobox.value.startsWith('mac')) {
				toast.error('Identifier must be a MAC for IPv4 ranges', {
					position: 'bottom-center'
				});

				return false;
			}

			if (ip.type === 'Host') {
				const entries = ip.entries || [];
				const hasIPv4 = entries.length === 1 && entries.every((entry) => isValidIPv4(entry.value));
				if (!hasIPv4) {
					toast.error('IP Address must be an IPv4 address for IPv4 ranges', {
						position: 'bottom-center'
					});
					return false;
				}
			}
		}

		if (range.type === 'ipv6') {
			if (!properties.identifier.combobox.value.startsWith('duid')) {
				toast.error('Identifier must be a DUID for IPv6 ranges', {
					position: 'bottom-center'
				});
				return false;
			}

			if (ip.type === 'Host') {
				const entries = ip.entries || [];
				const hasIPv6 = entries.length === 1 && entries.every((entry) => !isValidIPv4(entry.value));
				if (!hasIPv6) {
					toast.error('IP Address must be an IPv6 address for IPv6 ranges', {
						position: 'bottom-center'
					});
					return false;
				}
			}
		}

		return true;
	}

	async function create() {
		if (basicTests() === false) return;

		const response = await createDHCPLease(
			properties.hostname,
			properties.comments,
			properties.ip.combobox.value ? parseInt(properties.ip.combobox.value.split('-')[1]) : null,
			properties.identifier.combobox.value.startsWith('mac')
				? parseInt(properties.identifier.combobox.value.split('-')[1])
				: null,
			properties.identifier.combobox.value.startsWith('duid')
				? parseInt(properties.identifier.combobox.value.split('-')[1])
				: null,
			parseInt(properties.dhcpRange.combobox.value)
		);

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to create lease', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Lease created', {
			position: 'bottom-center'
		});

		open = false;
		reload = true;
	}

	async function edit() {
		if (basicTests() === false) return;
		const response = await modifyDHCPLease(
			Number(selectedLease),
			properties.hostname,
			properties.comments,
			properties.ip.combobox.value ? parseInt(properties.ip.combobox.value.split('-')[1]) : null,
			properties.identifier.combobox.value.startsWith('mac')
				? parseInt(properties.identifier.combobox.value.split('-')[1])
				: null,
			properties.identifier.combobox.value.startsWith('duid')
				? parseInt(properties.identifier.combobox.value.split('-')[1])
				: null,
			parseInt(properties.dhcpRange.combobox.value)
		);

		reload = true;
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to edit lease', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Lease edited', {
			position: 'bottom-center'
		});

		open = false;
		reload = true;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center">
						<span class="icon-[memory--range] mr-2 h-6 w-6"></span>

						<span>{selectedLease ? 'Edit' : 'Create'} DHCP Lease</span>
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

		<div class="flex flex-row gap-2">
			<CustomValueInput
				label="Hostname"
				bind:value={properties.hostname}
				placeholder="postgres"
				classes="flex-1 min-w-0 space-y-1.5"
			/>

			<CustomComboBox
				bind:open={properties.dhcpRange.combobox.open}
				label="Range"
				bind:value={properties.dhcpRange.combobox.value}
				data={properties.dhcpRange.combobox.options}
				classes="flex-1 min-w-0 max-w-[360px] space-y-1"
				placeholder="Select Range"
				triggerWidth="w-full"
				width="w-full"
			/>
		</div>

		<div class="flex min-w-0 flex-row gap-2">
			<CustomComboBox
				bind:open={properties.identifier.combobox.open}
				label="Identifier"
				bind:value={properties.identifier.combobox.value}
				data={properties.identifier.combobox.options}
				classes="basis-0 flex-1 min-w-0 space-y-1"
				placeholder="Select Identifier"
				triggerWidth="w-full"
				width="w-full"
			/>

			<CustomComboBox
				bind:open={properties.ip.combobox.open}
				label="IP Address"
				bind:value={properties.ip.combobox.value}
				data={properties.ip.combobox.options}
				classes="basis-0 flex-1 min-w-0 space-y-1"
				placeholder="Select IP Address"
				triggerWidth="w-full"
				width="w-full"
			/>
		</div>

		<CustomValueInput
			label="Comments"
			bind:value={properties.comments}
			placeholder="Optional comments"
			classes="w-full min-w-0 space-y-1.5"
			type="textarea"
			textAreaClasses="min-h-18"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if selectedLease}
					<Button onclick={edit} type="submit" size="sm">Edit</Button>
				{:else}
					<Button onclick={create} type="submit" size="sm">Create</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
