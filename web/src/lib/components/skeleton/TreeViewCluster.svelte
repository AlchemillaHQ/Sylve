<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { convertJailToTemplate, deleteJailTemplate, jailAction } from '$lib/api/jail/jail';
	import CreateJailFromTemplate from '$lib/components/custom/Jail/Template/Create.svelte';
	import { actionVm } from '$lib/api/vm/vm';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { slide } from 'svelte/transition';
	import { toast } from 'svelte-sonner';
	import SidebarElement from './TreeViewCluster.svelte';

	interface SidebarProps {
		id: string;
		label: string;
		icon: string;
		href?: string;
		state?: 'active' | 'inactive';
		resourceId?: number;
		resourceType?: 'vm' | 'jail' | 'jail-template';
		nodeHostname?: string;
		nextGuestId?: number;
		children?: SidebarProps[];
	}

	interface Props {
		item: SidebarProps;
		openIds: Set<string>;
		onToggleId: (id: string) => void;
		nextGuestId?: number;
	}

	let { item, openIds, onToggleId, nextGuestId = 100 }: Props = $props();
	let isOpen = $derived(openIds.has(item.id));

	const handleLabelClick = (e: MouseEvent) => {
		e.preventDefault();
		if (item.href) {
			goto(item.href, { replaceState: false, noScroll: false });
		}
	};

	const handleIconClick = (e: MouseEvent) => {
		e.preventDefault();
		e.stopPropagation();
		if (item.children && item.children.length > 0) {
			onToggleId(item.id);
		}
	};

	const sidebarActive = 'rounded-md bg-muted dark:bg-muted font-inter font-medium';

	function isItemActive(menuItem: SidebarProps, currentUrl: string): boolean {
		if (menuItem.href && currentUrl.startsWith(menuItem.href)) {
			return true;
		}
		if (menuItem.children) {
			return menuItem.children.some((child) => isItemActive(child, currentUrl));
		}
		return false;
	}

	let activeUrl = $derived(page.url.pathname);
	let isActive = $derived(isItemActive(item, activeUrl));
	let hasContextMenu = $derived(
		item.resourceType === 'vm' ||
			item.resourceType === 'jail' ||
			item.resourceType === 'jail-template'
	);
	let lastActiveUrl = $derived.by(() => {
		const segments = activeUrl.split('/');
		return segments[segments.length - 1];
	});
	let createFromTemplateOpen = $state(false);

	const handleActionClick = async (action: 'start' | 'reboot' | 'shutdown' | 'stop') => {
		if (item.resourceId === undefined || item.resourceType === undefined) {
			return;
		}

		if (item.resourceType === 'jail') {
			if (action !== 'start' && action !== 'stop') {
				return;
			}
			await jailAction(item.resourceId, action, item.nodeHostname);
		} else {
			await actionVm(item.resourceId, action, item.nodeHostname);
		}

		reload.leftPanel = true;

		console.log(`[cluster-tree] ${action} ${item.resourceType}`, {
			id: item.resourceId,
			hostname: item.nodeHostname
		});
	};

	const handleConvertToTemplate = async () => {
		if (!item.resourceId) return;
		const result = await convertJailToTemplate(item.resourceId, item.nodeHostname);
		if (result.error) {
			toast.error('Failed to convert jail to template', { position: 'bottom-center' });
			return;
		}
		reload.leftPanel = true;
		toast.success('Template conversion queued', { position: 'bottom-center' });
	};

	const handleDeleteTemplate = async () => {
		if (!item.resourceId) return;
		if (!confirm(`Delete template "${item.label}"?`)) return;
		const result = await deleteJailTemplate(item.resourceId, item.nodeHostname);
		if (result.error) {
			toast.error('Failed to delete template', { position: 'bottom-center' });
			return;
		}
		reload.leftPanel = true;
		toast.success('Template deleted', { position: 'bottom-center' });
	};
</script>

