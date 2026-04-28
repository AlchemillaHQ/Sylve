<script lang="ts">
	import { updateSambaConfig } from '$lib/api/samba/config';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Iface } from '$lib/types/network/iface';
	import type { SambaConfig } from '$lib/types/samba/config';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		reload: boolean;
		sambaConfig: SambaConfig;
		networkInterfaces: Iface[];
	}

	let {
		open = $bindable(),
		reload = $bindable(),
		sambaConfig,
		networkInterfaces
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	let options = {
		unixCharset: sambaConfig.unixCharset,
		workgroup: sambaConfig.workgroup,
		serverString: sambaConfig.serverString,
		interfaces: {
			combobox: {
				open: false,
				values: sambaConfig.interfaces
					? sambaConfig.interfaces
							.split(',')
							.map((s) => s.trim())
							.filter(Boolean)
					: []
			}
		},
		bindInterfacesOnly: sambaConfig.bindInterfacesOnly,
		appleExtensions: sambaConfig.appleExtensions
	};

	let properties = $state(options);

	async function saveConfig() {
		const response = await updateSambaConfig({
			unixCharset: properties.unixCharset,
			workgroup: properties.workgroup,
			serverString: properties.serverString,
			interfaces: properties.interfaces.combobox.values.join(','),
			bindInterfacesOnly: properties.bindInterfacesOnly,
			appleExtensions: properties.appleExtensions
		});

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to update Samba configuration', { position: 'bottom-center' });
			return;
		}

		toast.success('Samba configuration updated', { position: 'bottom-center' });
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
					title="Update Samba Configuration"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid grid-cols-2 gap-4">
			<CustomValueInput
				label="Unix Charset"
				placeholder="UTF-8"
				bind:value={properties.unixCharset}
				classes="space-y-1.5"
				type="text"
			/>

			<CustomValueInput
				label="Workgroup"
				placeholder="WORKGROUP"
				bind:value={properties.workgroup}
				classes="space-y-1.5"
				type="text"
			/>

			<CustomValueInput
				label="Server String"
				placeholder="Sylve SMB Server"
				bind:value={properties.serverString}
				classes="space-y-1.5"
				type="text"
			/>

			<ComboBox
				bind:open={properties.interfaces.combobox.open}
				label="Interfaces"
				bind:value={properties.interfaces.combobox.values}
				data={networkInterfaces.map((iface) => ({
					label: iface.description !== '' ? iface.description : iface.name,
					value: iface.name
				}))}
				classes="space-y-1.5"
				placeholder="Select interfaces"
				width="w-full"
				multiple={true}
			/>
		</div>

		<div class="flex items-center gap-6">
			<CustomCheckbox
				label="Bind Interfaces Only"
				bind:checked={properties.bindInterfacesOnly}
				classes="flex items-center gap-2"
			/>

			<CustomCheckbox
				label="Apple Extensions"
				bind:checked={properties.appleExtensions}
				classes="flex items-center gap-2"
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={saveConfig} type="submit" size="sm">Save</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
