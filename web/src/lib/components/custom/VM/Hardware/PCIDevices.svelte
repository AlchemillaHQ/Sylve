<script lang="ts">
	import { modifyPPT } from '$lib/api/vm/hardware';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';

	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		vm: VM | null;
		pciDevices: PCIDevice[];
		pptDevices: PPTDevice[];
	}

	let { open = $bindable(), vm, pciDevices, pptDevices }: Props = $props();
	let pciOptions = $derived.by(() => {
		let options = [];

		for (const pptDevice of pptDevices) {
			const device = pptDevice.deviceID;
			if (device) {
				const split = device.split('/');
				const bus = Number(split[0]);
				const deviceC = Number(split[1]);
				const functionC = Number(split[2]);
				for (const pciDevice of pciDevices) {
					if (
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

	// svelte-ignore state_referenced_locally
	let options = {
		combobox: {
			open: false,
			value: vm?.pciDevices?.map((device) => device.toString()) || [],
			options: pciOptions
		}
	};

	let properties = $state(options);

	async function modify() {
		if (vm) {
			const response = await modifyPPT(
				vm.rid,
				properties.combobox.value.map((id) => Number(id)) || []
			);

			if (response.error) {
				handleAPIError(response);
				toast.error('Failed to modify PCI devices', {
					position: 'bottom-center'
				});
			} else {
				toast.success('PCI devices modified', {
					position: 'bottom-center'
				});
				open = false;
			}
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-full min-w-0 p-5">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--video-input-hdmi] h-5 w-5"></span>
					<span>PCI Devices</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
						onclick={() => {
							properties = options;
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							properties = options;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="min-w-0 overflow-x-auto">
			<CustomComboBox
				bind:open={properties.combobox.open}
				bind:value={properties.combobox.value}
				data={properties.combobox.options}
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
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
