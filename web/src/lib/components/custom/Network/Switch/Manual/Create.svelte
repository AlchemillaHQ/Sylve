<script lang="ts">
	import { createManualSwitch } from '$lib/api/network/switch';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { handleAPIError } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		bridges: string[];
		reload: boolean;
	}

	let { open = $bindable(), bridges, reload = $bindable() }: Props = $props();

	let options = {
		name: '',
		bridge: {
			open: false,
			options: generateComboboxOptions(bridges),
			selected: ''
		}
	};

	let properties = $state(options);

	async function create() {
		if (!/^[a-zA-Z0-9]+$/.test(properties.name)) {
			toast.error('Invalid name', {
				position: 'bottom-center'
			});
			return;
		}

		const response = await createManualSwitch(properties.name, properties.bridge.selected);
		reload = true;
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to create manual switch', {
				position: 'bottom-center'
			});
		} else {
			toast.success('Manual switch created', {
				position: 'bottom-center'
			});

			open = false;
			properties = options;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<div class="flex items-center justify-between">
			<Dialog.Header>
				<Dialog.Title>
					<div class="flex items-center">
						<span class="icon-[streamline-sharp--router-wifi-network-solid] mr-2 h-6 w-6"></span>

						<span class="text-lg font-semibold">Create Manual Switch</span>
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

		<div class="flex flex-col gap-4">
			<CustomValueInput
				label={'Name'}
				placeholder="WAN"
				bind:value={properties.name}
				classes="flex-1 space-y-1.5"
				type="text"
			/>

			<ComboBox
				bind:open={properties.bridge.open}
				label={'Bridge'}
				bind:value={properties.bridge.selected}
				data={properties.bridge.options}
				classes="flex-1 space-y-1"
				placeholder="Select bridge"
				width="w-3/4"
			></ComboBox>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={create} type="submit" size="sm">Create</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
