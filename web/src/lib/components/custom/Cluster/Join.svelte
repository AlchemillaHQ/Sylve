<script lang="ts">
	import { joinCluster } from '$lib/api/cluster/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import { clusterStore } from '$lib/stores/auth';
	import { nodeId } from '$lib/stores/basic';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidIPv4, isValidIPv6, isValidPortNumber } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		reload: boolean;
	}

	let { open = $bindable(), reload = $bindable() }: Props = $props();
	let options = {
		ip:
			isValidIPv4(window.location.hostname) || isValidIPv6(window.location.hostname)
				? window.location.hostname
				: '',
		port: 8182,
		clusterKey: '',
		leaderApi: ''
	};

	let properties = $state(options);

	async function join() {
		let error = '';

		if (!isValidIPv4(properties.ip) && !isValidIPv6(properties.ip)) {
			error = 'Invalid IP address';
		} else if (!isValidPortNumber(properties.port)) {
			error = 'Invalid port number';
		}

		if (!properties.leaderApi) {
			error = 'Leader API is required';
		} else if (!properties.clusterKey) {
			error = 'Cluster Key is required';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});

			return;
		}

		const response = await joinCluster(
			$nodeId,
			properties.ip,
			Number(properties.port),
			properties.leaderApi,
			properties.clusterKey
		);

		reload = true;

		if (response.error) {
			handleAPIError(response);
			toast.error('Unable to join cluster', {
				position: 'bottom-center'
			});
			return;
		}

		if (response.data) {
			if (typeof response.data === 'string') {
				clusterStore.set(response.data);
			}
		}

		toast.success('Joined cluster', {
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
					<span class="icon-[grommet-icons--cluster] h-6 w-6"></span>
					<span>Join Cluster</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Reset'}
						onclick={() => {
							properties = options;
						}}
					>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>

					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							open = false;
							properties = options;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex flex-row gap-2">
			<CustomValueInput
				bind:value={properties.ip}
				placeholder="Node IP"
				classes="flex-1 space-y-1.5"
			/>

			<CustomValueInput
				bind:value={properties.port}
				placeholder="Node Port"
				classes="flex-1 space-y-1.5"
				type="number"
			/>
		</div>

		<div class="flex flex-row gap-2">
			<input type="text" style="display:none" autocomplete="username" />
			<input type="password" style="display:none" autocomplete="new-password" />

			<CustomValueInput
				bind:value={properties.leaderApi}
				placeholder="Leader API (192.168.1.1:8181)"
				classes="flex-1 space-y-1.5 w-1/2"
			/>

			<Input
				type="password"
				id="cluster-key"
				placeholder="Cluster Key"
				class="w-1/2"
				autocomplete="off"
				bind:value={properties.clusterKey}
				showPasswordOnFocus={true}
			/>
		</div>

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={join} type="submit" size="sm">{'Join'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
