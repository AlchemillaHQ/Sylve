<script lang="ts">
	import { page } from '$app/state';
	import { getHostUpdates, startHostUpdate } from '$lib/api/system/updates';
	import Monitoring from '$lib/components/custom/System/Updates/Monitoring.svelte';
	import Operation from '$lib/components/custom/System/Updates/Operation.svelte';
	import {
		hostUpdateReason,
		type HostUpdateAction,
		type HostUpdateContext,
		type HostUpdateRequest,
		type HostUpdates
	} from '$lib/types/system/updates';
	import { isAPIResponse } from '$lib/utils/http';
	import { resource, useInterval, watch } from 'runed';
	import { setContext, untrack, type Snippet } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { LayoutData } from './$types';

	let { data, children }: { data: LayoutData; children: Snippet } = $props();
	const node = $derived(data.node);
	let requestError = $state('');
	const inventory = resource(
		[() => node],
		async (
			[node],
			_,
			{ signal, refetching, data: previous }
		): Promise<{ node: string; view: HostUpdates | null }> => {
			if (!refetching) {
				requestError = data.updatesError ? hostUpdateReason(data.updatesError.message) : '';
				return { node, view: data.updates };
			}
			const result = await getHostUpdates({ hostname: node, signal });
			signal.throwIfAborted();
			if (isAPIResponse(result)) {
				requestError = hostUpdateReason(result.message);
				return previous?.node === node ? previous : { node, view: null };
			}
			requestError = '';
			return { node, view: result };
		},
		{ initialValue: untrack(() => ({ node, view: data.updates })) }
	);
	const view = $derived(inventory.current.node === node ? inventory.current.view : data.updates);
	let submitting = $state(false);
	let operationOpen = $state(false);
	let monitoringOpen = $state(false);
	let jobId = $state(0);
	const enabled = $derived(!!view && !requestError && !submitting && !view.activeJob);
	const requestedId = $derived(
		page.url.searchParams.get('job') === 'active'
			? (view?.activeJob?.id ?? 0)
			: Number(page.url.searchParams.get('job'))
	);
	const requestedDialog = $derived(page.url.searchParams.get('dialog'));

	function openOperation(id: number) {
		jobId = id;
		operationOpen = true;
	}
	async function refresh() {
		await inventory.refetch();
	}
	async function run(action: HostUpdateAction, request: HostUpdateRequest = {}) {
		if (!enabled) return;
		const target = node;
		submitting = true;
		try {
			const result = await startHostUpdate(action, request, { hostname: target });
			if (target !== node) return;
			if (isAPIResponse(result)) {
				toast.error(hostUpdateReason(result.message), { position: 'bottom-center' });
				return;
			}
			openOperation(result.id);
			await refresh();
		} finally {
			if (target === node) submitting = false;
		}
	}

	setContext<HostUpdateContext>('hostUpdates', {
		get node() {
			return node;
		},
		get view() {
			return view;
		},
		get error() {
			return requestError;
		},
		get loading() {
			return inventory.loading;
		},
		get enabled() {
			return enabled;
		},
		refresh,
		run,
		openOperation,
		openMonitoring: () => (monitoringOpen = true)
	});
	watch(
		() => node,
		() => {
			jobId = 0;
			operationOpen = false;
			monitoringOpen = false;
			submitting = false;
		}
	);
	watch(
		[() => node, () => requestedId],
		([_, id]) => {
			if (Number.isSafeInteger(id) && id > 0) openOperation(id);
		},
		{ lazy: false }
	);
	watch(
		[() => node, () => requestedDialog],
		([_, dialog]) => {
			monitoringOpen = dialog === 'monitoring';
		},
		{ lazy: false }
	);
	useInterval(3000, {
		callback: () => {
			if (
				view?.activeJob &&
				!operationOpen &&
				!inventory.loading &&
				document.visibilityState === 'visible'
			)
				void refresh();
		}
	});
</script>

<div class="flex h-full w-full flex-col">
	{#if requestError}<p class="text-destructive border-b px-3 py-2 text-xs" role="alert">
			{requestError}
		</p>{/if}
	{#if view?.reason && view.reason !== 'host_update_in_progress' && !page.url.pathname.endsWith('/system/updates/sylve')}<p
			class="border-b px-3 py-2 text-xs"
			role="status"
		>
			{hostUpdateReason(view.reason)}
		</p>{/if}
	<div class="min-h-0 flex-1">{@render children()}</div>
</div>

{#key node}
	{#if monitoringOpen && view}<Monitoring
			bind:open={monitoringOpen}
			{node}
			{view}
			{enabled}
			afterChange={refresh}
		/>{/if}
	{#key jobId}{#if operationOpen && jobId > 0}<Operation
				bind:open={operationOpen}
				{node}
				{jobId}
				bootEnvironmentSupported={view?.bootEnvironmentSupported ?? false}
				afterChange={refresh}
			/>{/if}{/key}
{/key}
