<script lang="ts">
	import { modifyIgnoreUMSR } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
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
	let ignoreUMSR: boolean = $state(vm.ignoreUMSR);

	async function modify() {
		if (!vm) return;
		const response = await modifyIgnoreUMSR(vm.vmId, ignoreUMSR);
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to modify unimplemented MSRs access setting', {
				position: 'bottom-center'
			});
			return;
		}

		toast.success('Modified unimplemented MSRs access setting', {
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
					<span class="icon-[eos-icons--system-re-registered] h-5 w-5"></span>
					<span>Ignore Unknown MSR Accesses</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						title={'Reset'}
						class="h-4 "
						onclick={() => {
							ignoreUMSR = vm.ignoreUMSR;
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
							ignoreUMSR = vm.ignoreUMSR;
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<span class="text-muted-foreground text-justify text-sm">
			Enable this option to ignore accesses to unimplemented Model-Specific Registers (MSRs) by the
			VM. This can help prevent crashes or instability caused by such accesses, but may also lead to
			unexpected behavior if the guest OS relies on these MSRs.
		</span>
		<CustomCheckbox
			label="Ignore Unimplemented MSR Accesses"
			bind:checked={ignoreUMSR}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">{'Save'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
