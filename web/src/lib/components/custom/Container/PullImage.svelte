<script lang="ts">
	import { pullContainerImage } from '$lib/api/container/resources';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { toastContainerError } from '$lib/utils/container/errors';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		node: string;
		pool: string;
		afterChange: () => void;
	}

	let { open = $bindable(), node, pool, afterChange }: Props = $props();

	let image = $state('');
	let os = $state('');
	let pulling = $state(false);

	const osOptions = [
		{ value: '', label: 'Auto (prefer FreeBSD)' },
		{ value: 'freebsd', label: 'FreeBSD' },
		{ value: 'linux', label: 'Linux' }
	];

	async function pull() {
		pulling = true;
		const result = await pullContainerImage(pool, image.trim(), os, node);
		pulling = false;
		if (result.status === 'error') {
			toastContainerError(result);
			return;
		}

		toast.success(`Pulled ${image.trim()}`, { position: 'bottom-center' });
		open = false;
		afterChange();
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[96%] p-5 md:max-w-lg"
		showCloseButton={!pulling}
		onClose={() => (open = false)}
		onEscapeKeydown={(event) => {
			if (pulling) event.preventDefault();
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--download]"
					size="h-5 w-5"
					gap="gap-2"
					title={`Pull Image — ${pool}`}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-4">
			<CustomValueInput
				label="Image"
				placeholder="docker.io/library/nginx:latest"
				bind:value={image}
			/>
			<SimpleSelect
				label="OS"
				placeholder="Auto (prefer FreeBSD)"
				options={osOptions}
				bind:value={os}
				onChange={(value) => (os = value)}
				classes={{
					parent: 'space-y-1.5',
					label: 'flex h-7 items-center text-sm',
					trigger: 'w-full'
				}}
			/>
		</div>

		<Dialog.Footer>
			<Button size="sm" onclick={pull} disabled={pulling}>
				{#if pulling}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					Pulling...
				{:else}
					Pull
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
