<script lang="ts">
	import {
		dismissNotification,
		getNotificationsCount,
		listNotifications
	} from '$lib/api/notifications';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { Notification } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	let open = $state(false);
	let showDismissed = $state(false);
	let dismissing = $state<number | null>(null);

	const notificationCount = resource(
		() => 'notification-bell-count',
		async () => {
			return await getNotificationsCount();
		},
		{ initialValue: { active: 0 } }
	);

	const notifications = resource(
		() => `notification-bell-list-${showDismissed ? 'all' : 'active'}`,
		async () => {
			return await listNotifications(showDismissed ? 'all' : 'active', 100, 0);
		},
		{ initialValue: { items: [] as Notification[], total: 0 } }
	);

	let count = $derived(notificationCount.current.active ?? 0);
	let items = $derived(notifications.current.items ?? []);

	watch(
		() => open,
		(value) => {
			if (value) {
				notifications.refetch();
			}
		}
	);

	watch(
		() => showDismissed,
		() => {
			if (open) {
				notifications.refetch();
			}
		}
	);

	watch(
		() => reload.notifications,
		(value) => {
			if (value) {
				notificationCount.refetch();
				if (open) {
					notifications.refetch();
				}
				reload.notifications = false;
			}
		}
	);

	useInterval(10000, {
		callback: () => {
			notificationCount.refetch();
			if (open) {
				notifications.refetch();
			}
		}
	});

	async function dismiss(item: Notification) {
		if (!item?.id || dismissing !== null) {
			return;
		}

		dismissing = item.id;
		const response = await dismissNotification(item.id);
		dismissing = null;

		if (isAPIResponse(response) && response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to dismiss notification', {
				duration: 4000,
				position: 'bottom-center'
			});
			return;
		}

		await Promise.all([notificationCount.refetch(), notifications.refetch()]);
		toast.success('Notification dismissed', {
			duration: 3000,
			position: 'bottom-center'
		});
	}

	function severityClass(severity: string) {
		switch (severity) {
			case 'critical':
				return 'text-red-600';
			case 'error':
				return 'text-red-500';
			case 'warning':
				return 'text-yellow-600';
			default:
				return 'text-blue-600';
		}
	}
</script>

<Button
	size="sm"
	class="relative h-6"
	variant="outline"
	onclick={() => (open = true)}
	title="Notifications"
>
	<div class="flex items-center gap-1.5">
		<span class="icon-[mdi--bell-outline] h-4 w-4"></span>
		<span>Notifications</span>
	</div>
	{#if count > 0}
		<span
			class="bg-destructive text-destructive-foreground absolute -right-2 -top-2 inline-flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px]"
		>
			{count > 99 ? '99+' : count}
		</span>
	{/if}
</Button>

<Dialog.Root bind:open>
	<Dialog.Content class="w-[95%] max-w-5xl p-5" showCloseButton={false}>
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between gap-4">
				<SpanWithIcon
					icon="icon-[mdi--bell-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Notifications"
				/>
				<CustomCheckbox label="Show Dismissed" bind:checked={showDismissed} />
			</Dialog.Title>
		</Dialog.Header>

		<div class="max-h-[55vh] overflow-auto rounded-md border">
			<Table.Root class="w-full">
				<Table.Header class="bg-muted/50 sticky top-0">
					<Table.Row>
						<Table.Head class="w-28">Severity</Table.Head>
						<Table.Head>Notification</Table.Head>
						<Table.Head class="w-32">Source</Table.Head>
						<Table.Head class="w-48">Last Seen</Table.Head>
						<Table.Head class="w-20 text-right">Count</Table.Head>
						<Table.Head class="w-28 text-right">Action</Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#if items.length === 0}
						<Table.Row>
							<Table.Cell colspan={6} class="text-muted-foreground h-24 text-center">
								No notifications found.
							</Table.Cell>
						</Table.Row>
					{:else}
						{#each items as item (item.id)}
							<Table.Row>
								<Table.Cell>
									<span class={severityClass(item.severity)}>{item.severity}</span>
								</Table.Cell>
								<Table.Cell>
									<div class="space-y-0.5">
										<p class="font-medium">{item.title}</p>
										{#if item.body}
											<p class="text-muted-foreground text-xs">{item.body}</p>
										{/if}
									</div>
								</Table.Cell>
								<Table.Cell class="text-xs">{item.source || '-'}</Table.Cell>
								<Table.Cell class="text-xs">{convertDbTime(item.lastOccurredAt)}</Table.Cell>
								<Table.Cell class="text-right">{item.occurrenceCount}</Table.Cell>
								<Table.Cell class="text-right">
									{#if !item.dismissedAt}
										<Button
											size="sm"
											variant="outline"
											class="h-6"
											onclick={() => dismiss(item)}
											disabled={dismissing !== null}
										>
											Dismiss
										</Button>
									{:else}
										<span class="text-muted-foreground text-xs">Dismissed</span>
									{/if}
								</Table.Cell>
							</Table.Row>
						{/each}
					{/if}
				</Table.Body>
			</Table.Root>
		</div>

		<Dialog.Footer>
			<Button variant="outline" class="h-7" onclick={() => notificationCount.refetch()}
				>Refresh Count</Button
			>
			<Button variant="outline" class="h-7" onclick={() => notifications.refetch()}
				>Refresh List</Button
			>
			<Button variant="outline" class="h-7" onclick={() => (open = false)}>Close</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
