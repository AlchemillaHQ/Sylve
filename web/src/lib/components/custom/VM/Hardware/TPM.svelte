<script lang="ts">
	import { modifyTPM } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
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
	<Dialog.Content
		onClose={() => {
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[eos-icons--system-re-registered]"
					size="h-5 w-5"
					gap="gap-2"
					title="TPM Emulation"
				/>
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
