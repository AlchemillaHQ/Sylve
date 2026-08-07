<script lang="ts">
	import { updateDHCPConfig } from '$lib/api/network/dhcp';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBoxBindable from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { DHCPConfig } from '$lib/types/network/dhcp';
	import type { SwitchList } from '$lib/types/network/switch';
	import { handleAPIError } from '$lib/utils/http';
	import { generateComboboxOptions, generateSwitchOptions } from '$lib/utils/input';
	import { isValidDHCPDomain, isValidIPv4, isValidIPv6 } from '$lib/utils/string';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		reload: boolean;
		networkSwitches: SwitchList;
		dhcpConfig: DHCPConfig;
	}

	let { open = $bindable(), reload = $bindable(), networkSwitches, dhcpConfig }: Props = $props();

	function createProperties() {
		const dnsServers = [...dhcpConfig.dnsServers];
		return {
			expandHosts: dhcpConfig.expandHosts,
			domain: dhcpConfig.domain,
			dnsServers: {
				combobox: {
					open: false,
					values: dnsServers,
					options: generateComboboxOptions(dnsServers)
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
	}

	let properties = $state(createProperties());
	let saving = $state(false);

	function resetForm() {
		if (!saving) properties = createProperties();
	}

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) resetForm();
		}
	);

	async function saveConfig() {
		if (saving) return;

		let error = '';
		const domain = properties.domain.trim().toLowerCase();
		const dnsServers = [...new Set(properties.dnsServers.combobox.values.map((dns) => dns.trim()))];

		if (!isValidDHCPDomain(domain)) {
			error = 'Invalid domain';
		}

		if (dnsServers.length > 16) {
			error = 'A maximum of 16 DNS servers is allowed';
		}

		for (const dns of dnsServers) {
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

		const switchIds = {
			manual: new Set<number>(),
			standard: new Set<number>()
		};

		for (const sw of properties.switches.combobox.values) {
			const [id, type] = sw.split('-');
			const parsedID = Number.parseInt(id, 10);
			if (!Number.isInteger(parsedID) || parsedID <= 0) {
				error = 'Invalid switch selection';
				break;
			}
			if (type === 'man') {
				switchIds.manual.add(parsedID);
			} else if (type === 'stan') {
				switchIds.standard.add(parsedID);
			} else {
				error = 'Invalid switch selection';
				break;
			}
		}

		if (switchIds.manual.size + switchIds.standard.size > 256) {
			error = 'A maximum of 256 switches is allowed';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const res = await updateDHCPConfig(
				[...switchIds.standard],
				[...switchIds.manual],
				dnsServers,
				domain,
				properties.expandHosts
			);

			if (res.status !== 'success') {
				handleAPIError(res);
				toast.error('Error updating DHCP configuration', {
					position: 'bottom-center'
				});
				return;
			}

			reload = true;
			toast.success('Updated DHCP configuration', {
				position: 'bottom-center'
			});
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={resetForm}
		onClose={() => {
			if (!saving) open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--dns]"
					size="h-6 w-6"
					gap="gap-2"
					title="Update DHCP Configuration"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<fieldset disabled={saving} class="contents">
			<div class="flex flex-col gap-4">
				<CustomValueInput
					label="Domain"
					placeholder="lan"
					bind:value={properties.domain}
					classes="flex-1 space-y-1.5"
					type="text"
				/>

				<ComboBoxBindable
					bind:open={properties.dnsServers.combobox.open}
					label="DNS Servers"
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
				label="Switches"
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
					<Button onclick={saveConfig} type="submit" size="sm" disabled={saving}>
						{#if saving}
							<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
							<span>Saving...</span>
						{:else}
							Save
						{/if}
					</Button>
				</div>
			</Dialog.Footer>
		</fieldset>
	</Dialog.Content>
</Dialog.Root>
