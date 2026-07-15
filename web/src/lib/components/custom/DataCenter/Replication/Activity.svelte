<script lang="ts">
	import { getNodes } from '$lib/api/cluster/cluster';
	import { listReplicationEvents, listReplicationPolicies } from '$lib/api/cluster/replication';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ReplicationEvent, ReplicationPolicy } from '$lib/types/cluster/replication';
	import { convertDbTime } from '$lib/utils/time';
	import { storage } from '$lib';
	import { resource, useInterval } from 'runed';
	import SpanWithIcon from '../../SpanWithIcon.svelte';

	const IN_PROGRESS_STATUSES = new Set([
		'running',
		'demoting',
		'promoting',
		'catchup',
		'catch-up',
		'catching_up',
		'catching-up'
	]);

	function hasCatchupHint(message: string): boolean {
		const normalized = String(message || '')
			.trim()
			.toLowerCase();
		if (!normalized) return false;
		return (
			normalized.includes('catchup') ||
			normalized.includes('catch-up') ||
			normalized.includes('catch_up') ||
			normalized.includes('catching_up') ||
			normalized.includes('catching-up')
		);
	}

	function parsedTime(value: string | null | undefined): number | null {
		if (!value) return null;
		const parsed = Date.parse(value);
		return Number.isFinite(parsed) ? parsed : null;
	}

	function isCompatibleLegacyFailoverEvent(
		event: ReplicationEvent,
		policy: ReplicationPolicy
	): boolean {
		const eventSource = String(event.sourceNodeId || '').trim();
		const eventTarget = String(event.targetNodeId || '').trim();
		const transitionSource = String(policy.transitionSourceNodeId || '').trim();
		const transitionTarget = String(policy.transitionTargetNodeId || '').trim();
		if (
			eventSource &&
			eventTarget &&
			transitionSource &&
			transitionTarget &&
			!(
				(eventSource === transitionSource && eventTarget === transitionTarget) ||
				(eventSource === transitionTarget && eventTarget === transitionSource)
			)
		) {
			return false;
		}

		const eventStartedAt = parsedTime(event.startedAt);
		const transitionRequestedAt = parsedTime(policy.transitionRequestedAt);
		const transitionCompletedAt = parsedTime(policy.transitionCompletedAt);
		if (
			eventStartedAt !== null &&
			transitionRequestedAt !== null &&
			eventStartedAt < transitionRequestedAt
		) {
			return false;
		}
		if (
			eventStartedAt !== null &&
			transitionCompletedAt !== null &&
			eventStartedAt > transitionCompletedAt
		) {
			return false;
		}
		return true;
	}

	function isReplicationEventInProgress(
		event: ReplicationEvent,
		policy?: ReplicationPolicy,
		legacyCandidateCount: number = 1
	): boolean {
		if (event.completedAt) return false;

		const eventType = String(event.eventType || '')
			.trim()
			.toLowerCase();
		if (eventType !== 'replication' && eventType !== 'failover') return false;

		const status = String(event.status || '')
			.trim()
			.toLowerCase();
		const statusInProgress =
			IN_PROGRESS_STATUSES.has(status) ||
			((status === 'demoting' || status === 'running') && hasCatchupHint(event.message || ''));
		if (!statusInProgress) return false;
		if (!policy) return true;

		if (eventType === 'failover') {
			const transitionState = String(policy.transitionState || '')
				.trim()
				.toLowerCase();
			if (
				policy.transitionCompletedAt ||
				transitionState === 'none' ||
				transitionState === 'completed' ||
				transitionState === 'failed'
			) {
				return false;
			}

			const eventRunId = String(event.transitionRunId || '').trim();
			const policyRunId = String(policy.transitionRunId || '').trim();
			if (eventRunId && policyRunId && eventRunId !== policyRunId) return false;
			if (!eventRunId) {
				if (legacyCandidateCount !== 1 || !isCompatibleLegacyFailoverEvent(event, policy))
					return false;
			}
		}

		if (eventType === 'replication') {
			const eventStartedAt = parsedTime(event.startedAt);
			const lastRunAt = parsedTime(policy.lastRunAt);
			if (eventStartedAt !== null && lastRunAt !== null && lastRunAt >= eventStartedAt) {
				return false;
			}
		}

		return true;
	}

	function filterInProgressReplicationEvents(
		events: ReplicationEvent[],
		policyById: Record<number, ReplicationPolicy>
	): ReplicationEvent[] {
		const legacyCandidatesByPolicy: Record<number, number> = {};
		for (const event of events) {
			const policy = event.policyId ? policyById[event.policyId] : undefined;
			if (
				event.policyId &&
				policy &&
				String(event.eventType || '').toLowerCase() === 'failover' &&
				!String(event.transitionRunId || '').trim() &&
				!event.completedAt &&
				IN_PROGRESS_STATUSES.has(
					String(event.status || '')
						.trim()
						.toLowerCase()
				) &&
				isCompatibleLegacyFailoverEvent(event, policy)
			) {
				legacyCandidatesByPolicy[event.policyId] =
					(legacyCandidatesByPolicy[event.policyId] || 0) + 1;
			}
		}
		return events.filter((event) => {
			const policy = event.policyId ? policyById[event.policyId] : undefined;
			const legacyCandidateCount = event.policyId
				? (legacyCandidatesByPolicy[event.policyId] ?? 1)
				: 1;
			return isReplicationEventInProgress(event, policy, legacyCandidateCount);
		});
	}

	function compactNodeLabel(value: string, nodeNameById: Record<string, string>): string {
		const nodeId = String(value || '').trim();
		if (!nodeId) return '-';
		const hostname = nodeNameById[nodeId];
		if (hostname) return hostname;
		return nodeId.length > 12 ? `${nodeId.slice(0, 8)}...` : nodeId;
	}

	function eventPath(
		event: ReplicationEvent,
		policy: ReplicationPolicy | undefined,
		nodeNameById: Record<string, string>
	): string {
		const sourceNodeId = String(
			event.sourceNodeId || policy?.activeNodeId || policy?.sourceNodeId || ''
		).trim();
		const directTargetNodeId = String(event.targetNodeId || '').trim();

		let targetNodeIds: string[] = [];
		if (directTargetNodeId) {
			targetNodeIds = [directTargetNodeId];
		} else if (event.eventType === 'replication' && policy) {
			targetNodeIds = policy.targets
				.slice()
				.sort((a, b) => b.weight - a.weight || a.nodeId.localeCompare(b.nodeId))
				.map((target) => String(target.nodeId || '').trim());
		}

		const destinations = Array.from(
			new Set(targetNodeIds.filter((nodeId) => nodeId && nodeId !== sourceNodeId))
		).map((nodeId) => compactNodeLabel(nodeId, nodeNameById));

		const destinationLabel =
			destinations.length > 0
				? destinations.join(', ')
				: event.eventType === 'replication' && !directTargetNodeId
					? 'policy targets'
					: '-';

		return `${compactNodeLabel(sourceNodeId, nodeNameById)} → ${destinationLabel}`;
	}

	function eventMessageLabel(value: string): string {
		const message = String(value || '')
			.trim()
			.replace(/[_-]+/g, ' ')
			.replace(/\s+/g, ' ');
		if (!message) return '-';
		return message.charAt(0).toUpperCase() + message.slice(1);
	}

	let replicationModalOpen = $state(false);

	let replicationActivity = resource(
		() => 'header-replication-activity',
		async () => {
			try {
				const [policies, events, nodes] = await Promise.all([
					listReplicationPolicies(),
					listReplicationEvents(200),
					getNodes().catch(() => [])
				]);
				const policyById: Record<number, ReplicationPolicy> = {};
				const policyNameById: Record<number, string> = {};
				for (const policy of policies) {
					policyById[policy.id] = policy;
					policyNameById[policy.id] = policy.name;
				}
				const nodeNameById: Record<string, string> = {};
				for (const node of nodes) {
					nodeNameById[node.nodeUUID] = node.hostname || node.nodeUUID;
				}

				const running = filterInProgressReplicationEvents(events, policyById)
					.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt))
					.map((event) => {
						const policy = event.policyId ? policyById[event.policyId] : undefined;
						return {
							...event,
							policyName: event.policyId
								? (policyNameById[event.policyId] ?? `Policy ${event.policyId}`)
								: '-',
							path: eventPath(event, policy, nodeNameById),
							messageLabel: eventMessageLabel(event.message || '')
						};
					});

				return {
					available: true,
					running,
					updatedAt: new Date().toISOString(),
					error: ''
				};
			} catch (error: unknown) {
				return {
					available: false,
					running: [],
					updatedAt: new Date().toISOString(),
					error: (error as Error)?.message || 'Failed to load replication activity'
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

	function inProgressLabel(status: string): string {
		switch (
			String(status || '')
				.trim()
				.toLowerCase()
		) {
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
			if (!storage.visible) return;
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
	<Dialog.Content class="w-[90%] max-w-2xl overflow-hidden p-6">
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--progress-clock]"
					title="Replication Activity"
					size="w-4 h-4"
					gap="gap-2"
				/>
			</Dialog.Title>
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
							<div class="text-right">{event.path}</div>
							<div>Started</div>
							<div class="text-right">{convertDbTime(event.startedAt)}</div>
							<div>Message</div>
							<div class="text-right">{event.messageLabel}</div>
						</div>
					</div>
				{/each}
			</div>
		{/if}

		{#if replicationActivity.current.updatedAt}
			<div class="text-xs text-muted-foreground">
				Auto-refreshes every 5 seconds · Last updated: {convertDbTime(
					replicationActivity.current.updatedAt
				)}
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
