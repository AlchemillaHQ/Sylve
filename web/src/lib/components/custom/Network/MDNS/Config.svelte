<script lang="ts">
	import { setMdnsSettings } from '$lib/api/network/mdns';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Iface } from '$lib/types/network/iface';
	import type { MdnsSettings } from '$lib/types/network/mdns';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		reload: boolean;
		mdnsSettings: MdnsSettings;
		networkInterfaces: Iface[];
	}

	let {
		open = $bindable(),
		reload = $bindable(),
		mdnsSettings,
		networkInterfaces
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	let options = {
		interfaces: {
			combobox: {
				open: false,
				values: mdnsSettings.interfaces
					? mdnsSettings.interfaces
							.split(',')
							.map((s) => s.trim())
							.filter(Boolean)
					: []
			}
		},
		hostname: mdnsSettings.hostname
	};

	let properties = $state(options);

	async function saveConfig() {
		const response = await setMdnsSettings({
			interfaces: properties.interfaces.combobox.values.join(','),
			hostname: properties.hostname
		});

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to update mDNS settings', { position: 'bottom-center' });
			return;
		}

		toast.success('mDNS settings updated', { position: 'bottom-center' });
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		showCloseButton={true}
		showResetButton={true}
		onReset={() => (properties = options)}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--server-network]"
					size="h-6 w-6"
					gap="gap-2"
					title="Update mDNS Settings"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex flex-col gap-4">
			<ComboBox
				bind:open={properties.interfaces.combobox.open}
				label="Interfaces"
				bind:value={properties.interfaces.combobox.values}
				data={networkInterfaces.map((iface) => ({
					label: iface.description !== '' ? iface.description : iface.name,
					value: iface.name
				}))}
				classes="space-y-1.5"
				placeholder="All interfaces"
				width="w-full"
				multiple={true}
			/>

			<CustomValueInput
				label="Hostname"
				placeholder="myserver"
				bind:value={properties.hostname}
				classes="space-y-1.5"
				type="text"
				hint="Leave empty to use the system hostname"
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={saveConfig} type="submit" size="sm">Save</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
