<script lang="ts">
	import { modifyBootOrder } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { untrack } from 'svelte';

	interface Props {
		open: boolean;
		node: string;
		vm: VM;
		reload: boolean;
	}

	let { open = $bindable(), node, vm, reload = $bindable(false) }: Props = $props();

	let startAtBoot = $state(untrack(() => vm.startAtBoot));
	let startOrder = $state(untrack(() => vm.startOrder));
	let saving = $state(false);

	async function modify() {
		if (saving) return;
		const normalizedStartOrder = Number(startOrder);
		if (!Number.isSafeInteger(normalizedStartOrder) || normalizedStartOrder < 0) {
			toast.error('Start order must be a non-negative whole number', {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyBootOrder(vm.rid, startAtBoot, normalizedStartOrder, {
				hostname: node
			});
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify start order', { position: 'bottom-center' });
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No start-order changes needed'
					: 'Modified start order',
				{ position: 'bottom-center' }
			);

			reload = true;
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/3 overflow-hidden p-5 lg:max-w-2xl"
		showResetButton={true}
		onReset={() => {
			startAtBoot = vm.startAtBoot;
			startOrder = vm.startOrder;
		}}
		onClose={() => {
			startAtBoot = vm.startAtBoot;
			startOrder = vm.startOrder;
			open = false;
		}}
	>
		<Dialog.Header>
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
			disabled={saving}
		/>

		<CustomCheckbox
			label="Start at Boot"
			bind:checked={startAtBoot}
			classes="flex items-center gap-2"
			disabled={saving}
		></CustomCheckbox>

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
