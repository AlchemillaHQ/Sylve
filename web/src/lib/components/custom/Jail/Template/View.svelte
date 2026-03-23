<script lang="ts">
	import { getJailTemplateById } from '$lib/api/jail/jail';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { JailTemplate } from '$lib/types/jail/jail';
	import { isAPIResponse } from '$lib/utils/http';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		templateId: number;
		templateLabel: string;
		hostname?: string;
	}

	let { open = $bindable(), templateId, templateLabel, hostname }: Props = $props();

	let loading = $state(false);
	let template = $state<JailTemplate | null>(null);

	let title = $derived.by(() => {
		return template?.name || templateLabel || `Template ${templateId}`;
	});

	async function loadTemplate() {
		loading = true;
		try {
			const result = await getJailTemplateById(templateId, hostname);
			if (isAPIResponse(result) && result.status === 'error') {
				template = null;
				toast.error(result.error || 'Failed to load template details', {
					position: 'bottom-center'
				});
				return;
			}

			template = result;
		} catch {
			template = null;
			toast.error('Failed to load template details', { position: 'bottom-center' });
		} finally {
			loading = false;
		}
	}

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				void loadTemplate();
			} else {
				template = null;
			}
		}
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-3xl">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--file-tree-outline] h-5 w-5"></span>
					<span>Template Details - {title}</span>
				</div>

				<Button size="sm" variant="link" class="h-4" onclick={() => (open = false)} title={'Close'}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">{'Close'}</span>
				</Button>
			</Dialog.Title>
		</Dialog.Header>

		<div class="max-h-[70vh] overflow-y-auto py-2">
			{#if loading}
				<div class="text-muted-foreground flex items-center gap-2 py-8 text-sm">
					<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					<span>Loading template details...</span>
				</div>
			{:else if template}
				<div class="grid grid-cols-1 gap-2 text-sm md:grid-cols-2">
					<div><span class="font-medium">ID:</span> {template.id}</div>
					<div><span class="font-medium">Source CTID:</span> {template.sourceCtId}</div>
					<div><span class="font-medium">Source Jail:</span> {template.sourceJailName || '-'}</div>
					<div><span class="font-medium">Type:</span> {template.type}</div>
					<div><span class="font-medium">Pool:</span> {template.pool}</div>
					<div><span class="font-medium">Root Dataset:</span> {template.rootDataset}</div>
					<div><span class="font-medium">Resource Limits:</span> {template.resourceLimits ?? true}</div>
					<div><span class="font-medium">CPU Cores:</span> {template.cores}</div>
					<div><span class="font-medium">Memory (bytes):</span> {template.memory}</div>
					<div><span class="font-medium">Inherit IPv4:</span> {template.inheritIPv4}</div>
					<div><span class="font-medium">Inherit IPv6:</span> {template.inheritIPv6}</div>
					<div><span class="font-medium">Created:</span> {template.createdAt}</div>
					<div><span class="font-medium">Updated:</span> {template.updatedAt}</div>
				</div>

				<div class="mt-4 grid grid-cols-1 gap-2 text-sm md:grid-cols-2">
					<div>
						<div class="font-medium">Network Templates</div>
						<div>{template.networks.length}</div>
						{#if template.networks.length > 0}
							<div class="text-muted-foreground mt-1 text-xs">
								{#each template.networks as network, index}
									<div>#{index + 1} {network.name} ({network.switchType}, switch {network.switchId})</div>
								{/each}
							</div>
						{/if}
					</div>

					<div>
						<div class="font-medium">Hook Templates</div>
						<div>{template.hooks.length}</div>
						{#if template.hooks.length > 0}
							<div class="text-muted-foreground mt-1 text-xs">
								{#each template.hooks as hook, index}
									<div>#{index + 1} {hook.phase} ({hook.enabled ? 'enabled' : 'disabled'})</div>
								{/each}
							</div>
						{/if}
					</div>
				</div>

				{#if template.allowedOptions.length > 0}
					<div class="mt-4 text-sm">
						<div class="font-medium">Allowed Options</div>
						<div class="text-muted-foreground mt-1 text-xs">{template.allowedOptions.join(', ')}</div>
					</div>
				{/if}

				{#if template.additionalOptions}
					<div class="mt-4 text-sm">
						<div class="font-medium">Additional Options</div>
						<pre class="bg-muted mt-1 rounded-md p-2 text-xs whitespace-pre-wrap">{template.additionalOptions}</pre>
					</div>
				{/if}

				{#if template.fstab}
					<div class="mt-4 text-sm">
						<div class="font-medium">FStab</div>
						<pre class="bg-muted mt-1 rounded-md p-2 text-xs whitespace-pre-wrap">{template.fstab}</pre>
					</div>
				{/if}

				{#if template.resolvConf}
					<div class="mt-4 text-sm">
						<div class="font-medium">resolv.conf</div>
						<pre class="bg-muted mt-1 rounded-md p-2 text-xs whitespace-pre-wrap">{template.resolvConf}</pre>
					</div>
				{/if}
			{:else}
				<div class="text-muted-foreground py-8 text-sm">No template details available.</div>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