<li class="w-full">
	{#if hasContextMenu}
		<ContextMenu.Root>
			<ContextMenu.Trigger
				role="button"
				tabindex={0}
				class={`my-0.5 flex w-full cursor-pointer items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}`}
				onclick={handleLabelClick}
				onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleLabelClick(e as any) : null)}
			>
				<div class="flex items-center space-x-1 text-sm">
					{#if item.icon === 'material-symbols--monitor-outline' || item.icon === 'hugeicons--prison'}
						<div class="flex items-center space-x-1 text-sm">
							<div class="relative">
								<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
								{#if item.state && item.state === 'active'}
									<div
										class="absolute -right-1 bottom-0.5 flex h-2 w-2 items-center justify-center rounded-full bg-green-500"
									>
										<span class="icon-[mdi--play] h-2 w-2 text-white"></span>
									</div>
								{/if}
							</div>
						</div>
					{:else}
						<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
					{/if}
					<p class="font-inter cursor-pointer whitespace-nowrap">
						{item.label}
					</p>
				</div>

				{#if item.children && item.children.length > 0}
					<span
						role="button"
						tabindex="0"
						class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5 cursor-pointer`}
						onclick={handleIconClick}
						onkeydown={(e) =>
							e.key === 'Enter' || e.key === ' ' ? handleIconClick(e as any) : null}
					></span>
				{/if}
			</ContextMenu.Trigger>
			<ContextMenu.Content>
				{#if item.resourceType === 'jail'}
					{#if item.state === 'active'}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('stop')}>
							<span class="icon-[mdi--stop] h-4 w-4"></span>
							Stop
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('start')}>
							<span class="icon-[mdi--play] h-4 w-4"></span>
							Start
						</ContextMenu.Item>
					{/if}
					<ContextMenu.Separator />
					<ContextMenu.Item class="gap-2" onclick={() => void handleConvertToTemplate()}>
						<span class="icon-[mdi--content-copy] h-4 w-4"></span>
						Convert to Template
					</ContextMenu.Item>
				{:else if item.resourceType === 'vm'}
					{#if item.state === 'active'}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('reboot')}>
							<span class="icon-[mdi--restart] h-4 w-4"></span>
							Reboot
						</ContextMenu.Item>
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('shutdown')}>
							<span class="icon-[mdi--power] h-4 w-4"></span>
							Shutdown
						</ContextMenu.Item>
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('stop')}>
							<span class="icon-[mdi--stop] h-4 w-4"></span>
							Stop
						</ContextMenu.Item>
					{:else}
						<ContextMenu.Item class="gap-2" onclick={() => void handleActionClick('start')}>
							<span class="icon-[mdi--play] h-4 w-4"></span>
							Start
						</ContextMenu.Item>
					{/if}
				{:else if item.resourceType === 'jail-template'}
					<ContextMenu.Item class="gap-2" onclick={() => (createFromTemplateOpen = true)}>
						<span class="icon-[mdi--plus-box-outline] h-4 w-4"></span>
						Create Jail
					</ContextMenu.Item>
					<ContextMenu.Item
						class="gap-2 text-destructive"
						onclick={() => void handleDeleteTemplate()}
					>
						<span class="icon-[mdi--delete-outline] h-4 w-4"></span>
						Delete Template
					</ContextMenu.Item>
				{/if}
			</ContextMenu.Content>
		</ContextMenu.Root>
	{:else}
		<div
			role="button"
			tabindex="0"
			class={`my-0.5 flex w-full cursor-pointer items-center justify-between px-1.5 py-0.5 ${isActive ? sidebarActive : 'hover:bg-muted dark:hover:bg-muted rounded-md'}${lastActiveUrl === item.label ? 'text-primary!' : ' '}`}
			onclick={handleLabelClick}
			onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleLabelClick(e as any) : null)}
		>
			<div class="flex items-center space-x-1 text-sm">
				{#if item.icon === 'material-symbols--monitor-outline' || item.icon === 'hugeicons--prison'}
					<div class="flex items-center space-x-1 text-sm">
						<div class="relative">
							<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
							{#if item.state && item.state === 'active'}
								<div
									class="absolute -right-1 bottom-0.5 flex h-2 w-2 items-center justify-center rounded-full bg-green-500"
								>
									<span class="icon-[mdi--play] h-2 w-2 text-white"></span>
								</div>
							{/if}
						</div>
					</div>
				{:else}
					<span class={`icon-[${item.icon}]`} style="width: 18px; height: 18px;"></span>
				{/if}
				<p class="font-inter cursor-pointer whitespace-nowrap">
					{item.label}
				</p>
			</div>

			{#if item.children && item.children.length > 0}
				<span
					role="button"
					tabindex="0"
					class={`icon-[teenyicons--${isOpen ? 'down-solid' : 'right-solid'}] h-3.5 w-3.5 cursor-pointer`}
					onclick={handleIconClick}
					onkeydown={(e) => (e.key === 'Enter' || e.key === ' ' ? handleIconClick(e as any) : null)}
				></span>
			{/if}
		</div>
	{/if}
</li>

{#if isOpen && item.children}
	<ul class="pl-5" transition:slide={{ duration: 200, easing: (t) => t }} style="overflow: hidden;">
		{#each item.children as child (child.id)}
			<SidebarElement
				item={child}
				{openIds}
				{onToggleId}
				nextGuestId={item.nextGuestId ?? nextGuestId}
			/>
		{/each}
	</ul>
{/if}

{#if item.resourceType === 'jail-template' && item.resourceId}
	<CreateJailFromTemplate
		bind:open={createFromTemplateOpen}
		templateId={item.resourceId}
		templateLabel={item.label}
		hostname={item.nodeHostname}
		{nextGuestId}
	/>
{/if}
