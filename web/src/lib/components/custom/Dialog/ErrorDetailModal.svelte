<script lang="ts">
	import DetailBlock from '$lib/components/custom/Dialog/DetailBlock.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import {
		clearErrorDetail,
		closeErrorDetail,
		errorDetailState
	} from '$lib/stores/error-details.svelte';

	function statusClass(status: string): string {
		switch (status.toLowerCase()) {
			case 'warning':
				return 'bg-yellow-500/10 text-yellow-700 dark:text-yellow-400';
			case 'success':
			case 'ok':
				return 'bg-green-500/10 text-green-700 dark:text-green-400';
			default:
				return 'bg-destructive/10 text-destructive';
		}
	}

	function occurredAt(value: string): string {
		const date = new Date(value);
		return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
	}
</script>

<Dialog.Root
	bind:open={errorDetailState.open}
	onOpenChangeComplete={(open) => {
		if (!open) clearErrorDetail();
	}}
>
	<Dialog.Content
		class="z-[70] flex max-h-[88vh] w-[calc(100%-1.5rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl"
		overlayClass="z-[70] backdrop-blur-sm"
		showCloseButton={false}
		onInteractOutside={closeErrorDetail}
	>
		<div class="flex items-start justify-between gap-4 border-b px-5 py-4">
			<div class="min-w-0">
				<div class="flex items-center gap-2.5">
					<span class="icon-[mdi--alert-circle-outline] text-destructive h-5 w-5 shrink-0"></span>
					<Dialog.Title class="truncate text-base font-semibold">Error Details</Dialog.Title>
				</div>
				<Dialog.Description class="mt-1.5 line-clamp-2 pl-7">
					{errorDetailState.data?.title || 'The request could not be completed.'}
				</Dialog.Description>
			</div>
			<Dialog.Close
				aria-label="Close error details"
				class="text-muted-foreground focus-visible:ring-ring inline-flex h-8 w-8 shrink-0 items-center justify-center opacity-70 transition-opacity hover:opacity-100 focus-visible:ring-2 focus-visible:outline-none"
				onclick={closeErrorDetail}
			>
				<span class="icon-[mdi--close] h-5 w-5"></span>
			</Dialog.Close>
		</div>

		{#if errorDetailState.data}
			<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
				<div class="grid grid-cols-2 gap-3 min-[480px]:grid-cols-[1fr_1fr_1fr_2fr]">
					<div class="rounded-lg border px-3 py-2.5">
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Status
						</div>
						<span
							class="mt-1 inline-flex rounded-md px-2 py-0.5 text-xs font-semibold capitalize {statusClass(errorDetailState.data.status)}"
						>
							{errorDetailState.data.status}
						</span>
					</div>

					{#if errorDetailState.data.httpStatus !== undefined}
						<div class="rounded-lg border px-3 py-2.5">
							<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
								HTTP Status
							</div>
							<div class="mt-1 font-mono text-sm font-semibold">
								{errorDetailState.data.httpStatus}
							</div>
						</div>
					{/if}

					{#if errorDetailState.data.method}
						<div class="col-span-2 rounded-lg border px-3 py-2.5 min-[480px]:col-span-1">
							<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
								Method
							</div>
							<div class="mt-1 font-mono text-sm font-semibold">
								{errorDetailState.data.method}
							</div>
						</div>
					{/if}

					<div class="col-span-2 rounded-lg border px-3 py-2.5 min-[480px]:col-span-1">
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Occurred
						</div>
						<div class="mt-1 text-sm font-medium whitespace-nowrap">
							{occurredAt(errorDetailState.data.occurredAt)}
						</div>
					</div>
				</div>

				{#if errorDetailState.data.path || errorDetailState.data.node}
					<div class="grid grid-cols-2 gap-3">
						{#if errorDetailState.data.path}
							<div
								class="rounded-lg border px-3 py-2.5 {errorDetailState.data.node
									? ''
									: 'col-span-2'}"
							>
								<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
									API Request
								</div>
								<div class="mt-1 flex flex-wrap items-center gap-2 font-mono text-xs">
									{#if errorDetailState.data.method}
										<span class="bg-muted rounded px-1.5 py-0.5 font-semibold">
											{errorDetailState.data.method}
										</span>
									{/if}
									<span class="break-all">{errorDetailState.data.path}</span>
								</div>
							</div>
						{/if}

						{#if errorDetailState.data.node}
							<div
								class="rounded-lg border px-3 py-2.5 {errorDetailState.data.path
									? ''
									: 'col-span-2'}"
							>
								<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
									Node
								</div>
								<div class="mt-1 font-mono text-sm">{errorDetailState.data.node}</div>
							</div>
						{/if}
					</div>
				{/if}

				<div class="grid grid-cols-2 gap-3">
					<div
						class="rounded-lg border px-3 py-2.5 {errorDetailState.data.errors.length > 0
							? ''
							: 'col-span-2'}"
					>
						<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
							Message
						</div>
						<div class="mt-1 text-sm leading-relaxed">{errorDetailState.data.message}</div>
					</div>

					{#if errorDetailState.data.errors.length > 0}
						<div class="rounded-lg border px-3 py-2.5">
							<div class="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
								{errorDetailState.data.errors.length === 1 ? 'Error' : 'Errors'}
							</div>
							<ul class="mt-1.5 space-y-1 text-sm">
								{#each errorDetailState.data.errors as error, index (`${index}-${error}`)}
									<li class="font-mono break-all">{error}</li>
								{/each}
							</ul>
						</div>
					{/if}
				</div>

				<DetailBlock
					label="Raw Response"
					value={errorDetailState.data.rawResponse}
					copyLabel="Error details"
				/>
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
