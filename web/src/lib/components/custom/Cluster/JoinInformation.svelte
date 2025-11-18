<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import { nodeId } from '$lib/stores/basic';
	import type { ClusterDetails } from '$lib/types/cluster/cluster';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		cluster: ClusterDetails | undefined;
	}

	let { open = $bindable(), cluster }: Props = $props();

	function copy() {
		navigator.clipboard.writeText(cluster?.cluster.key || '');
		toast.success('Cluster key copied', {
			position: 'bottom-center'
		});

		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[ant-design--cluster-outlined] h-6 w-6"></span>
					<span>Cluster Information</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
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
					<Table.Cell>{$nodeId}</Table.Cell>
				</Table.Row>
				<Table.Row>
					<Table.Cell>Leader Node</Table.Cell>
					<Table.Cell>{cluster?.leaderAddress}</Table.Cell>
				</Table.Row>
				<Table.Row>
					<Table.Cell>Cluster Key</Table.Cell>
					<Table.Cell>{cluster?.cluster.key}</Table.Cell>
				</Table.Row>
			</Table.Body>
		</Table.Root>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={copy} type="submit" size="sm">{'Copy'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
