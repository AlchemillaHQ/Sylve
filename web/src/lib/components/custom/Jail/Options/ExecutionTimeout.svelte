<script lang="ts">
	import { modifyExecutionTimeout } from '$lib/api/jail/options';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
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
	let execTimeout = $state(jail.execTimeout);
	let saving = $state(false);

	function reset() {
		execTimeout = jail.execTimeout;
	}

	async function modify() {
		if (saving) return;
		const normalizedTimeout = Number(execTimeout);
		if (!Number.isSafeInteger(normalizedTimeout) || normalizedTimeout < 1) {
			toast.error('Execution timeout must be a whole number greater than zero', {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyExecutionTimeout(jail.ctId, normalizedTimeout, {
				hostname: node
			});
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to modify execution timeout', { position: 'bottom-center' });
				return;
			}

			await onSaved();
			toast.success('Execution timeout modified', { position: 'bottom-center' });
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
					icon="icon-[mdi--timer-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Execution Timeout"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-2">
			<CustomValueInput
				label="Execution Timeout (Seconds)"
				placeholder="120"
				bind:value={execTimeout}
				classes="flex-1 space-y-1.5"
				type="number"
				disabled={saving}
			/>
			<p class="text-muted-foreground text-xs">
				Applies to each exec.* command, including jail startup and shutdown.
			</p>
		</div>

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
