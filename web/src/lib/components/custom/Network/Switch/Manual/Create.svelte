<script lang="ts">
	import { createManualSwitch } from '$lib/api/network/switch';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { handleAPIError } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		bridges: string[];
		reload: boolean;
	}

	let { open = $bindable(), bridges, reload = $bindable() }: Props = $props();

	// svelte-ignore state_referenced_locally
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
	<Dialog.Content
		showCloseButton={true}
		showResetButton={true}
		onReset={() => (properties = options)}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[streamline-sharp--router-wifi-network-solid]"
					size="h-6 w-6"
					gap="gap-2"
					title="Create Manual Switch"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex flex-col gap-4">
			<CustomValueInput
				label="Name"
				placeholder="WAN"
				bind:value={properties.name}
				classes="space-y-1"
				type="text"
			/>

			<ComboBox
				bind:open={properties.bridge.open}
				label="Bridge"
				bind:value={properties.bridge.selected}
				data={properties.bridge.options}
				classes="space-y-1"
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
