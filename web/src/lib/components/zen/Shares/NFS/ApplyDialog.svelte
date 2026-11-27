<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { applyNFS } from '$lib/api/nfs';
	import * as AlertDialog from '$lib/components/ui/alert-dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { isRequestCancellation } from '$lib/utils/http';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { applyPending, pendingDetail, requestError } from './nfs';

	let {
		hostname,
		onClose,
		onDone
	}: {
		hostname: string;
		onClose: () => void;
		onDone: () => void;
	} = $props();

	const lifetime = new AbortController();
	let applying = $state(false);

	onDestroy(() => lifetime.abort());

	async function apply() {
		if (applying) return;
		applying = true;
		try {
			const response = await applyNFS({ hostname, signal: lifetime.signal });
			if (lifetime.signal.aborted) return;
			if (response.status !== 'success') {
				toast.error(requestError(response, 'NFS could not be applied.'), {
					position: 'bottom-center'
				});
			} else if (applyPending(response)) {
				toast.error(pendingDetail(response, 'NFS couldn’t be re-applied'), {
					position: 'bottom-center'
				});
			} else {
				toast.success('NFS re-applied', { position: 'bottom-center' });
			}
			onDone();
			onClose();
		} catch (error) {
			if (lifetime.signal.aborted || isRequestCancellation(error)) return;
			toast.error('NFS could not be applied.', { position: 'bottom-center' });
		} finally {
			if (!lifetime.signal.aborted) applying = false;
		}
	}
</script>

<AlertDialog.Root
	open
	onOpenChange={(open) => {
		if (!open && !applying) onClose();
	}}
>
	<AlertDialog.Content escapeKeydownBehavior={applying ? 'ignore' : 'close'} aria-busy={applying}>
		<AlertDialog.Header class="text-left">
			<AlertDialog.Title>Re-apply NFS?</AlertDialog.Title>
			<AlertDialog.Description class="text-pretty">
				NFS restarts with the saved settings and shares. Connected clients are interrupted briefly;
				most reconnect on their own.
			</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={applying}>Cancel</AlertDialog.Cancel>
			<Button class="gap-2" disabled={applying} onclick={() => void apply()}>
				{#if applying}
					<span class="icon-[lucide--loader-circle] size-4 animate-spin" aria-hidden="true"></span>
				{/if}
				{applying ? 'Applying…' : 'Re-apply'}
			</Button>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
