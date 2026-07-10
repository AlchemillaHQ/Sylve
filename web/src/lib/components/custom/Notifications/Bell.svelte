<script lang="ts">
	import {
		dismissNotification,
		getNotificationsCount,
		listNotifications
	} from '$lib/api/notifications';
	import ModalTable from '$lib/components/custom/ModalTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { Notification } from '$lib/types/notifications';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { storage } from '$lib';
	import { resource, useInterval, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent, ColumnDefinition } from 'tabulator-tables';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	type NotificationRow = Record<string, unknown> & {
		notification: Notification;
		dismissedAt: string | null | undefined;
		dismissing: boolean;
	};

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
			if (!storage.visible) return;
			notificationCount.refetch();
			if (open) {
				notifications.refetch();
			}
		}
	});

	async function refresh() {
		await Promise.all([notificationCount.refetch(), notifications.refetch()]);
	}

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

	function severityIcon(severity: string) {
		switch (severity) {
			case 'critical':
				return 'icon-[mdi--alert-octagon-outline]';
			case 'error':
				return 'icon-[mdi--alert-circle-outline]';
			case 'warning':
				return 'icon-[mdi--alert-outline]';
			default:
				return 'icon-[mdi--information-outline]';
		}
	}

	function severityColor(severity: string) {
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

	function capitalize(s: string) {
		return s.charAt(0).toUpperCase() + s.slice(1);
	}

	function severityFormatter(cell: CellComponent): HTMLElement {
		const severity = String(cell.getValue());
		const container = document.createElement('span');
		container.className = `flex items-center gap-1.5 ${severityColor(severity)}`;

		const icon = document.createElement('span');
		icon.className = `${severityIcon(severity)} h-4 w-4`;
		container.append(icon, capitalize(severity));

		return container;
	}

	function notificationFormatter(cell: CellComponent): HTMLElement {
		const notification = cell.getValue() as Notification;
		const container = document.createElement('div');
		container.className = 'space-y-0.5 whitespace-normal';

		const title = document.createElement('p');
		title.className = 'font-medium';
		title.textContent = notification.title;
		container.append(title);

		if (notification.body) {
			const body = document.createElement('p');
			body.className = 'text-muted-foreground text-xs';
			body.textContent = notification.body;
			container.append(body);
		}

		return container;
	}

	function dismissFormatter(cell: CellComponent): HTMLElement {
		const row = cell.getRow().getData() as NotificationRow;
		const icon = document.createElement('span');
		icon.className = 'h-4 w-4';

		if (row.dismissedAt) {
			icon.classList.add('icon-[lucide--bell-off]', 'text-muted-foreground/50');
			icon.title = 'Dismissed';
			return icon;
		}

		const button = document.createElement('button');
		button.type = 'button';
		button.disabled = row.dismissing;
		button.className =
			'inline-flex items-center justify-center opacity-50 transition-opacity hover:opacity-100 focus:outline-none disabled:pointer-events-none disabled:opacity-30';
		button.title = 'Dismiss';
		icon.classList.add('icon-[lucide--x]');
		button.append(icon);
		button.addEventListener('click', () => void dismiss(row.notification));

		return button;
	}

	const notificationColumns: ColumnDefinition[] = [
		{ title: 'Severity', field: 'severity', minWidth: 120, formatter: severityFormatter },
		{
			title: 'Notification',
			field: 'notification',
			minWidth: 220,
			widthGrow: 3,
			variableHeight: true,
			formatter: notificationFormatter
		},
		{ title: 'Source', field: 'source', minWidth: 120, cssClass: 'text-xs' },
		{
			title: 'Last Seen',
			field: 'lastOccurredAt',
			minWidth: 170,
			cssClass: 'text-xs',
			formatter: (cell: CellComponent) => convertDbTime(String(cell.getValue()))
		},
		{
			title: 'Count',
			field: 'occurrenceCount',
			minWidth: 75,
			hozAlign: 'right',
			headerHozAlign: 'right'
		},
		{
			title: 'Action',
			field: 'dismissedAt',
			minWidth: 90,
			hozAlign: 'center',
			headerHozAlign: 'center',
			formatter: dismissFormatter
		}
	];

	let tableRows = $derived(
		items.map((item) => ({
			id: item.id,
			severity: item.severity,
			notification: item,
			source: item.source || '-',
			lastOccurredAt: item.lastOccurredAt,
			occurrenceCount: item.occurrenceCount,
			dismissedAt: item.dismissedAt,
			dismissing: dismissing !== null
		}))
	);
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
	<Dialog.Content class="w-[95%] !max-w-[50vw] p-5" showCloseButton={false}>
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<SpanWithIcon
					icon="icon-[mdi--bell-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Notifications"
				/>
				<button
					onclick={() => (open = false)}
					class="opacity-50 transition-opacity hover:opacity-100 focus:outline-none disabled:pointer-events-none"
				>
					<span class="icon-[lucide--x] h-5 w-5"></span>
					<span class="sr-only">Close</span>
				</button>
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex h-[40vh] min-h-0 min-w-0 flex-col overflow-hidden">
			<ModalTable
				rows={tableRows}
				columns={notificationColumns}
				pageSize={10}
				placeholder="No notifications found."
			/>
		</div>

		<Dialog.Footer class="flex items-center !justify-between">
			<CustomCheckbox label="Show Dismissed" bind:checked={showDismissed} />
			<Button variant="outline" class="h-7" onclick={refresh}>Refresh</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<style>
	:global(
		html:not(.dark)
			.s-modal-table-container
			.tabulator-placeholder
			.tabulator-placeholder-contents
	) {
		color: var(--muted-foreground);
	}
</style>
