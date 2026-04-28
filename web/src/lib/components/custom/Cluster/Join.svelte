<script lang="ts">
	import { joinCluster } from '$lib/api/cluster/cluster';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import { handleAPIError } from '$lib/utils/http';
	import { isValidIPv4, isValidIPv6 } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';
	import { storage } from '$lib';
	import { logOut } from '$lib/api/auth';
	import SpanWithIcon from '../SpanWithIcon.svelte';

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
		clusterKey: '',
		leaderIp: ''
	};

	let properties = $state(options);
	let loading = $state(false);

	function getJoinErrorMessage(response: { message?: string; error?: string | string[] }): string {
		const backendReportedMismatch =
			response.message === 'cluster_version_mismatch' ||
			(typeof response.error === 'string' && response.error.includes('leader=')) ||
			(Array.isArray(response.error) && response.error.some((item) => item.includes('leader=')));

		if (backendReportedMismatch) {
			return 'Version mismatch: this node and the leader must run the same Sylve version';
		}

		return 'Unable to join cluster';
	}

	async function join() {
		let error = '';

		if (!isValidIPv4(properties.ip) && !isValidIPv6(properties.ip)) {
			error = 'Invalid IP address';
		}

		if (!isValidIPv4(properties.leaderIp) && !isValidIPv6(properties.leaderIp)) {
			error = 'Leader IP is required';
		} else if (!properties.clusterKey) {
			error = 'Cluster Key is required';
		}

		if (error) {
			toast.error(error, {
				position: 'bottom-center'
			});

			return;
		}

		if (storage.nodeId) {
			loading = true;

			const response = await joinCluster(
				storage.nodeId,
				properties.ip,
				properties.leaderIp,
				properties.clusterKey
			);

			loading = false;
			reload = true;

			if (response.error) {
				handleAPIError(response);
				toast.error(getJoinErrorMessage(response), {
					position: 'bottom-center'
				});
				return;
			}

			if (response.data) {
				if (typeof response.data === 'string') {
					storage.clusterToken = response.data;
				}
			}

			toast.success('Joined cluster', {
				position: 'bottom-center'
			});

			await logOut('Login required after joining cluster');
		} else {
			toast.error('No Node ID available', {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		showCloseButton={true}
		showResetButton={true}
		onReset={() => {
			properties = options;
		}}
		onClose={() => {
			properties = options;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[grommet-icons--cluster]"
					size="h-6 w-6"
					gap="gap-2"
					title="Join Cluster"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="flex flex-row gap-2">
			<CustomValueInput
				bind:value={properties.ip}
				placeholder="Node IP"
				classes="flex-1 space-y-1.5"
			/>
		</div>

		<div class="flex flex-row gap-2">
			<input type="text" style="display:none" autocomplete="username" />
			<input type="password" style="display:none" autocomplete="new-password" />

			<CustomValueInput
				bind:value={properties.leaderIp}
				placeholder="Leader IP (192.168.1.1)"
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
				<Button onclick={join} type="submit" size="sm" disabled={loading}>
					{#if loading}
						<div class="flex items-center gap-2">
							<span class="icon-[mdi--loading] animate-spin h-4 w-4"></span>
							<span>Joining</span>
						</div>
					{:else}
						Join
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
