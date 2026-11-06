<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import type { ClusterDetails } from '$lib/types/cluster/cluster';
	import { toast } from 'svelte-sonner';
	import { storage } from '$lib';
	import { generateJoinKey } from '$lib/api/cluster/cluster';
	import { isAPIResponse } from '$lib/utils/http';
	import { watch } from 'runed';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		cluster: ClusterDetails | undefined;
	}

	let { open = $bindable(), cluster }: Props = $props();
	let joinKey = $state('');
	let joinAddress = $state('');
	let loading = $state(false);
	let requestGeneration = 0;

	async function generate() {
		const generation = ++requestGeneration;
		loading = true;
		const result = await generateJoinKey();

		if (generation !== requestGeneration || !open) return;

		loading = false;
		if (isAPIResponse(result)) {
			toast.error('Unable to generate a join key', { position: 'bottom-center' });
			return;
		}

		joinKey = result.key;
		joinAddress = result.address;
	}

	watch(
		() => open,
		() => {
			requestGeneration++;
			joinKey = '';
			joinAddress = '';
			loading = false;
		}
	);

	async function copy() {
		if (!joinKey) return;
		await navigator.clipboard.writeText(joinKey);
		toast.success('Join key copied', {
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
				{#if joinKey}
					<Table.Row>
						<Table.Cell>Join Through</Table.Cell>
						<Table.Cell>{joinAddress}</Table.Cell>
					</Table.Row>
					<Table.Row>
						<Table.Cell>Join Key</Table.Cell>
						<Table.Cell class="whitespace-normal break-all font-mono text-xs">{joinKey}</Table.Cell>
					</Table.Row>
				{/if}
			</Table.Body>
		</Table.Root>

		<p class="text-muted-foreground text-sm">
			A join key admits one node, which joins through this node. Generating a new key replaces the
			previous one.
		</p>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={generate} variant="outline" size="sm" disabled={loading}>
					{#if loading}
						<div class="flex items-center gap-2">
							<span class="icon-[mdi--loading] animate-spin h-4 w-4"></span>
							<span>Generating</span>
						</div>
					{:else}
						{joinKey ? 'Generate New Key' : 'Generate Join Key'}
					{/if}
				</Button>
				<Button onclick={copy} type="submit" size="sm" disabled={loading || !joinKey}>Copy</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
