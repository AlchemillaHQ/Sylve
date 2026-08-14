<script lang="ts">
	import { modifyBootRom } from '$lib/api/vm/vm';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Architecture } from '$lib/types/info/cpu';
	import type { VM, VMBootRom } from '$lib/types/vm/vm';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { untrack } from 'svelte';

	interface Props {
		open: boolean;
		node: string;
		architecture: Architecture;
		vm: VM;
		reload: boolean;
	}

	let { open = $bindable(), node, architecture, vm, reload = $bindable(false) }: Props = $props();
	let bootROMOptions = $derived(
		architecture === 'arm64'
			? [
					{ label: 'U-Boot (Default)', value: 'uboot' },
					{ label: 'None', value: 'none' }
				]
			: [
					{ label: 'UEFI (Default)', value: 'uefi' },
					{ label: 'None', value: 'none' }
				]
	);

	let comboBox = $state({
		open: false,
		value: untrack(() => vm.bootRom)
	});
	let saving = $state(false);

	async function modify() {
		if (saving) return;
		saving = true;
		try {
			const response = await modifyBootRom(vm.rid, comboBox.value as VMBootRom, {
				hostname: node
			});
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify boot ROM', { position: 'bottom-center' });
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No boot-ROM changes needed'
					: 'Modified boot ROM',
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
			comboBox.value = vm.bootRom;
		}}
		onClose={() => {
			comboBox.value = vm.bootRom;
			open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon icon="icon-[mdi--chip]" size="h-5 w-5" gap="gap-2" title="Boot ROM" />
			</Dialog.Title>
		</Dialog.Header>

		<ComboBox
			bind:open={comboBox.open}
			label="Boot ROM"
			bind:value={comboBox.value}
			data={bootROMOptions}
			classes="flex-1 space-y-1"
			placeholder="Select boot ROM"
			width="w-full"
			disabled={saving}
		></ComboBox>

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
