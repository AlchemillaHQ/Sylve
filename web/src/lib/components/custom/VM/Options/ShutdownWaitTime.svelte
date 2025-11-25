<script lang="ts">
	import { modifyShutdownWaitTime } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
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

	let shutdownWaitTime = $state(vm.shutdownWaitTime);

	async function modify() {
		if (!vm) return;

		if (isNaN(Number(shutdownWaitTime)) || Number(shutdownWaitTime) < 0) {
			toast.error('Shutdown Wait Time must be a non-negative number', {
				position: 'bottom-center'
			});
			return;
		}

		const response = await modifyShutdownWaitTime(vm.rid, Number(shutdownWaitTime));
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify shutdown wait time', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified shutdown wait time', {
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

					<span>Shutdown Wait Time</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
						onclick={() => {
							shutdownWaitTime = vm.shutdownWaitTime;
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
							shutdownWaitTime = vm.shutdownWaitTime;
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
			bind:value={shutdownWaitTime}
			classes="flex-1 space-y-1.5"
			type="number"
		/>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
