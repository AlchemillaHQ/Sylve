<script lang="ts">
	import { createJailFromTemplate } from '$lib/api/jail/jail';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		templateId: number;
		templateLabel: string;
		sourceCtId?: number;
		hostname?: string;
	}

	let {
		open = $bindable(),
		templateId,
		templateLabel,
		sourceCtId = 0,
		hostname
	}: Props = $props();

	let createMode = $state<'single' | 'multiple'>('single');
	let singleCTID = $state(sourceCtId || 0);
	let singleName = $state('');
	let multipleStartCTID = $state(sourceCtId || 0);
	let multipleCount = $state(1);
	let multipleNamePrefix = $state('');
	let actionLoading = $state(false);

	function normalizeTemplateName(label: string): string {
		return label.replace(/\s*\((?:CT\s*)?\d+\)\s*$/i, '').trim();
	}

	let templateName = $derived.by(() => {
		const cleaned = normalizeTemplateName(templateLabel);
		return cleaned || `Template ${templateId}`;
	});

	function resetForm() {
		createMode = 'single';
		singleCTID = sourceCtId || 0;
		singleName = '';
		multipleStartCTID = sourceCtId || 0;
		multipleCount = 1;
		multipleNamePrefix = templateName;
	}

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				resetForm();
			}
		}
	);

	async function create() {
		actionLoading = true;

		const result =
			createMode === 'single'
				? await createJailFromTemplate(
						templateId,
						{
							mode: 'single',
							ctid: Number(singleCTID),
							name: singleName || undefined
						},
						hostname
					)
				: await createJailFromTemplate(
						templateId,
						{
							mode: 'multiple',
							startCtid: Number(multipleStartCTID),
							count: Number(multipleCount),
							namePrefix: multipleNamePrefix || undefined
						},
						hostname
					);

		actionLoading = false;

		if (result.error) {
			toast.error('Failed to create jail from template', { position: 'bottom-center' });
			return;
		}

		open = false;
		reload.leftPanel = true;
		toast.success('Create jail request queued', { position: 'bottom-center' });
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-lg">
		<Dialog.Header class="p-0">
			<Dialog.Title>Create Jail - Template {templateName}</Dialog.Title>
		</Dialog.Header>
		<div class="grid gap-4 py-2">
			<div class="flex gap-2">
				<Button
					size="sm"
					variant={createMode === 'single' ? 'default' : 'outline'}
					onclick={() => (createMode = 'single')}>Single</Button
				>
				<Button
					size="sm"
					variant={createMode === 'multiple' ? 'default' : 'outline'}
					onclick={() => (createMode = 'multiple')}>Multiple</Button
				>
			</div>

			{#if createMode === 'single'}
				<div class="grid gap-2">
					<Label for={`single-ctid-${templateId}`}>CTID</Label>
					<Input id={`single-ctid-${templateId}`} type="number" min="1" bind:value={singleCTID} />
				</div>
				<div class="grid gap-2">
					<Label for={`single-name-${templateId}`}>Name (optional)</Label>
					<Input id={`single-name-${templateId}`} bind:value={singleName} />
				</div>
			{:else}
				<div class="grid gap-2">
					<Label for={`multi-start-${templateId}`}>Starting CTID</Label>
					<Input
						id={`multi-start-${templateId}`}
						type="number"
						min="1"
						bind:value={multipleStartCTID}
					/>
				</div>
				<div class="grid gap-2">
					<Label for={`multi-count-${templateId}`}>Count</Label>
					<Input id={`multi-count-${templateId}`} type="number" min="1" bind:value={multipleCount} />
				</div>
				<div class="grid gap-2">
					<Label for={`multi-prefix-${templateId}`}>Name Prefix</Label>
					<Input id={`multi-prefix-${templateId}`} bind:value={multipleNamePrefix} />
				</div>
			{/if}
		</div>
		<Dialog.Footer>
			<Button size="sm" disabled={actionLoading} onclick={() => void create()}>Create Jail</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
