<script lang="ts">
	import { modifyBootOrder } from '$lib/api/jail/jail';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		reload: boolean;
	}

	let { open = $bindable(), jail, reload = $bindable(false) }: Props = $props();

	// svelte-ignore state_referenced_locally
	let startAtBoot = $state(jail.startAtBoot);
	let startOrder = $state(jail.startOrder);

	async function modify() {
		if (!jail) return;
		const response = await modifyBootOrder(jail.ctId, startAtBoot, Number(startOrder));
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify start order', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified start order', {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/3 overflow-hidden p-6 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			startAtBoot = jail.startAtBoot;
			startOrder = jail.startOrder;
		}}
		onClose={() => {
			startAtBoot = jail.startAtBoot;
			startOrder = jail.startOrder;
			open = false;
		}}
	>
		<Dialog.Header class="">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[basil--power-button-solid]"
					size="h-5 w-5"
					gap="gap-2"
					title="Start Order"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			label="Start Order"
			placeholder="1"
			bind:value={startOrder}
			classes="flex-1 space-y-1.5"
			type="number"
		/>

		<CustomCheckbox
			label="Start at Boot"
			bind:checked={startAtBoot}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">Save</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
