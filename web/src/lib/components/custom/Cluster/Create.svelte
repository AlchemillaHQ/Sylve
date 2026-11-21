<script lang="ts">
	import { createCluster, refreshClusterAfterLifecycleChange } from '$lib/api/cluster/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { handleAPIError } from '$lib/utils/http';
	import { normalizeNodeIP } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';
	import { watch } from 'runed';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		reload: boolean;
	}

	let { open = $bindable(), reload = $bindable() }: Props = $props();
	let options = {
		ip: ''
	};

	let properties = $state(options);
	let loading = $state(false);
	let resetIP = $state(false);

	watch([() => open, () => window?.location?.hostname, () => resetIP], ([open, hostname]) => {
		if (open && hostname) {
			const ip = normalizeNodeIP(hostname);
			if (ip) {
				properties.ip = ip;
			}
		}
	});

	async function create() {
		const ip = normalizeNodeIP(properties.ip);
		if (!ip) {
			toast.error('Node IP must be an IPv4 or IPv6 address that other nodes can reach', {
				position: 'bottom-center'
			});

			return;
		}

		loading = true;

		const response = await createCluster(ip);
		reload = true;
		loading = false;
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to create cluster', {
				position: 'bottom-center'
			});
		} else {
			await refreshClusterAfterLifecycleChange();
			toast.success('Cluster created', {
				position: 'bottom-center'
			});

			open = false;
			properties = options;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		showCloseButton={true}
		showResetButton={true}
		onReset={() => {
			resetIP = !resetIP;
		}}
		onClose={() => (properties = options)}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[oui--ml-create-population-job]"
					size="h-6 w-6"
					gap="gap-2"
					title="Create Cluster"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4">
			<CustomValueInput bind:value={properties.ip} placeholder="Node IP" classes="w-full" />
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={create} type="submit" size="sm" disabled={loading}>
					{#if loading}
						<span class="icon-[mdi-light--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Create
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
