<script lang="ts">
	import { saveRegistryCredential } from '$lib/api/container/resources';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { RegistryCredential } from '$lib/types/container/container';
	import { toastContainerError } from '$lib/utils/container/errors';
	import { isAPIErrorResponse } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		node: string;
		credential?: RegistryCredential;
		afterChange: () => void;
	}

	let { open = $bindable(), node, credential, afterChange }: Props = $props();

	// svelte-ignore state_referenced_locally
	let form = $state({
		registry: credential?.registry ?? '',
		username: credential?.username ?? '',
		password: ''
	});
	let saving = $state(false);

	async function save() {
		saving = true;
		const result = await saveRegistryCredential(form, node);
		saving = false;
		if (isAPIErrorResponse(result)) {
			toastContainerError(result);
			return;
		}

		toast.success(`Credentials for ${result.registry} saved`, { position: 'bottom-center' });
		open = false;
		afterChange();
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[96%] p-5 md:max-w-md"
		showCloseButton={!saving}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--key-chain-variant]"
					size="h-5 w-5"
					gap="gap-2"
					title={credential ? `Edit Registry — ${credential.registry}` : 'New Registry'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<form
			class="space-y-4"
			onsubmit={(e) => {
				e.preventDefault();
				save();
			}}
		>
			<CustomValueInput
				label="Registry"
				placeholder="ghcr.io"
				bind:value={form.registry}
				disabled={!!credential}
			/>
			<CustomValueInput
				label="Username"
				placeholder="username"
				autocomplete="username"
				bind:value={form.username}
			/>
			<CustomValueInput
				label="Password"
				placeholder="Password or access token"
				type="password"
				autocomplete="new-password"
				revealOnFocus
				bind:value={form.password}
			/>

			<Dialog.Footer>
				<Button size="sm" type="submit" disabled={saving}>
					{#if saving}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					{/if}
					Save
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
