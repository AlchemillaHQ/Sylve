<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { storage } from '$lib';
	import {
		dismissNotification,
		getNotificationsCount,
		listNotifications
	} from '$lib/api/notifications';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { alerts } from '$lib/stores/interface.svelte';
	import type { Notification } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { dateToAgo } from '$lib/utils/time';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	const severityStyle: Record<Notification['severity'], string> = {
		critical: 'icon-[lucide--octagon-alert] text-red-600 dark:text-red-500',
		error: 'icon-[lucide--circle-alert] text-red-600 dark:text-red-500',
		warning: 'icon-[lucide--triangle-alert] text-amber-500',
		info: 'icon-[lucide--info] text-zen-accent'
	};

	let dismissing = $state<number | null>(null);

	const count = resource(
		() => 'zen-alerts-count',
		async () => {
			const result = await getNotificationsCount();
			alerts.count = result.active;
			return result.active;
		},
		{ initialValue: 0 }
	);

	const list = resource(
		() => alerts.open,
		async (open) => (open ? (await listNotifications('active', 100, 0)).items : []),
		{ initialValue: [] as Notification[] }
	);

	function refresh() {
		count.refetch();
		if (alerts.open) list.refetch();
	}

	useInterval(10000, { callback: () => storage.visible && refresh() });

	watch(
		() => reload.notifications,
		(pending) => {
			if (!pending) return;
			refresh();
			reload.notifications = false;
		}
	);

	async function dismiss(item: Notification) {
		dismissing = item.id;
		const response = await dismissNotification(item.id);
		dismissing = null;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to dismiss notification', { duration: 4000, position: 'bottom-center' });
			return;
		}

		refresh();
	}
</script>

<Button variant="ghost" size="icon" class="relative" onclick={() => (alerts.open = true)}>
	<span class="icon-[lucide--bell] size-5"></span>
	<span class="sr-only">Alerts</span>
	{#if alerts.count > 0}
		<span
			class="bg-destructive absolute top-1 right-1 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-medium text-white"
		>
			{alerts.count > 99 ? '99+' : alerts.count}
		</span>
	{/if}
</Button>

<Sheet.Root bind:open={alerts.open}>
	<Sheet.Content side="right" class="w-full gap-0 sm:max-w-md" showCloseButton={false}>
		<Sheet.Header class="gap-1 border-b py-3">
			<div class="flex items-center justify-between gap-3">
				<Sheet.Title>Alerts</Sheet.Title>
				<Sheet.Close>
					{#snippet child({ props })}
						<Button variant="ghost" size="icon" class="text-muted-foreground" {...props}>
							<span class="icon-[lucide--x] size-5"></span>
							<span class="sr-only">Close alerts</span>
						</Button>
					{/snippet}
				</Sheet.Close>
			</div>
			<Sheet.Description>
				{#if alerts.count === 1}
					1 active alert
				{:else}
					{alerts.count} active alerts
				{/if}
			</Sheet.Description>
		</Sheet.Header>

		<div class="min-h-0 flex-1 overflow-y-auto">
			{#if list.current.length === 0 && !list.loading}
				<div class="text-muted-foreground flex flex-col items-center gap-2 px-6 py-16 text-center">
					<span class="icon-[lucide--circle-check] size-8"></span>
					<p class="text-sm">Nothing needs your attention.</p>
				</div>
			{:else}
				<ul class="divide-y">
					{#each list.current as item (item.id)}
						<li class="flex gap-3 p-4">
							<span class="{severityStyle[item.severity]} mt-0.5 size-5 shrink-0"></span>
							<div class="min-w-0 flex-1">
								<p class="text-sm font-medium break-words">{item.title}</p>
								{#if item.body}
									<p class="text-muted-foreground mt-1 text-sm break-words whitespace-pre-line">
										{item.body}
									</p>
								{/if}
								<div class="mt-2 flex items-center gap-2">
									<span class="text-muted-foreground text-xs capitalize">{item.severity}</span>
									<span class="text-muted-foreground text-xs">
										· {dateToAgo(item.lastOccurredAt)}
										{#if item.occurrenceCount > 1}· ×{item.occurrenceCount}{/if}
									</span>
									<Button
										variant="outline"
										size="sm"
										class="ml-auto h-7"
										disabled={dismissing !== null}
										onclick={() => dismiss(item)}
									>
										Dismiss
									</Button>
								</div>
							</div>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	</Sheet.Content>
</Sheet.Root>
