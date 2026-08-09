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
		node: string;
		vm: VM;
		reload: boolean;
	}

	let { open = $bindable(), node, vm, reload = $bindable(false) }: Props = $props();
	let saving = $state(false);

	async function modify() {
		if (saving) return;
		const tpmEmulation = !vm.tpmEmulation;
		saving = true;
		try {
			const response = await modifyTPM(vm.rid, tpmEmulation, { hostname: node });

			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error(`Failed to ${tpmEmulation ? 'enable' : 'disable'} TPM emulation`, {
					position: 'bottom-center'
				});
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No TPM-emulation changes needed'
					: `TPM emulation ${tpmEmulation ? 'enabled' : 'disabled'}`,
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
				<Button onclick={modify} type="submit" size="sm" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
						Saving...
					{:else if vm.tpmEmulation}
						Disable TPM
					{:else}
						Enable TPM
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
