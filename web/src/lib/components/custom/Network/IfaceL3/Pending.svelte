<script lang="ts">
	import {
		confirmHostInterfaceL3,
		getHostInterfaceL3Pending,
		revertHostInterfaceL3
	} from '$lib/api/network/ifaceL3';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { HostInterfaceL3PendingEntry } from '$lib/types/network/ifaceL3';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		entry: HostInterfaceL3PendingEntry | null;
		remaining?: number;
		onDone: () => void | Promise<void>;
	}

	let { open = $bindable(), entry, remaining = 1, onDone }: Props = $props();

	let busy = $state(false);
	let now = $state(Date.now());
	let expired = $state(false);
	let lastExpiryCheck = $state(0);

	const expiryPollInterval = 1000;

	$effect(() => {
		if (!open) {
			return;
		}
		const timer = setInterval(() => {
			now = Date.now();
		}, 250);
		return () => clearInterval(timer);
	});

	$effect(() => {
		// Reset the countdown state whenever a new operation is shown.
		const currentID = entry?.id ?? '';
		if (currentID === '') {
			return;
		}
		now = Date.now();
		expired = false;
		lastExpiryCheck = 0;
	});

	let remainingSeconds = $derived.by(() => {
		if (!entry) {
			return 0;
		}
		const deadline = Date.parse(entry.deadline);
		if (Number.isNaN(deadline)) {
			return 0;
		}
		return Math.max(0, Math.ceil((deadline - now) / 1000));
	});

	let actionLabel = $derived(entry?.kind === 'delete' ? 'removal' : 'change');

	async function confirm() {
		if (!entry) {
			return;
		}
		busy = true;
		try {
			const response = await confirmHostInterfaceL3(entry.id);
			if (isAPIResponse(response) && response.status !== 'success') {
				handleAPIError(response);
				return;
			}
			open = false;
			toast.success(`${entry.interface} confirmed`, { position: 'bottom-center' });
			await onDone();
		} finally {
			busy = false;
		}
	}

	async function revert() {
		if (!entry) {
			return;
		}
		busy = true;
		try {
			const response = await revertHostInterfaceL3(entry.id);
			if (isAPIResponse(response) && response.status !== 'success') {
				handleAPIError(response);
				return;
			}
			open = false;
			toast.success(`${entry.interface} reverted`, { position: 'bottom-center' });
			await onDone();
		} finally {
			busy = false;
		}
	}

	async function checkExpired() {
		if (!entry || busy || expired) {
			return;
		}
		const timestamp = Date.now();
		if (timestamp - lastExpiryCheck < expiryPollInterval) {
			return;
		}
		lastExpiryCheck = timestamp;

		const pending = await getHostInterfaceL3Pending();
		if (isAPIResponse(pending)) {
			return;
		}
		if (!pending.some((operation) => operation.id === entry.id)) {
			expired = true;
			open = false;
			toast.info(`${entry.interface}: the Host IP change was rolled back`, {
				position: 'bottom-center'
			});
			await onDone();
		}
	}

	$effect(() => {
		if (open && entry && remainingSeconds === 0) {
			void checkExpired();
		}
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[96%] overflow-hidden p-5 lg:max-w-md"
		showCloseButton={false}
		aria-busy={busy}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--timer-alert-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title={`Confirm Host IP ${actionLabel}`}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="space-y-3 py-2 text-sm">
			<p>
				The {actionLabel} on <span class="font-medium">{entry?.interface ?? ''}</span> is applied but
				not confirmed yet.
			</p>
			<p class="text-muted-foreground">
				Confirm within {remainingSeconds}s or it is rolled back automatically. If the interface
				stops responding, the timeout restores the previous state.
			</p>
			{#if remaining > 1}
				<p class="text-xs text-muted-foreground">
					{remaining - 1} more Host IP change{remaining - 1 === 1 ? '' : 's'} waiting for confirmation
					after this one.
				</p>
			{/if}
		</div>

		<Dialog.Footer>
			<div class="flex w-full items-center justify-between gap-2">
				<Button size="sm" variant="outline" onclick={revert} disabled={busy}>Revert now</Button>
				<Button size="sm" onclick={confirm} disabled={busy}>
					{#if busy}
						<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					{/if}
					Confirm
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
