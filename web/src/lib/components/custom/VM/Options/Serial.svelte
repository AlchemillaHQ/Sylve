<script lang="ts">
	import { modifySerialConsole } from '$lib/api/vm/vm';
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

	async function setSerial(enable: boolean) {
		if (saving) return;
		saving = true;
		try {
			const response = await modifySerialConsole(vm.rid, enable, { hostname: node });
			if (response.status !== 'success') {
				handleAPIError(response);
				toast.error('Failed to modify serial console', { position: 'bottom-center' });
				return;
			}

			toast.success(
				response.message === 'no_changes_detected'
					? 'No serial-console changes needed'
					: 'Modified serial console',
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
					icon="icon-[mdi--console]"
					size="h-5 w-5"
					gap="gap-2"
					title="Serial Console"
				/>
			</Dialog.Title>
		</Dialog.Header>

		{#if vm?.serial}
			<span class="text-sm text-justify">
				This VM currently has serial console access enabled. You can disable it using the button
				below.
			</span>
		{:else}
			<span class="text-sm text-justify">
				This VM currently has serial console access disabled. You can enable it using the button
				below.
			</span>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-0">
				{#if !vm?.serial}
					<Button onclick={() => setSerial(true)} type="submit" size="sm" disabled={saving}>
						{#if saving}
							<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
							Enabling...
						{:else}
							Enable
						{/if}
					</Button>
				{:else}
					<Button onclick={() => setSerial(false)} type="submit" size="sm" disabled={saving}>
						{#if saving}
							<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
							Disabling...
						{:else}
							Disable
						{/if}
					</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
