<script lang="ts">
	import { modifyBootOrder, modifyWoL } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { VM } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		vm: VM;
		reload: boolean;
	}

	let { open = $bindable(), vm, reload = $bindable(false) }: Props = $props();

	let startAtBoot = $state(vm.startAtBoot);
	let startOrder = $state(vm.startOrder);

	async function modify() {
		if (!vm) return;
		const response = await modifyBootOrder(vm.vmId, startAtBoot, Number(startOrder));
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
	<Dialog.Content class="w-1/3 overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header class="">
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[basil--power-button-solid] h-5 w-5"></span>

					<span>Start Order</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
						onclick={() => {
							startAtBoot = vm.startAtBoot;
							startOrder = vm.startOrder;
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
							startAtBoot = vm.startAtBoot;
							startOrder = vm.startOrder;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>

						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			label={'Start Order'}
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
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
