<script lang="ts">
	import { listReplicationEvents, listReplicationPolicies } from '$lib/api/cluster/replication';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { filterInProgressReplicationEvents } from '$lib/utils/replication-activity';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, useInterval } from 'runed';

	let replicationModalOpen = $state(false);

	// svelte-ignore state_referenced_locally
	let replicationActivity = resource(
		() => 'header-replication-activity',
		async () => {
			try {
				const [policies, events] = await Promise.all([
					listReplicationPolicies(),
					listReplicationEvents(200)
				]);
				const policyNameById: Record<number, string> = {};
				for (const policy of policies) {
					policyNameById[policy.id] = policy.name;
				}

				const running = filterInProgressReplicationEvents(events)
					.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt))
					.map((event) => ({
						...event,
						policyName: event.policyId
							? (policyNameById[event.policyId] ?? `Policy ${event.policyId}`)
							: '-'
					}));

				return {
					available: true,
					running,
					updatedAt: new Date().toISOString(),
					error: ''
				};
			} catch (error: any) {
				return {
					available: false,
					running: [],
					updatedAt: new Date().toISOString(),
					error: error?.message || 'Failed to load replication activity'
				};
			}
		},
		{
			initialValue: {
				available: false,
				running: [],
				updatedAt: '',
				error: ''
			}
		}
	);

	let runningReplicationEvents = $derived(replicationActivity.current.running || []);
	let runningReplicationCount = $derived(runningReplicationEvents.length);

	function eventTypeLabel(value: string): string {
		if (value === 'failover') return 'Failover';
		if (value === 'replication') return 'Replication';
		return value || 'Event';
	}

	function compactNodeLabel(value: string): string {
		const nodeId = String(value || '').trim();
		if (!nodeId) return '-';
		return nodeId.length > 12 ? `${nodeId.slice(0, 8)}...` : nodeId;
	}

	function inProgressLabel(status: string): string {
		switch (String(status || '').trim().toLowerCase()) {
			case 'demoting':
				return 'Demoting';
			case 'promoting':
				return 'Promoting';
			case 'catchup':
			case 'catch-up':
			case 'catching_up':
			case 'catching-up':
				return 'Catching up';
			default:
				return 'Running';
		}
	}

	useInterval(5000, {
		callback: () => {
			if (
				!replicationModalOpen &&
				!replicationActivity.current.available &&
				runningReplicationCount === 0
			) {
				return;
			}
			replicationActivity.refetch();
		}
	});
</script>

{#if runningReplicationCount > 0}
	<Button
		class="relative h-6"
		size="sm"
		variant="outline"
		onclick={() => {
			replicationModalOpen = true;
			replicationActivity.refetch();
		}}
	>
		<div class="flex items-center gap-2">
			<span class="icon-[mdi--progress-clock] h-4 w-4"></span>
			<span>Replication</span>
		</div>
		{#if runningReplicationCount > 0}
			<span
				class="bg-primary text-primary-foreground absolute -right-2 -top-2 inline-flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px]"
				>{runningReplicationCount}</span
			>
		{/if}
	</Button>
{/if}

<Dialog.Root bind:open={replicationModalOpen}>
	<Dialog.Content class="w-[90%] max-w-2xl overflow-hidden p-5">
		<Dialog.Header>
			<Dialog.Title>Replication Activity</Dialog.Title>
		</Dialog.Header>

		{#if !replicationActivity.current.available && replicationActivity.current.error}
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-300">
				{replicationActivity.current.error}
			</div>
		{:else if runningReplicationEvents.length === 0}
			<div class="rounded-md border bg-muted/20 p-3 text-sm text-muted-foreground">
				No failover or replication task is running right now.
			</div>
		{:else}
			<div class="max-h-[55vh] space-y-2 overflow-auto pr-1">
				{#each runningReplicationEvents as event (event.id)}
					<div class="rounded-md border p-3">
						<div class="flex items-center justify-between gap-2">
							<div class="text-sm font-medium">
								{eventTypeLabel(event.eventType)} - {event.policyName}
							</div>
							<div class="text-xs text-yellow-500">{inProgressLabel(event.status)}</div>
						</div>
						<div class="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs text-muted-foreground">
							<div>Workload</div>
							<div class="text-right">{event.guestType || 'guest'} {event.guestId || 0}</div>
							<div>Path</div>
							<div class="text-right">
								{compactNodeLabel(event.sourceNodeId)} -> {compactNodeLabel(event.targetNodeId)}
							</div>
							<div>Started</div>
							<div class="text-right">{convertDbTime(event.startedAt)}</div>
							<div>Message</div>
							<div class="text-right">{event.message || '-'}</div>
						</div>
					</div>
				{/each}
			</div>
		{/if}

		{#if replicationActivity.current.updatedAt}
			<div class="text-xs text-muted-foreground">
				Last updated: {convertDbTime(replicationActivity.current.updatedAt)}
			</div>
		{/if}

		<Dialog.Footer>
			<Button variant="outline" class="h-7" onclick={() => replicationActivity.refetch()}
				>Refresh</Button
			>
			<Button variant="outline" class="h-7" onclick={() => (replicationModalOpen = false)}
				>Close</Button
			>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
