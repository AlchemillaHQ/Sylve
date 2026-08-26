<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { ClusterDetails } from '$lib/types/cluster/cluster';
	import { toast } from 'svelte-sonner';
	import { storage } from '$lib';
	import { getJoinKey } from '$lib/api/cluster/cluster';
	import { isAPIResponse } from '$lib/utils/http';
	import { watch } from 'runed';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		cluster: ClusterDetails | undefined;
	}

	let { open = $bindable(), cluster }: Props = $props();
	let clusterKey = $state('');
	let loading = $state(false);
	let requestGeneration = 0;

	async function loadClusterKey(generation: number) {
		const result = await getJoinKey();

		if (generation !== requestGeneration || !open) return;

		loading = false;
		if (isAPIResponse(result)) {
			toast.error('Unable to load cluster key', { position: 'bottom-center' });
			return;
		}

		clusterKey = result.key;
	}

	watch(
		() => open,
		(isOpen) => {
			const generation = ++requestGeneration;
			clusterKey = '';
			if (!isOpen) {
				loading = false;
				return;
			}

			loading = true;
			void loadClusterKey(generation);
		}
	);

	async function copy() {
		if (!clusterKey) return;
		await navigator.clipboard.writeText(clusterKey);
		toast.success('Cluster key copied', {
			position: 'bottom-center'
		});

		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[ant-design--cluster-outlined]"
					size="h-6 w-6"
					gap="gap-2"
					title="Cluster Information"
				/>
			</Dialog.Title>
		</Dialog.Header>
		<Table.Root>
			<Table.Header>
				<Table.Row>
					<Table.Head>Property</Table.Head>
					<Table.Head>Value</Table.Head>
				</Table.Row>
			</Table.Header>
			<Table.Body>
				<Table.Row>
					<Table.Cell>Node ID</Table.Cell>
					<Table.Cell>{storage.nodeId}</Table.Cell>
				</Table.Row>
				<Table.Row>
					<Table.Cell>Leader Node</Table.Cell>
					<Table.Cell>{cluster?.leaderAddress}</Table.Cell>
				</Table.Row>
				<Table.Row>
					<Table.Cell>Cluster Key</Table.Cell>
					<Table.Cell>{loading ? 'Loading…' : clusterKey || 'Unavailable'}</Table.Cell>
				</Table.Row>
			</Table.Body>
		</Table.Root>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={copy} type="submit" size="sm" disabled={loading || !clusterKey}>
					Copy
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
