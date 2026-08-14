<script lang="ts">
	import { modifyIgnoreUMSR } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
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
	let ignoreUMSR: boolean = $state(untrack(() => vm.ignoreUMSR));
	let saving = $state(false);

	async function modify() {
		if (saving) return;
		saving = true;
		try {
			const response = await modifyIgnoreUMSR(vm.rid, ignoreUMSR, { hostname: node });
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify unimplemented MSRs access setting', {
					position: 'bottom-center'
				});
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No unimplemented-MSR changes needed'
					: 'Modified unimplemented MSRs access setting',
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
			ignoreUMSR = vm.ignoreUMSR;
		}}
		onClose={() => {
			ignoreUMSR = vm.ignoreUMSR;
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[eos-icons--system-re-registered]"
					size="h-5 w-5"
					gap="gap-2"
					title="Ignore Unknown MSR Accesses"
				/>
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
