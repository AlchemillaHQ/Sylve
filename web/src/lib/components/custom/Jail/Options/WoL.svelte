<script lang="ts">
	import { modifyWoL } from '$lib/api/jail/options';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
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
	// svelte-ignore state_referenced_locally
	let wol = $state(jail.wol);
	let saving = $state(false);

	function reset() {
		wol = jail.wol;
	}

	async function modify() {
		if (saving) return;
		saving = true;
		try {
			const response = await modifyWoL(jail.ctId, wol, { hostname: node });
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to modify WoL setting', { position: 'bottom-center' });
				return;
			}

			await onSaved();
			toast.success('WoL setting modified', { position: 'bottom-center' });
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
					icon="icon-[arcticons--wakeonlan]"
					size="h-5 w-5"
					gap="gap-2"
					title="Wake on LAN"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<span class="text-muted-foreground text-justify text-sm">
			Enabling this setting turns on Wake on LAN for every MAC address attached to this jail.
		</span>
		<CustomCheckbox label="WoL" bind:checked={wol} classes="flex items-center gap-2" />

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
