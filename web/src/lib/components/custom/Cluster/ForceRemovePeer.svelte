<script lang="ts">
	import { forceRemovePeer, refreshClusterAfterLifecycleChange } from '$lib/api/cluster/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
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
	let fenced = $state(false);
	let busy = $state(false);
	let conflict: PeerRemovalConflict | null = $state(null);

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				fenced = false;
				busy = false;
				conflict = null;
			}
		}
	);

	async function confirm() {
		if (!node || !fenced || busy) return;
		busy = true;
		conflict = null;
		try {
			const response = await forceRemovePeer(String(node.id));
			if (response.error) {
				if (response.message === 'peer_removal_blocked') {
					const parsed = PeerRemovalConflictSchema.safeParse(response.data);
					conflict = parsed.success ? parsed.data : null;
					return;
				}
				handleAPIError(response);
				toast.error(
					response.message === 'cluster_consensus_unavailable'
						? 'Raft cannot commit removal without quorum'
						: 'Failed to force remove peer',
					{ position: 'bottom-center' }
				);
				return;
			}
			await refreshClusterAfterLifecycleChange();
			reload = true;
			open = false;
			toast.success('Peer membership force removed; target cleanup is not confirmed', {
				position: 'bottom-center'
			});
		} finally {
			busy = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--alert-octagon-outline]"
					size="h-6 w-6"
					gap="gap-2"
					title="Force Remove Peer"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 text-sm">
			<div class="rounded-md border border-red-500/30 bg-red-500/10 p-3">
				<p class="font-medium text-red-600 dark:text-red-400">
					This removes only Raft membership for <span class="break-all font-mono">{node?.id}</span>.
				</p>
				<p class="mt-1 text-muted-foreground">
					The target is not contacted or cleaned. Force removal cannot bypass missing Raft quorum.
				</p>
			</div>

			{#if conflict}
				<div class="rounded-md border p-3">
					<p class="font-medium">The node still owns cluster resources</p>
					<ul class="mt-2 space-y-1 text-muted-foreground">
						{#each conflict.dependencies as dependency (dependency.kind + dependency.id)}
							<li>{dependency.kind}: {dependency.name || dependency.id}</li>
						{/each}
					</ul>
				</div>
			{/if}

			<CustomCheckbox
				label="The target's power, storage, or network is externally fenced"
				bind:checked={fenced}
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={() => (open = false)} size="sm" variant="outline" disabled={busy}>
					Cancel
				</Button>
				<Button
					onclick={confirm}
					size="sm"
					variant="destructive"
					disabled={!node || !fenced || busy}
				>
					{busy ? 'Removing…' : 'Force Remove Membership'}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
