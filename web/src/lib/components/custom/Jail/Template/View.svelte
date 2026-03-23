<script lang="ts">
	import { getJailTemplateById } from '$lib/api/jail/jail';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import { Badge } from '$lib/components/ui/badge/index.js';
	import type { JailTemplate } from '$lib/types/jail/jail';
	import { isAPIResponse } from '$lib/utils/http';
	import { dateToAgo } from '$lib/utils/time';
	import humanFormat from 'human-format';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import { sleep } from '$lib/utils';

	interface Props {
		open: boolean;
		templateId: number;
		templateLabel: string;
		hostname?: string;
	}

	let { open = $bindable(), templateId, templateLabel, hostname }: Props = $props();

	let loading = $state(false);
	let template = $state<JailTemplate | null>(null);

	let selectedHook = $state<{ phase: string; script: string; enabled: boolean } | null>(null);
	let hookModalOpen = $state(false);

	function formatHookPhase(phase: string): string {
		const phaseLower = phase.toLowerCase();
		switch (phaseLower) {
			case 'prestart':
				return 'Pre Start';
			case 'start':
				return 'Start';
			case 'poststart':
				return 'Post Start';
			case 'prestop':
				return 'Pre Stop';
			case 'stop':
				return 'Stop';
			case 'poststop':
				return 'Post Stop';
			default:
				return phase.charAt(0).toUpperCase() + phase.slice(1);
		}
	}

	let title = $derived.by(() => {
		return template?.name || templateLabel || `Template ${templateId}`;
	});

	async function loadTemplate() {
		loading = true;
		await sleep(500);
		try {
			const result = await getJailTemplateById(templateId, hostname);
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
	<Dialog.Content class="max-w-5xl">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[mdi--file-tree-outline] h-5 w-5"></span>
					<span>Template Details - {title.replaceAll('Template', '')}</span>
				</div>

				<Button size="sm" variant="link" class="h-4" onclick={() => (open = false)} title={'Close'}>
					<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
					<span class="sr-only">{'Close'}</span>
				</Button>
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
				<Tabs.Root value="basic" class="w-full overflow-hidden flex flex-col h-full">
					<Tabs.List class="grid w-full grid-cols-4 p-0">
						<Tabs.Trigger class="border-b" value="basic">Basic</Tabs.Trigger>
						<Tabs.Trigger class="border-b" value="network">Network</Tabs.Trigger>
						<Tabs.Trigger class="border-b" value="storage">Storage</Tabs.Trigger>
						<Tabs.Trigger class="border-b" value="advanced">Advanced</Tabs.Trigger>
					</Tabs.List>

					<ScrollArea class="h-[65vh] md:h-[55vh] lg:h-[45vh] w-full p-4">
						<Tabs.Content value="basic" class="space-y-4 m-0 outline-none">
							<div class="flex flex-col gap-4">
								<div class="border rounded-md overflow-hidden h-fit">
									<div class="bg-muted flex items-center gap-2 px-4 py-2">
										<span class="icon-[mdi--information] text-primary h-5 w-5"></span>
										<span class="font-semibold">Basic Details</span>
									</div>
									<div class="p-4 grid grid-cols-1 gap-4 text-sm md:grid-cols-2">
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">ID</span><span class="font-medium"
												>{template.id}</span
											>
										</div>
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">Type</span>
											<div class="mt-0.5">
												{#if template.type === 'linux'}
													<Badge class="mt-0.5 bg-emerald-600/80 text-white font-semibold">
														<span class="icon icon-[uil--linux] mr-1 text-sm bg-white"></span>
														Linux
													</Badge>
												{:else if template.type === 'freebsd'}
													<Badge class="mt-0.5 bg-red-600/80 text-white font-semibold">
														<span class="icon icon-[mdi--freebsd] mr-1 text-sm bg-white"></span>
														FreeBSD
													</Badge>
												{:else}
													<span class="text-xs text-muted-foreground">{template.type}</span>
												{/if}
											</div>
										</div>
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">Source CTID</span><span
												class="font-medium">{template.sourceCtId}</span
											>
										</div>
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">Source Jail</span><span
												class="font-medium">{template.sourceJailName || '-'}</span
											>
										</div>
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">Created</span><span
												class="font-medium">{dateToAgo(template.createdAt)}</span
											>
										</div>
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">Updated</span><span
												class="font-medium">{dateToAgo(template.updatedAt)}</span
											>
										</div>
									</div>
								</div>

								<div class="border rounded-md overflow-hidden h-fit">
									<div class="bg-muted flex items-center gap-2 px-4 py-2">
										<span class="icon-[mdi--memory] text-primary h-5 w-5"></span>
										<span class="font-semibold">Hardware Limits</span>
									</div>
									<div class="p-4 grid grid-cols-3 gap-4 text-sm">
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">CPU Cores</span>
											<span class="font-medium text-amber-600 dark:text-amber-400"
												>{template.cores}</span
											>
										</div>
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">Memory</span>
											<span class="font-medium text-emerald-600 dark:text-emerald-400"
												>{humanFormat(template.memory || 0, { scale: 'SI', unit: 'B' })}</span
											>
										</div>
										<div class="flex flex-col">
											<span class="text-muted-foreground text-xs">Resource Limits</span>
											<div class="mt-0.5">
												{#if template.resourceLimits ?? true}
													<Badge class="mt-0.5 bg-green-600/80 text-white font-semibold"
														>Enabled</Badge
													>
												{:else}
													<Badge class="mt-0.5 bg-red-600/80 text-white font-semibold"
														>Disabled</Badge
													>
												{/if}
											</div>
										</div>
									</div>
								</div>
							</div>
						</Tabs.Content>

						<Tabs.Content value="network" class="space-y-4 m-0 outline-none">
							<div class="border rounded-md overflow-hidden">
								<div class="bg-muted flex items-center gap-2 px-4 py-2">
									<span class="icon-[mdi--network] text-primary h-5 w-5"></span>
									<span class="font-semibold">Inheritance</span>
								</div>
								<div class="p-4 grid grid-cols-1 gap-4 text-sm md:grid-cols-2">
									<div class="flex flex-col">
										<span class="text-muted-foreground text-xs">Inherit IPv4</span>
										<div class="mt-0.5">
											<Badge
												class="mt-0.5 {template.inheritIPv4
													? 'bg-green-600/80'
													: 'bg-gray-600/80'} text-white font-semibold"
											>
												{template.inheritIPv4 ? 'Yes' : 'No'}
											</Badge>
										</div>
									</div>
									<div class="flex flex-col">
										<span class="text-muted-foreground text-xs">Inherit IPv6</span>
										<div class="mt-0.5">
											<Badge
												class="mt-0.5 {template.inheritIPv6
													? 'bg-green-600/80'
													: 'bg-gray-600/80'} text-white font-semibold"
											>
												{template.inheritIPv6 ? 'Yes' : 'No'}
											</Badge>
										</div>
									</div>
								</div>
							</div>

							<div class="border rounded-md overflow-hidden">
								<div class="bg-muted flex items-center justify-between px-4 py-2">
									<div class="flex gap-2 items-center">
										<span class="icon-[mdi--router-wireless] text-primary h-5 w-5"></span>
										<span class="font-semibold">Network Interfaces</span>
									</div>
									<Badge variant="secondary">{template.networks.length}</Badge>
								</div>
								<div class="p-4">
									{#if template.networks.length > 0}
										<div class="grid gap-2">
											{#each template.networks as network, index}
												<div class="border rounded-md p-3 flex flex-col gap-2 bg-muted/30">
													<div class="font-medium text-sm flex items-center justify-between gap-2">
														<div class="flex items-center gap-2">
															<span class="text-muted-foreground">#{index + 1}</span>
															{network.name}
														</div>
														{#if network.switchType === 'standard'}
															<Badge
																variant="outline"
																class="border-blue-200 bg-blue-50/50 text-blue-700 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-300"
															>
																<span class="icon-[mdi--lan] mr-1"></span> Standard
															</Badge>
														{:else if network.switchType === 'manual'}
															<Badge
																variant="outline"
																class="border-orange-200 bg-orange-50/50 text-orange-700 dark:border-orange-800 dark:bg-orange-900/20 dark:text-orange-300"
															>
																<span class="icon-[mdi--wrench-outline] mr-1"></span> Manual
															</Badge>
														{:else}
															<Badge variant="outline">{network.switchType}</Badge>
														{/if}
													</div>
													<div class="grid grid-cols-3 gap-2 text-xs text-muted-foreground">
														<div class="flex flex-col">
															<span class="font-medium">Switch ID</span>
															<span>{network.switchId}</span>
														</div>
														<div class="flex flex-col">
															<span class="font-medium">DHCP</span>
															<span>{network.dhcp ? 'Yes' : 'No'}</span>
														</div>
														<div class="flex flex-col">
															<span class="font-medium">SLAAC</span>
															<span>{network.slaac ? 'Yes' : 'No'}</span>
														</div>
													</div>
												</div>
											{/each}
										</div>
									{:else}
										<div class="text-muted-foreground text-center text-sm py-4 italic">
											No network interfaces defined
										</div>
									{/if}
								</div>
							</div>

							{#if template.resolvConf}
								<div class="border rounded-md overflow-hidden">
									<div class="bg-muted flex items-center gap-2 px-4 py-2">
										<span class="icon-[mdi--dns] text-primary h-5 w-5"></span>
										<span class="font-semibold">resolv.conf</span>
									</div>
									<div class="bg-zinc-950 p-4 overflow-x-auto text-green-400 font-mono text-xs">
										<pre>{template.resolvConf}</pre>
									</div>
								</div>
							{/if}
						</Tabs.Content>

						<Tabs.Content value="storage" class="space-y-4 m-0 outline-none">
							<div class="border rounded-md overflow-hidden">
								<div class="bg-muted flex items-center gap-2 px-4 py-2">
									<span class="icon-[mdi--database] text-primary h-5 w-5"></span>
									<span class="font-semibold">ZFS Storage</span>
								</div>
								<div class="p-4 grid grid-cols-1 gap-4 text-sm md:grid-cols-2">
									<div class="flex flex-col">
										<span class="text-muted-foreground text-xs">Pool</span><span class="font-medium"
											>{template.pool}</span
										>
									</div>
									<div class="flex flex-col">
										<span class="text-muted-foreground text-xs">Root Dataset</span><span
											class="font-medium">{template.rootDataset}</span
										>
									</div>
								</div>
							</div>

							{#if template.fstab}
								<div class="border rounded-md overflow-hidden">
									<div class="bg-muted flex items-center gap-2 px-4 py-2">
										<span class="icon-[mdi--folder-account] text-primary h-5 w-5"></span>
										<span class="font-semibold">FStab Mounts</span>
									</div>
									<div class="bg-zinc-950 p-4 overflow-x-auto text-green-400 font-mono text-xs">
										<pre>{template.fstab}</pre>
									</div>
								</div>
							{/if}
						</Tabs.Content>

						<Tabs.Content value="advanced" class="space-y-4 m-0 outline-none">
							<div class="border rounded-md overflow-hidden">
								<div class="bg-muted flex items-center justify-between px-4 py-2">
									<div class="flex gap-2 items-center">
										<span class="icon-[mdi--hook] text-primary h-5 w-5"></span>
										<span class="font-semibold">Pre/Post Hooks</span>
									</div>
									<Badge variant="secondary">{template.hooks.length}</Badge>
								</div>
								<div class="p-4">
									{#if template.hooks.length > 0}
										<div class="grid grid-cols-1 md:grid-cols-2 gap-2">
											{#each template.hooks as hook, index}
												<button
													class="border rounded-md px-3 py-2 flex justify-between items-center text-sm bg-muted/30 hover:bg-muted/50 transition-colors cursor-pointer text-left"
													onclick={() => {
														selectedHook = hook;
														hookModalOpen = true;
													}}
												>
													<div class="flex items-center gap-2">
														<span class="text-muted-foreground text-xs">#{index + 1}</span>
														<span class="font-medium">{formatHookPhase(hook.phase)}</span>
													</div>
													<Badge
														class="{hook.enabled
															? 'bg-green-600/80'
															: 'bg-gray-600/80'} text-white font-semibold"
													>
														{hook.enabled ? 'Enabled' : 'Disabled'}
													</Badge>
												</button>
											{/each}
										</div>
									{:else}
										<div class="text-muted-foreground text-center text-sm py-4 italic">
											No hooks configured
										</div>
									{/if}
								</div>
							</div>

							{#if template.allowedOptions && template.allowedOptions.length > 0}
								<div class="border rounded-md overflow-hidden">
									<div class="bg-muted flex items-center gap-2 px-4 py-2">
										<span class="icon-[mdi--shield-check] text-primary h-5 w-5"></span>
										<span class="font-semibold">Allowed Options</span>
									</div>
									<div class="p-4 flex flex-wrap gap-2">
										{#each template.allowedOptions as opt}
											<Badge variant="outline" class="bg-muted text-muted-foreground">{opt}</Badge>
										{/each}
									</div>
								</div>
							{/if}

							{#if template.additionalOptions}
								<div class="border rounded-md overflow-hidden">
									<div class="bg-muted flex items-center gap-2 px-4 py-2">
										<span class="icon-[mdi--wrench] text-primary h-5 w-5"></span>
										<span class="font-semibold">Additional Options</span>
									</div>
									<div class="bg-zinc-950 p-4 overflow-x-auto text-green-400 font-mono text-xs">
										<pre>{template.additionalOptions}</pre>
									</div>
								</div>
							{/if}
						</Tabs.Content>
					</ScrollArea>
				</Tabs.Root>
			{:else}
				<div class="text-muted-foreground flex items-center justify-center py-16 text-sm italic">
					No template details available.
				</div>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={hookModalOpen}>
	<Dialog.Content class="max-w-2xl">
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2">
				<span class="icon-[mdi--script-text-outline] h-5 w-5"></span>
				<span
					>{selectedHook
						? `${formatHookPhase(selectedHook.phase)} Hook Details`
						: 'Hook Details'}</span
				>
				{#if selectedHook}
					<Badge
						class="ml-2 {selectedHook.enabled
							? 'bg-green-600/80'
							: 'bg-gray-600/80'} text-white font-semibold"
					>
						{selectedHook.enabled ? 'Enabled' : 'Disabled'}
					</Badge>
				{/if}
			</Dialog.Title>
		</Dialog.Header>
		<div class="mt-4">
			{#if selectedHook?.script}
				<Textarea
					class="font-mono text-xs h-[40vh] w-full resize-none p-4"
					value={selectedHook.script}
					readonly
					spellcheck={false}
				/>
			{:else}
				<div
					class="flex h-[20vh] items-center justify-center rounded-md border border-dashed text-muted-foreground text-sm"
				>
					No script content found for this hook
				</div>
			{/if}
		</div>
		<Dialog.Footer>
			<Button variant="secondary" onclick={() => (hookModalOpen = false)}>Close</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
