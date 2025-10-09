<script lang="ts">
	import { updateDHCPConfig } from '$lib/api/network/dhcp';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBoxBindable from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { DHCPConfig } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError } from '$lib/utils/http';
	import { generateComboboxOptions, generateSwitchOptions } from '$lib/utils/input';
	import { isValidDHCPDomain, isValidIPv4, isValidIPv6 } from '$lib/utils/string';
	import Icon from '@iconify/svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		reload: boolean;
		networkInterfaces: Iface[];
		networkSwitches: SwitchList;
		dhcpConfig: DHCPConfig;
	}

	let {
		open = $bindable(),
		reload = $bindable(),
		networkInterfaces,
		networkSwitches,
		dhcpConfig
	}: Props = $props();

	let options = {
		expandHosts: dhcpConfig.expandHosts,
		domain: dhcpConfig.domain,
		dnsServers: {
			combobox: {
				open: false,
				values: dhcpConfig.dnsServers,
				options: generateComboboxOptions(dhcpConfig.dnsServers)
			}
		},
		switches: {
			combobox: {
				open: false,
				values: [
					...dhcpConfig.manualSwitches.map((s) => `${s.id}-man-${s.name}`),
					...dhcpConfig.standardSwitches.map((s) => `${s.id}-stan-${s.name}`)
				],
				options: generateSwitchOptions(networkSwitches)
			}
		}
	};

	let properties = $state(options);

	async function saveConfig() {
		let error = '';

		if (!isValidDHCPDomain(properties.domain)) {
			error = 'Invalid domain';
		}

		for (const dns of properties.dnsServers.combobox.values) {
			if (!isValidIPv4(dns) && !isValidIPv6(dns)) {
				error = 'Invalid DNS server';
				break;
			}
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		let switchIds = {
			manual: [] as number[],
			standard: [] as number[]
		};

		for (const sw of properties.switches.combobox.values) {
			const [id, type] = sw.split('-');
			if (type === 'man') {
				switchIds.manual.push(parseInt(id));
			} else if (type === 'stan') {
				switchIds.standard.push(parseInt(id));
			}
		}

		const res = await updateDHCPConfig(
			switchIds.standard,
			switchIds.manual,
			properties.dnsServers.combobox.values,
			properties.domain,
			properties.expandHosts
		);

		reload = true;

		if (res.error) {
			handleAPIError(res);
			toast.error('Error updating DHCP configuration', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Updated DHCP configuration', {
			position: 'bottom-center'
		});

		open = false;
		return;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center">
						<Icon icon="mdi:dns" class="mr-2 h-6 w-6" />
						<span class="text-lg font-semibold">Update DHCP Configuration</span>
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

		<div class="flex flex-col gap-4">
			<CustomValueInput
				label={'Domain'}
				placeholder="lan"
				bind:value={properties.domain}
				classes="flex-1 space-y-1.5"
				type="text"
			/>

			<ComboBoxBindable
				bind:open={properties.dnsServers.combobox.open}
				label={'DNS Servers'}
				bind:value={properties.dnsServers.combobox.values}
				data={properties.dnsServers.combobox.options}
				classes="flex-1 space-y-1"
				placeholder="Select DNS servers"
				width="w-full"
				multiple={true}
			></ComboBoxBindable>
		</div>

		<ComboBox
			bind:open={properties.switches.combobox.open}
			label={'Switches'}
			bind:value={properties.switches.combobox.values}
			data={properties.switches.combobox.options}
			classes="flex-1 space-y-1"
			placeholder="Select switches"
			width="w-full"
			multiple={true}
		></ComboBox>

		<CustomCheckbox
			label="Expand Hosts"
			bind:checked={properties.expandHosts}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={saveConfig} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
