<script lang="ts">
	import { refreshClusterAfterLifecycleChange, removePeer } from '$lib/api/cluster/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import { PeerRemovalConflictSchema, type PeerRemovalConflict } from '$lib/types/cluster/cluster';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		reload: boolean;
		node: Row | null;
	}

	let { open = $bindable(), reload = $bindable(), node }: Props = $props();

	let busy = $state(false);
	let conflict: PeerRemovalConflict | null = $state(null);

	const dependencyLabels: Record<string, string> = {
		guest: 'Guest',
		backup_job: 'Backup Job',
		replication_policy: 'Replication Policy',
		replication_lease: 'Replication Lease',
		backup_operation: 'Backup Operation',
		replication_operation: 'Replication Operation',
		restore_operation: 'Restore Operation',
		guest_operation: 'Guest Operation',
		runner_rebind: 'Runner Rebind'
	};

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				conflict = null;
				busy = false;
			}
		}
	);

	function dependencyLine(dep: PeerRemovalConflict['dependencies'][number]): string {
		const parts: string[] = [];
		if (dep.name) parts.push(dep.name);
		if (dep.role) parts.push(dep.role);
		if (dep.state) parts.push(dep.state);
		return parts.join(' · ');
	}

	async function confirm() {
		if (!node || busy) return;
		busy = true;
		conflict = null;

		const removedVoter = String(node.suffrage ?? '').toLowerCase() === 'voter';

		try {
			const response = await removePeer(String(node.id));

			if (response.error) {
				if (response.message === 'peer_removal_blocked') {
					const parsed = PeerRemovalConflictSchema.safeParse(response.data);
					conflict = parsed.success ? parsed.data : null;
					return;
				}

				open = false;
				if (response.message === 'not_leader') {
					reload = true;
					toast.error(
						'This node is no longer the cluster leader. Refresh and retry on the leader.',
						{
							position: 'bottom-center'
						}
					);
					return;
				}

				handleAPIError(response);
				toast.error('Failed to remove peer', {
					position: 'bottom-center'
				});
				return;
			}

			await refreshClusterAfterLifecycleChange();
			reload = true;
			open = false;
			toast.success(
				removedVoter ? 'Peer removed. Cluster is running with reduced redundancy.' : 'Peer removed',
				{
					position: 'bottom-center'
				}
			);
		} finally {
			busy = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content showCloseButton={true} onClose={() => (conflict = null)}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--account-remove-outline]"
					size="h-6 w-6"
					gap="gap-2"
					title="Remove Peer"
				/>
			</Dialog.Title>
		</Dialog.Header>

		{#if conflict}
			<div class="grid gap-3">
				<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm">
					<p class="font-medium text-red-600 dark:text-red-400">
						Node still owns cluster resources
					</p>
					<p class="mt-1 text-muted-foreground">
						Move or delete these resources before removing the peer:
					</p>
				</div>

				<div class="max-h-72 space-y-1 overflow-y-auto rounded-md border p-2">
					{#if conflict.dependencies.length === 0}
						<p class="px-2 py-1.5 text-sm text-muted-foreground">
							No blocked resources were reported. Retry removal.
						</p>
					{:else}
						{#each conflict.dependencies as dep (dep.kind + dep.id)}
							<div
								class="flex items-center justify-between gap-2 rounded px-2 py-1.5 text-sm hover:bg-muted/30"
							>
								<span class="font-medium">{dependencyLabels[dep.kind] ?? dep.kind}</span>
								<span class="text-muted-foreground">{dependencyLine(dep)}</span>
							</div>
						{/each}
					{/if}
				</div>
			</div>
		{:else}
			<div class="grid gap-3">
				<div class="rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm">
					<p class="font-medium text-amber-700 dark:text-amber-400">
						This will remove the node from the cluster permanently.
					</p>
					<p class="mt-1 text-muted-foreground">
						Node <span class="font-semibold break-all">{node?.id}</span> will no longer participate in
						quorum. This action cannot be undone.
					</p>
				</div>

				{#if node?.status === 'online'}
					<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm">
						<p class="font-medium text-red-600 dark:text-red-400">This node is currently online</p>
						<p class="mt-1 text-muted-foreground">
							Removing a live node is destructive. Only continue if you intend to remove this peer
							while it is still running.
						</p>
					</div>
				{/if}
			</div>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				{#if conflict}
					<Button
						onclick={() => {
							conflict = null;
							open = false;
						}}
						size="sm"
						variant="outline"
					>
						Close
					</Button>
				{:else}
					<Button onclick={() => (open = false)} size="sm" variant="outline" disabled={busy}>
						Cancel
					</Button>
					<Button onclick={confirm} size="sm" variant="destructive" disabled={busy || !node}>
						{#if busy}
							<span class="icon-[mdi-light--loading] h-4 w-4 animate-spin"></span>
						{:else}
							Remove Peer
						{/if}
					</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
