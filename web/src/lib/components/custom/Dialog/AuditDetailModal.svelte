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
		class="z-[70] flex max-h-[90vh] w-[calc(100%-1.5rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl"
		overlayClass="z-[70] backdrop-blur-sm"
		showCloseButton={false}
		onInteractOutside={() => (open = false)}
	>
		<div class="flex items-start justify-between gap-4 border-b px-5 py-4">
			<div class="min-w-0">
				<div class="flex items-center gap-2.5">
					<span class="icon-[mdi--clipboard-text-clock-outline] text-primary h-5 w-5 shrink-0"></span>
					<Dialog.Title class="truncate text-base font-semibold">Audit Record Details</Dialog.Title>
				</div>
				<Dialog.Description class="mt-1.5 truncate pl-7">
					{record?.resolvedAction || 'Request and response details'}
				</Dialog.Description>
			</div>
			<Dialog.Close
				aria-label="Close audit record details"
				class="text-muted-foreground focus-visible:ring-ring inline-flex h-8 w-8 shrink-0 items-center justify-center opacity-70 transition-opacity hover:opacity-100 focus-visible:ring-2 focus-visible:outline-none"
				onclick={() => (open = false)}
			>
				<span class="icon-[mdi--close] h-5 w-5"></span>
			</Dialog.Close>
		</div>

		{#if record}
			<div class="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-4">
				<div class="grid grid-cols-2 gap-3 md:grid-cols-4">
					<div class="rounded-lg border px-3 py-2.5">
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Record ID
						</div>
						<div class="mt-1 font-mono text-sm font-semibold">#{record.id}</div>
					</div>
					<div class="rounded-lg border px-3 py-2.5">
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Status
						</div>
						<span
							class="mt-1 inline-flex rounded-md px-2 py-0.5 text-xs font-semibold {statusClass(record.status)}"
						>
							{statusLabel(record.status)}
						</span>
					</div>
					<div class="rounded-lg border px-3 py-2.5">
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Node
						</div>
						<div class="mt-1 truncate font-mono text-sm font-semibold">{record.node}</div>
					</div>
					<div class="rounded-lg border px-3 py-2.5">
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Duration
						</div>
						<div class="mt-1 font-mono text-sm font-semibold">
							{duration(record.started, record.ended)}
						</div>
					</div>
				</div>

				<div class="grid gap-3 md:grid-cols-2">
					<div class="rounded-lg border px-3 py-2.5">
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Actor
						</div>
						<div class="mt-1 text-sm font-medium">{record.user}@{record.authType || 'cluster'}</div>
					</div>
					<div class="rounded-lg border px-3 py-2.5">
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Timeline
						</div>
						<div class="mt-1 text-sm">
							{convertDbTime(record.started)}
							<span class="text-muted-foreground px-1.5">to</span>
							{convertDbTime(record.ended)}
						</div>
					</div>
				</div>

				<div class="grid grid-cols-2 gap-4">
					<section
						class="min-w-0 space-y-3 rounded-xl border p-4 {initialSection === 'request'
							? 'border-primary/60 ring-primary/15 ring-2'
							: ''}"
					>
						<div class="flex items-center gap-2">
							<span class="icon-[mdi--upload-network-outline] text-primary h-4 w-4"></span>
							<h3 class="text-sm font-semibold">Request</h3>
						</div>
						<div class="rounded-lg border px-3 py-2.5">
							<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
								Endpoint
							</div>
							<div class="mt-1 flex flex-wrap items-center gap-2 font-mono text-xs">
								<span class="bg-muted rounded px-1.5 py-0.5 font-semibold">
									{record.action.method}
								</span>
								<span class="break-all">{record.action.path}</span>
							</div>
						</div>
						{#if record.action.query}
							<DetailBlock label="Query" value={record.action.query} copyLabel="Query" />
						{/if}
						{#if hasRequestBody}
							<DetailBlock
								label="Request Payload"
								value={record.action.body}
								copyLabel="Request payload"
							/>
						{:else}
							<p class="text-muted-foreground text-sm">No request payload was recorded.</p>
						{/if}
					</section>

					<section
						class="min-w-0 space-y-3 rounded-xl border p-4 {initialSection === 'response'
							? 'border-primary/60 ring-primary/15 ring-2'
							: ''}"
					>
						<div class="flex items-center gap-2">
							<span class="icon-[mdi--download-network-outline] text-primary h-4 w-4"></span>
							<h3 class="text-sm font-semibold">Response</h3>
						</div>
						{#if record.error}
							<DetailBlock label="Recorded Error" value={record.error} copyLabel="Error" />
						{/if}
						{#if hasResponse}
							<DetailBlock
								label="Response Payload"
								value={record.action.response}
								copyLabel="Response"
							/>
						{:else if !record.error}
							<p class="text-muted-foreground text-sm">No response or error was recorded.</p>
						{/if}
					</section>
				</div>

				<DetailBlock label="Raw Audit Record" value={record} copyLabel="Audit record" />
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
