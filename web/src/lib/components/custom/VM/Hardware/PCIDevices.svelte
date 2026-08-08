<script lang="ts">
	import { modifyPPT } from '$lib/api/vm/hardware';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';

	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		vm: VM | null;
		pciDevices: PCIDevice[];
		pptDevices: PPTDevice[];
		reload: boolean;
	}

	let {
		open = $bindable(),
		vm,
		pciDevices,
		pptDevices,
		reload = $bindable(false)
	}: Props = $props();
	let pciOptions = $derived.by(() => {
		let options = [];

		for (const pptDevice of pptDevices) {
			if (pptDevice.domain !== 0) continue;
			const device = pptDevice.deviceID;
			if (device) {
				const split = device.split('/');
				const bus = Number(split[0]);
				const deviceC = Number(split[1]);
				const functionC = Number(split[2]);
				for (const pciDevice of pciDevices) {
					if (
						pciDevice.name.startsWith('ppt') &&
						pciDevice.domain === pptDevice.domain &&
						pciDevice.bus === bus &&
						pciDevice.device === deviceC &&
						pciDevice.function === functionC
					) {
						let label = `${pciDevice.names.vendor} ${pciDevice.names.device}`;
						if (label.length > 32) {
							label = `${label.slice(0, 16)}...${label.slice(-16)}`;
						}

						label = `(${pciDevice.bus}/${pciDevice.device}/${pciDevice.function}) ${label}`;

						options.push({
							label: label,
							value: pptDevice.id.toString()
						});
					}
				}
			}
		}

		return options;
	});

	function initialProperties() {
		return {
			combobox: {
				open: false,
				value: vm?.pciDevices?.map((device) => device.toString()) || []
			}
		};
	}

	let properties = $state(initialProperties());
	let saving = $state(false);

	async function modify() {
		if (vm && !saving) {
			saving = true;
			try {
				const response = await modifyPPT(
					vm.rid,
					properties.combobox.value.map((id) => Number(id)) || []
				);

				if (response.status !== 'success') {
					handleAPIError(response);
					toast.error('Failed to modify PCI devices', {
						position: 'bottom-center'
					});
					return;
				}

				reload = true;
				toast.success('PCI devices modified', {
					position: 'bottom-center'
				});
				open = false;
			} finally {
				saving = false;
			}
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-full min-w-0 p-5"
		showResetButton={true}
		onReset={() => {
			properties = initialProperties();
		}}
		onClose={() => {
			properties = initialProperties();
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--video-input-hdmi]"
					size="h-5 w-5"
					gap="gap-2"
					title="PCI Devices"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="min-w-0 overflow-x-auto">
			<CustomComboBox
				bind:open={properties.combobox.open}
				bind:value={properties.combobox.value}
				data={pciOptions}
				onValueChange={(value) => {
					properties.combobox.value = value as string[];
				}}
				placeholder="Select PCI Devices"
				disabled={false}
				disallowEmpty={false}
				multiple={true}
				width="w-full"
				commandClasses="max-w-full break-words"
			/>
		</div>
		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Saving...
					{:else}
						Save
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
