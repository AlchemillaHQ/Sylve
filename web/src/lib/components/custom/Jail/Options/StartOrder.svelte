<script lang="ts">
	import { modifyBootOrder } from '$lib/api/jail/options';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Jail } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		node: string;
		onSaved: () => void | Promise<void>;
	}

	let { open = $bindable(), jail, node, onSaved }: Props = $props();
	// This dialog is remounted for each jail edit, so props are the intended initial state.
	// svelte-ignore state_referenced_locally
	let startAtBoot = $state(jail.startAtBoot);
	// svelte-ignore state_referenced_locally
	let startOrder = $state(jail.startOrder);
	let saving = $state(false);

	function reset() {
		startAtBoot = jail.startAtBoot;
		startOrder = jail.startOrder;
	}

	async function modify() {
		if (saving) return;
		const normalizedStartOrder = Number(startOrder);
		if (!Number.isSafeInteger(normalizedStartOrder) || normalizedStartOrder < 0) {
			toast.error('Start order must be a non-negative whole number', {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyBootOrder(jail.ctId, startAtBoot, normalizedStartOrder, {
				hostname: node
			});
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to modify start order', { position: 'bottom-center' });
				return;
			}

			await onSaved();
			toast.success('Start order modified', { position: 'bottom-center' });
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-1/3 overflow-hidden p-6 lg:max-w-2xl"
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={reset}
		onClose={() => {
			if (saving) return;
			reset();
			open = false;
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		aria-busy={saving}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[basil--power-button-solid]"
					size="h-5 w-5"
					gap="gap-2"
					title="Start Order"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<CustomValueInput
			label="Start Order"
			placeholder="1"
			bind:value={startOrder}
			classes="flex-1 space-y-1.5"
			type="number"
		/>

		<CustomCheckbox
			label="Start at Boot"
			bind:checked={startAtBoot}
			classes="flex items-center gap-2"
		/>

		<Dialog.Footer class="flex justify-end">
			<Button onclick={modify} type="submit" size="sm" disabled={saving} aria-busy={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					Saving...
				{:else}
					Save
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
