<script lang="ts">
	import DetailBlock from '$lib/components/custom/Dialog/DetailBlock.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { AuditRecord } from '$lib/types/info/audit';
	import { convertDbTime } from '$lib/utils/time';

	type AuditDetailSection = 'request' | 'response';
	type ResolvedAuditRecord = AuditRecord & { resolvedAction: string };

	interface Props {
		open: boolean;
		record: ResolvedAuditRecord | null;
		initialSection?: AuditDetailSection;
	}

	let { open = $bindable(), record, initialSection = 'response' }: Props = $props();

	let hasRequestBody = $derived(
		record ? Object.prototype.hasOwnProperty.call(record.action, 'body') : false
	);
	let hasResponse = $derived(
		record ? Object.prototype.hasOwnProperty.call(record.action, 'response') : false
	);

	function statusClass(status: string): string {
		switch (status) {
			case 'success':
				return 'bg-green-500/10 text-green-700 dark:text-green-400';
			case 'pending':
			case 'started':
				return 'bg-blue-500/10 text-blue-700 dark:text-blue-400';
			case 'client_error':
				return 'bg-yellow-500/10 text-yellow-700 dark:text-yellow-400';
			case 'failed':
			case 'server_error':
				return 'bg-destructive/10 text-destructive';
			default:
				return 'bg-muted text-muted-foreground';
		}
	}

	function statusLabel(status: string): string {
		switch (status) {
			case 'success':
				return 'OK';
			case 'client_error':
				return 'Bad Request';
			case 'server_error':
				return 'Error';
			case 'pending':
				return 'In Progress';
			default:
				return status.charAt(0).toUpperCase() + status.slice(1);
		}
	}

	function duration(started: string, ended: string): string {
		const start = new Date(started).getTime();
		const end = new Date(ended).getTime();
		if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return 'Unknown';

		const milliseconds = end - start;
		if (milliseconds < 1000) return `${milliseconds} ms`;
		if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(2)} s`;

		const minutes = Math.floor(milliseconds / 60_000);
		const seconds = Math.floor((milliseconds % 60_000) / 1000);
		return `${minutes}m ${seconds}s`;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="z-[70] flex max-h-[80vh] w-[calc(100%-1.5rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl"
		overlayClass="z-[70] backdrop-blur-sm"
		showCloseButton={false}
		onInteractOutside={() => (open = false)}
	>
		<div class="flex items-start justify-between gap-3 border-b px-4 py-3">
			<div class="min-w-0">
				<Dialog.Title class="truncate text-sm font-semibold">
					{record?.resolvedAction || 'Audit Record Details'}
				</Dialog.Title>
				<Dialog.Description class="sr-only">Request and response details</Dialog.Description>
			</div>
			<Dialog.Close
				aria-label="Close audit record details"
				class="text-muted-foreground focus-visible:ring-ring inline-flex h-6 w-6 shrink-0 items-center justify-center opacity-70 transition-opacity hover:opacity-100 focus-visible:ring-2 focus-visible:outline-none"
				onclick={() => (open = false)}
			>
				<span class="icon-[mdi--close] h-4 w-4"></span>
			</Dialog.Close>
		</div>

		{#if record}
			<div class="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-3">
				<div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs">
					<span
						class="inline-flex items-center rounded px-1.5 py-0.5 font-semibold {statusClass(
							record.status
						)}"
					>
						{statusLabel(record.status)}
					</span>
					<span class="bg-muted rounded px-1.5 py-0.5 font-mono font-semibold">
						{record.action.method}
					</span>
					<span class="min-w-0 truncate font-mono">{record.action.path}</span>
					<span class="text-muted-foreground ml-auto whitespace-nowrap">
						#{record.id} · {record.user}@{record.authType || 'cluster'} · {record.node}
					</span>
				</div>
				<div class="text-muted-foreground text-xs">
					{convertDbTime(record.started)}
					<span class="px-1">→</span>
					{convertDbTime(record.ended)}
					<span class="px-1">·</span>
					{duration(record.started, record.ended)}
				</div>

				{#if record.action.query}
					<DetailBlock label="Query" value={record.action.query} copyLabel="Query" />
				{/if}
				{#if hasRequestBody}
					<DetailBlock
						label="Request Payload"
						value={record.action.body}
						copyLabel="Request payload"
						class={initialSection === 'request' ? 'border-primary/60 ring-primary/15 ring-1' : ''}
					/>
				{/if}
				{#if record.error}
					<DetailBlock label="Error" value={record.error} copyLabel="Error" />
				{/if}
				{#if hasResponse}
					<DetailBlock
						label="Response Payload"
						value={record.action.response}
						copyLabel="Response"
						class={initialSection === 'response' ? 'border-primary/60 ring-primary/15 ring-1' : ''}
					/>
				{/if}
				{#if !hasRequestBody && !hasResponse && !record.error}
					<p class="text-muted-foreground text-sm">No additional request/response details were recorded.</p>
				{/if}
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
