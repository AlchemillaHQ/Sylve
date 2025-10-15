<script lang="ts">
	import { createDHCPRange, updateDHCPRange } from '$lib/api/network/dhcp';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { DHCPConfig, DHCPRange } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import { handleAPIError } from '$lib/utils/http';
	import { generateSwitchOptions } from '$lib/utils/input';
	import {
		dnsmasqToSeconds,
		ipMaskToCIDR,
		isValidDHCPRange,
		isValidIPv4,
		secondsToDnsmasq
	} from '$lib/utils/string';
	import Icon from '@iconify/svelte';
	import { toast } from 'svelte-sonner';
	import { isValidIPv4Range, isValidIPv6Range } from '$lib/utils/inet';

	interface Props {
		open: boolean;
		reload: boolean;
		networkInterfaces: Iface[];
		networkSwitches: SwitchList;
		dhcpConfig: DHCPConfig;
		dhcpRanges: DHCPRange[];
		selectedRange: DHCPRange | null;
	}

	let {
		open = $bindable(),
		reload = $bindable(),
		networkInterfaces,
		networkSwitches,
		dhcpRanges,
		dhcpConfig,
		selectedRange = null as DHCPRange | null
	}: Props = $props();

	let configuredSwitches = $derived.by(() => {
		const sws: string[] = [];
		if (dhcpConfig) {
			if (dhcpConfig.standardSwitches) {
				sws.push(...dhcpConfig.standardSwitches.map((sw) => `${sw.id}-stan-${sw.name}`));
			}
			if (dhcpConfig.manualSwitches) {
				sws.push(...dhcpConfig.manualSwitches.map((sw) => `${sw.id}-man-${sw.name}`));
			}
		}

		return sws;
	});

	let currentSwId = $derived.by(() => {
		if (selectedRange) {
			if (selectedRange.standardSwitch) {
				const sw = networkSwitches.standard?.find((s) => s.id === selectedRange.standardSwitch?.id);
				if (sw) {
					return `${sw.id}-stan-${sw.name}`;
				}
			} else if (selectedRange.manualSwitch) {
				const sw = networkSwitches.manual?.find((s) => s.id === selectedRange.manualSwitch?.id);
				if (sw) {
					return `${sw.id}-man-${sw.name}`;
				}
			}
		}

		return '';
	});

	let ipType = $derived.by(() => {
		if (selectedRange) {
			if (isValidIPv4(selectedRange.startIp)) {
				return 'ipv4' as 'ipv4' | 'ipv6';
			} else {
				return 'ipv6' as 'ipv4' | 'ipv6';
			}
		}

		return 'ipv4' as 'ipv4' | 'ipv6';
	});

	let usedSwitches = $derived.by(() => {
		if (!dhcpRanges || dhcpRanges.length === 0) {
			return [] as string[];
		}

		const used: string[] = [];
		for (const range of dhcpRanges) {
			let swVal = '';
			if (range.standardSwitch) {
				const sw = networkSwitches.standard?.find((s) => s.id === range.standardSwitch?.id);
				if (sw) swVal = `${sw.id}-stan-${sw.name}`;
			} else if (range.manualSwitch) {
				const sw = networkSwitches.manual?.find((s) => s.id === range.manualSwitch?.id);
				if (sw) swVal = `${sw.id}-man-${sw.name}`;
			}
			if (swVal) {
				// store with ip version to allow same switch for the other type
				used.push(`${swVal}|${range.type}` as const);
			}
		}

		// While editing, allow re-selecting the currently attached switch for THIS range's type
		if (selectedRange) {
			const curType = isValidIPv4(selectedRange.startIp) ? 'ipv4' : 'ipv6';
			const curKeyStan = `${selectedRange.standardSwitch?.id}-stan-${selectedRange.standardSwitch?.name}|${curType}`;
			const curKeyMan = `${selectedRange.manualSwitch?.id}-man-${selectedRange.manualSwitch?.name}|${curType}`;

			const iStan = used.indexOf(curKeyStan);
			if (iStan > -1) used.splice(iStan, 1);

			const iMan = used.indexOf(curKeyMan);
			if (iMan > -1) used.splice(iMan, 1);
		}

		return used;
	});

	let options = $derived({
		ipType: {
			combobox: {
				value: ipType,
				options: [
					{ label: 'IPv4', value: 'ipv4' },
					{ label: 'IPv6', value: 'ipv6' }
				],
				open: false
			}
		},
		startIp: selectedRange ? selectedRange.startIp : '',
		endIp: selectedRange ? selectedRange.endIp : '',
		switchId: {
			combobox: {
				open: false,
				value: selectedRange == null ? '' : currentSwId,
				options: generateSwitchOptions(networkSwitches).filter((opt) => {
					const key = `${opt.value}|${ipType}`; // <- use current ipType
					return !usedSwitches.includes(key) || opt.value === currentSwId;
				})
			}
		},
		expiry: selectedRange ? secondsToDnsmasq(selectedRange.expiry, true) : '12h',
		raOnly: selectedRange ? selectedRange.raOnly : false,
		slaac: selectedRange ? selectedRange.slaac : false
	});

	let properties = $state(options);

	$effect(() => {
		if (properties.ipType.combobox.value === 'ipv4') {
			properties.raOnly = false;
			properties.slaac = false;

			properties.switchId.combobox.options = generateSwitchOptions(networkSwitches).filter(
				(opt) => {
					const key = `${opt.value}|ipv4`;
					return !usedSwitches.includes(key) || opt.value === currentSwId;
				}
			);
		}

		if (properties.ipType.combobox.value === 'ipv6') {
			properties.switchId.combobox.options = generateSwitchOptions(networkSwitches).filter(
				(opt) => {
					const key = `${opt.value}|ipv6`;
					return !usedSwitches.includes(key) || opt.value === currentSwId;
				}
			);
		}
	});

	$effect(() => {
		if (open && properties.expiry !== '') {
			try {
				dnsmasqToSeconds(properties.expiry);
			} catch (e) {
				properties.expiry = '12h';
				toast.error('Invalid expiry format, resetting to 12h', {
					position: 'bottom-center'
				});
			}
		}
	});

	async function create() {
		let ipVersion = properties.ipType.combobox.value;

		if (!ipVersion) {
			toast.error('IP Type is required', {
				position: 'bottom-center'
			});
			return;
		}

		if (!properties.switchId.combobox.value) {
			toast.error('No switch selected', {
				position: 'bottom-center'
			});
			return;
		}

		if (ipVersion === 'ipv4') {
			const iface = networkInterfaces.find(
				(iface) =>
					iface.description === properties.switchId.combobox.value.split('-')[2] ||
					iface.name === properties.switchId.combobox.value.split('-')[2]
			);

			if (!iface) {
				toast.error('Failed to find interface for selected switch', {
					position: 'bottom-center'
				});
				return;
			} else {
				if (iface.ipv4 && iface.ipv4.length > 0) {
					let one = false;

					for (const ipv4 of iface.ipv4) {
						if (isValidIPv4Range(properties.startIp, properties.endIp, ipv4.ip, ipv4.netmask)) {
							one = true;
							break;
						}
					}

					if (!one) {
						toast.error('IP Range not in switch subnet', {
							position: 'bottom-center'
						});
						return;
					}
				} else {
					toast.error('Selected interface has no IPv4 address', {
						position: 'bottom-center'
					});
					return;
				}
			}
		} else {
			const iface = networkInterfaces.find(
				(iface) =>
					iface.description === properties.switchId.combobox.value.split('-')[2] ||
					iface.name === properties.switchId.combobox.value.split('-')[2]
			);

			if (!iface) {
				toast.error('Failed to find interface for selected switch', {
					position: 'bottom-center'
				});
				return;
			} else {
				if (iface.ipv6 && iface.ipv6.length > 0) {
					const usable = iface.ipv6.filter((ip) => !ip.ip.startsWith('fe80'));
					if (usable.length === 0) {
						toast.error('Selected interface has no usable IPv6 address', {
							position: 'bottom-center'
						});
						return;
					} else {
						for (const ipv6 of usable) {
							if (properties.startIp !== '' || properties.endIp !== '') {
								if (
									!isValidIPv6Range(
										properties.startIp,
										properties.endIp,
										ipv6.ip,
										ipv6.prefixLength
									)
								) {
									toast.error(`IP Range not in switch subnet (${ipv6.ip}/${ipv6.prefixLength})`, {
										position: 'bottom-center'
									});
									return;
								}
							}
						}
					}
				}
			}
		}

		if (!properties.expiry || properties.expiry.trim() === '') {
			toast.error('Expiry is required', {
				position: 'bottom-center'
			});
			return;
		}

		let standardSwId = undefined as number | undefined;
		let manualSwId = undefined as number | undefined;

		if (properties.switchId.combobox.value) {
			const parts = properties.switchId.combobox.value.split('-');
			if (parts[1] === 'stan') {
				standardSwId = parseInt(parts[0]);
			} else if (parts[1] === 'man') {
				manualSwId = parseInt(parts[0]);
			}
		}

		const res = await createDHCPRange(
			ipVersion,
			properties.startIp,
			properties.endIp,
			dnsmasqToSeconds(properties.expiry),
			properties.raOnly,
			properties.slaac,
			standardSwId,
			manualSwId
		);

		if (res.error) {
			handleAPIError(res);
			toast.error('Error creating DHCP range', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Created DHCP range', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}

	async function save() {
		if (!selectedRange) {
			toast.error('No DHCP range selected', {
				position: 'bottom-center'
			});
			return;
		}

		if (properties.ipType.combobox.value === 'ipv4') {
			const iface = networkInterfaces.find(
				(iface) =>
					iface.description === properties.switchId.combobox.value.split('-')[2] ||
					iface.name === properties.switchId.combobox.value.split('-')[2]
			);

			if (!iface) {
				toast.error('Failed to find interface for selected switch', {
					position: 'bottom-center'
				});
				return;
			} else {
				if (iface.ipv4 && iface.ipv4.length > 0) {
					let one = false;

					for (const ipv4 of iface.ipv4) {
						if (isValidIPv4Range(properties.startIp, properties.endIp, ipv4.ip, ipv4.netmask)) {
							one = true;
							break;
						}
					}

					if (!one) {
						toast.error('IP Range not in switch subnet', {
							position: 'bottom-center'
						});
						return;
					}
				} else {
					toast.error('Selected interface has no IPv4 address', {
						position: 'bottom-center'
					});
					return;
				}
			}
		} else {
			const iface = networkInterfaces.find(
				(iface) =>
					iface.description === properties.switchId.combobox.value.split('-')[2] ||
					iface.name === properties.switchId.combobox.value.split('-')[2]
			);

			if (!iface) {
				toast.error('Failed to find interface for selected switch', {
					position: 'bottom-center'
				});
				return;
			} else {
				if (iface.ipv6 && iface.ipv6.length > 0) {
					const usable = iface.ipv6.filter((ip) => !ip.ip.startsWith('fe80'));
					if (usable.length === 0) {
						toast.error('Selected interface has no usable IPv6 address', {
							position: 'bottom-center'
						});
						return;
					} else {
						for (const ipv6 of usable) {
							if (properties.startIp !== '' || properties.endIp !== '') {
								if (
									!isValidIPv6Range(
										properties.startIp,
										properties.endIp,
										ipv6.ip,
										ipv6.prefixLength
									)
								) {
									toast.error(`IP Range not in switch subnet (${ipv6.ip}/${ipv6.prefixLength})`, {
										position: 'bottom-center'
									});
									return;
								}
							}
						}
					}
				}
			}
		}

		if (!properties.switchId.combobox.value) {
			toast.error('No switch selected', {
				position: 'bottom-center'
			});
			return;
		}

		if (!properties.expiry || properties.expiry.trim() === '') {
			toast.error('Expiry is required', {
				position: 'bottom-center'
			});
			return;
		}

		let standardSwId = undefined as number | undefined;
		let manualSwId = undefined as number | undefined;

		if (properties.switchId.combobox.value) {
			const parts = properties.switchId.combobox.value.split('-');
			if (parts[1] === 'stan') {
				standardSwId = parseInt(parts[0]);
			} else if (parts[1] === 'man') {
				manualSwId = parseInt(parts[0]);
			}
		}

		const res = await updateDHCPRange(
			properties.ipType.combobox.value,
			selectedRange!.id,
			properties.startIp,
			properties.endIp,
			dnsmasqToSeconds(properties.expiry),
			properties.raOnly,
			properties.slaac,
			standardSwId,
			manualSwId
		);

		if (res.error) {
			handleAPIError(res);
			toast.error('Error updating DHCP range', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Updated DHCP range', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center">
						<Icon icon="memory:range" class="mr-2 h-6 w-6" />
						<span>{selectedRange ? 'Edit' : 'Create'} DHCP Range</span>
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
					<Icon icon="radix-icons:reset" class="pointer-events-none h-4 w-4" />
					<span class="sr-only">{'Reset'}</span>
				</Button>
				<Button size="sm" variant="link" class="h-4" title={'Close'} onclick={() => (open = false)}>
					<Icon icon="material-symbols:close-rounded" class="pointer-events-none h-4 w-4" />
					<span class="sr-only">{'Close'}</span>
				</Button>
			</div>
		</div>

		{#if !selectedRange}
			<CustomComboBox
				bind:open={properties.ipType.combobox.open}
				label="IP Type"
				bind:value={properties.ipType.combobox.value}
				data={properties.ipType.combobox.options}
				classes="flex-1 space-y-1"
				placeholder="Select IP Type"
				triggerWidth="w-full"
				width="w-full"
			></CustomComboBox>
		{/if}

		<div class="flex flex-row gap-2">
			<CustomValueInput
				label="Start IP"
				bind:value={properties.startIp}
				placeholder={properties.ipType.combobox.value === 'ipv4'
					? '192.168.1.50'
					: 'fd00:cafe:babe::50'}
				classes="flex-1 space-y-1.5"
			/>

			<CustomValueInput
				label="End IP"
				bind:value={properties.endIp}
				placeholder={properties.ipType.combobox.value === 'ipv4'
					? '192.168.1.150'
					: 'fd00:cafe:babe::150'}
				classes="flex-1 space-y-1.5"
			/>
		</div>

		<div class="flex flex-row gap-2">
			<CustomComboBox
				bind:open={properties.switchId.combobox.open}
				label="Switch"
				bind:value={properties.switchId.combobox.value}
				data={properties.switchId.combobox.options}
				classes="flex-1 space-y-1"
				placeholder="Select Switch"
				triggerWidth="w-full"
				width="w-full lg:w-[75%]"
			></CustomComboBox>

			<CustomValueInput
				label="Expiry"
				bind:value={properties.expiry}
				placeholder="Expiry"
				classes="flex-1 space-y-1.5"
			/>
		</div>

		{#if properties.ipType.combobox.value === 'ipv6'}
			<div class="mt-2 flex flex-row gap-2">
				<CustomCheckbox
					label="RA Only"
					bind:checked={properties.raOnly}
					classes="flex items-center gap-2"
				></CustomCheckbox>

				<CustomCheckbox
					label="SLAAC"
					bind:checked={properties.slaac}
					classes="flex items-center gap-2"
				></CustomCheckbox>
			</div>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if !selectedRange}
					<Button onclick={create} type="submit" size="sm">Create</Button>
				{:else}
					<Button onclick={save} type="submit" size="sm">Save</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
