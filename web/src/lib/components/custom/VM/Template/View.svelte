<script lang="ts">
	import { getVMTemplateById } from '$lib/api/vm/vm';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import type { VMTemplate } from '$lib/types/vm/vm';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { isAPIResponse } from '$lib/utils/http';
	import { dateToAgo } from '$lib/utils/time';
	import { sleep } from '$lib/utils';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		templateId: number;
		templateLabel: string;
		hostname?: string;
	}

	let { open = $bindable(), templateId, templateLabel, hostname }: Props = $props();

	let loading = $state(false);
	let template = $state<VMTemplate | null>(null);

	let title = $derived.by(() => {
		return template?.name || templateLabel || `Template ${templateId}`;
	});

	function hasCloudInitData(vmTemplate: VMTemplate | null): boolean {
		if (!vmTemplate) {
			return false;
		}
		return Boolean(
			vmTemplate.cloudInitData?.trim() ||
			vmTemplate.cloudInitMetaData?.trim() ||
			vmTemplate.cloudInitNetworkConfig?.trim()
		);
	}

	async function loadTemplate() {
		loading = true;
		await sleep(300);
		try {
			const result = await getVMTemplateById(templateId, hostname);
			if (isAPIResponse(result) && result.status === 'error') {
				template = null;
				toast.error(result.error?.[0] || 'Failed to load template details', {
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
				template = null;
				void loadTemplate();
			}
		}
	);
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-5xl" onClose={() => (open = false)}>
		<Dialog.Header class="p-0">
			<Dialog.Title class="text-left">
				<SpanWithIcon
					icon="icon-[mdi--monitor-shimmer]"
					size="h-5 w-5"
					gap="gap-2"
					title="VM Template - {title}"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-4 flex flex-1 flex-col overflow-y-auto">
			{#if loading}
				<div
					class="flex h-[65vh] w-full items-center justify-center text-muted-foreground md:h-[55vh] lg:h-[45vh]"
				>
					<span class="icon-[mdi--loading] h-9 w-9 animate-spin text-primary"></span>
				</div>
			{:else if template}
				<Tabs.Root value="basic" class="flex h-full w-full flex-col overflow-hidden">
					<Tabs.List class="grid w-full grid-cols-4 p-0">
						<Tabs.Trigger class="border-b" value="basic">Basic</Tabs.Trigger>
						<Tabs.Trigger class="border-b" value="storage">Storage</Tabs.Trigger>
						<Tabs.Trigger class="border-b" value="network">Network</Tabs.Trigger>
						<Tabs.Trigger class="border-b" value="cloud-init">Cloud-Init</Tabs.Trigger>
					</Tabs.List>

					<ScrollArea class="h-[65vh] w-full p-4 md:h-[55vh] lg:h-[45vh]">
						<Tabs.Content value="basic" class="m-0 space-y-4 outline-none">
							<div class="grid gap-4 md:grid-cols-2">
								<div class="border rounded-md p-4 text-sm space-y-2">
									<div class="text-xs text-muted-foreground">Template ID</div>
									<div class="font-medium">{template.id}</div>
									<div class="text-xs text-muted-foreground">Source VM</div>
									<div class="font-medium">{template.sourceVmName || '-'}</div>
									<div class="text-xs text-muted-foreground">Updated</div>
									<div class="font-medium">{dateToAgo(template.updatedAt)}</div>
								</div>

								<div class="border rounded-md p-4 text-sm space-y-2">
									<div class="text-xs text-muted-foreground">CPU</div>
									<div class="font-medium">
										{template.cpuSockets}S / {template.cpuCores}C / {template.cpuThreads}T
									</div>
									<div class="text-xs text-muted-foreground">RAM</div>
									<div class="font-medium">{formatBytesBinary(template.ram || 0)}</div>
									<div class="flex gap-2 pt-1">
										<Badge variant={template.tpmEmulation ? 'default' : 'outline'}>TPM</Badge>
										<Badge variant={template.serial ? 'default' : 'outline'}>Serial</Badge>
										<Badge variant={template.vncEnabled ? 'default' : 'outline'}>VNC</Badge>
									</div>
								</div>
							</div>
						</Tabs.Content>

						<Tabs.Content value="storage" class="m-0 space-y-3 outline-none">
							{#if template.storages.length === 0}
								<div class="text-muted-foreground py-4 text-sm italic">
									No cloneable storage in template
								</div>
							{:else}
								{#each template.storages as storage, index (`tmpl-storage-${index}`)}
									<div class="border rounded-md p-3 text-sm">
										<div class="font-medium">
											Storage #{storage.sourceStorageId} ({storage.type.toUpperCase()})
										</div>
										<div class="text-xs text-muted-foreground">
											Pool {storage.pool} • Size {formatBytesBinary(storage.size || 0)} • Boot order {storage.bootOrder}
										</div>
									</div>
								{/each}
							{/if}
						</Tabs.Content>

						<Tabs.Content value="network" class="m-0 space-y-3 outline-none">
							{#if template.networks.length === 0}
								<div class="text-muted-foreground py-4 text-sm italic">
									No network interfaces in template
								</div>
							{:else}
								{#each template.networks as network, idx (`tmpl-network-${idx}`)}
									<div class="border rounded-md p-3 text-sm">
										<div class="font-medium">Network #{idx + 1}</div>
										<div class="text-xs text-muted-foreground">
											{network.switchType} switch: {network.switchName} • {network.emulation}
										</div>
									</div>
								{/each}
							{/if}
						</Tabs.Content>

						<Tabs.Content value="cloud-init" class="m-0 space-y-3 outline-none">
							{#if hasCloudInitData(template)}
								<div class="space-y-2">
									<div class="text-xs text-muted-foreground">User Data</div>
									<Textarea
										value={template.cloudInitData || ''}
										readonly
										class="min-h-32 font-mono text-xs"
									/>
								</div>
								<div class="space-y-2">
									<div class="text-xs text-muted-foreground">Meta Data</div>
									<Textarea
										value={template.cloudInitMetaData || ''}
										readonly
										class="min-h-32 font-mono text-xs"
									/>
								</div>
								<div class="space-y-2">
									<div class="text-xs text-muted-foreground">Network Config</div>
									<Textarea
										value={template.cloudInitNetworkConfig || ''}
										readonly
										class="min-h-32 font-mono text-xs"
									/>
								</div>
							{:else}
								<div class="text-muted-foreground py-4 text-sm italic">
									Cloud-init data is not set on this template
								</div>
							{/if}
						</Tabs.Content>
					</ScrollArea>
				</Tabs.Root>
			{:else}
				<div class="text-muted-foreground py-8 text-center text-sm">Template data unavailable</div>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
