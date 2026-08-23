<script lang="ts">
	import { modifyWoL } from '$lib/api/vm/vm';
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
	let wol = $state(untrack(() => vm.wol));
	let saving = $state(false);

	async function modify() {
		if (saving) return;
		saving = true;
		try {
			const response = await modifyWoL(vm.rid, wol, { hostname: node });
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify WoL setting', { position: 'bottom-center' });
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No WoL changes needed'
					: 'Modified WoL setting',
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
			wol = vm.wol;
		}}
		onClose={() => {
			wol = vm.wol;
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[arcticons--wakeonlan]"
					size="h-5 w-5"
					gap="gap-2"
					title="Wake on LAN"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<span class="text-muted-foreground text-justify text-sm">
			Setting this option to be <b>on</b> will enable Wake on LAN for this VM for all MAC addresses attached
			to it
		</span>
		<CustomCheckbox
			label="WoL"
			bind:checked={wol}
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
