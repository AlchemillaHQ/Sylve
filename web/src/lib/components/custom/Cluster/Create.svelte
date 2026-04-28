<script lang="ts">
	import { createCluster } from '$lib/api/cluster/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { storage } from '$lib';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidIPv4, isValidIPv6 } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';
	import { logOut } from '$lib/api/auth';
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
			if (isValidIPv4(hostname) || isValidIPv6(hostname)) {
				properties.ip = hostname;
			}
		}
	});

	async function create() {
		let error = '';

		if (!isValidIPv4(properties.ip) && !isValidIPv6(properties.ip)) {
			error = 'Invalid IP address';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});

			return;
		}

		loading = true;

		const response = await createCluster(properties.ip);
		reload = true;
		loading = false;
		if (response.error) {
			handleAPIError(response);
			toast.error('Failed to create cluster', {
				position: 'bottom-center'
			});
		} else {
			if (typeof response.data === 'string') {
				storage.clusterToken = response.data;
			}

			toast.success('Cluster created', {
				position: 'bottom-center'
			});

			open = false;
			properties = options;

			await logOut('Login required after initializing cluster');
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
