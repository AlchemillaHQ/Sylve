<script lang="ts">
	import { modifyTPM } from '$lib/api/vm/vm';
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

	async function modify() {
		if (!vm) return;
		const tpmEmulation = !vm.tpmEmulation;
		const response = await modifyTPM(vm.rid, tpmEmulation);

		if (response.error) {
			handleAPIError(response);
			toast.error(`Failed to ${tpmEmulation ? 'enable' : 'disable'} TPM emulation`, {
				position: 'bottom-center'
			});
			return;
		}

		toast.success(`TPM emulation ${tpmEmulation ? 'enabled' : 'disabled'}`, {
			position: 'bottom-center'
		});

		reload = true;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[eos-icons--system-re-registered] h-5 w-5"></span>
					<span>TPM Emulation</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<span class="text-muted-foreground text-justify text-sm">
			Enable or disable TPM (Trusted Platform Module) emulation for this virtual machine. Disabling
			this option after it has been enabled may lead to boot issues if the guest OS relies on TPM
			functionality.
		</span>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={modify} type="submit" size="sm">
					{#if vm.tpmEmulation}
						Disable
					{:else}
						Enable
					{/if} TPM
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
