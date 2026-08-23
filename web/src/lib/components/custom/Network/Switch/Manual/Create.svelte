<script lang="ts">
	import { createManualSwitch } from '$lib/api/network/switch';
	import Button from '$lib/components/ui/button/button.svelte';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { isValidSwitchName } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		bridges: string[];
		reload: boolean;
	}

	let { open = $bindable(), bridges, reload = $bindable() }: Props = $props();

	let properties = $state({
		name: '',
		bridge: {
			open: false,
			selected: ''
		}
	});
	let bridgeOptions = $derived(generateComboboxOptions(bridges));
	let saving = $state(false);

	function reset() {
		properties.name = '';
		properties.bridge.open = false;
		properties.bridge.selected = '';
	}

	async function create() {
		if (saving) return;

		const name = properties.name.trim();
		const bridge = properties.bridge.selected.trim();
		if (!name || name.length > 128 || !isValidSwitchName(name)) {
			toast.error('Invalid name', {
				position: 'bottom-center'
			});
			return;
		}
		if (!bridge) {
			toast.error('Select a bridge', {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await createManualSwitch(name, bridge);
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to create manual switch', {
					position: 'bottom-center'
				});
				return;
			}

			toast.success('Manual switch created', {
				position: 'bottom-center'
			});
			reload = true;
			reset();
			open = false;
		} catch (error) {
			console.error('Failed to create manual switch', error);
			toast.error('Failed to create manual switch', {
				position: 'bottom-center'
			});
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={reset}
		onClose={() => {
			if (!saving) open = false;
		}}
		aria-busy={saving}
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
				data={bridgeOptions}
				classes="space-y-1"
				placeholder="Select bridge"
				width="w-3/4"
			></ComboBox>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={create} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Creating...
					{:else}
						Create
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
