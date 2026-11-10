<script lang="ts">
	import { listGrants, listRoles } from '$lib/api/auth/permissions';
	import { joinCluster, refreshClusterAfterLifecycleChange } from '$lib/api/cluster/cluster';
	import * as Alert from '$lib/components/ui/alert/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { getClusterJoinErrorMessage } from '$lib/utils/cluster';
	import { isValidIPv4 } from '$lib/utils/string';
	import { toast } from 'svelte-sonner';
	import { storage } from '$lib';
	import { watch } from 'runed';
	import SpanWithIcon from '../SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		reload: boolean;
	}

	let { open = $bindable(), reload = $bindable() }: Props = $props();
	let options = {
		ip: isValidIPv4(window.location.hostname) ? window.location.hostname : '',
		clusterKey: '',
		leaderIp: ''
	};

	let properties = $state(options);
	let loading = $state(false);

	// Joining replaces this node's grants and custom roles with the cluster's.
	let replaced = $state<string[]>([]);
	async function loadReplaced() {
		const [grants, roles] = await Promise.all([listGrants(), listRoles()]);
		replaced = [
			...(isAPIResponse(grants) ? [] : grants).map(
				(grant) => `${grant.role} on ${grant.path} for ${grant.subject}`
			),
			...(isAPIResponse(roles) ? [] : roles)
				.filter((role) => !role.builtin)
				.map((role) => `Custom role ${role.name}`)
		];
	}

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) void loadReplaced();
		}
	);

	function getJoinErrorMessage(response: { message?: string; error?: string | string[] }): string {
		const details = Array.isArray(response.error) ? response.error.join(' ') : response.error || '';
		return getClusterJoinErrorMessage(`${response.message || ''} ${details}`, false);
	}

	async function join() {
		// Clustering is IPv4-only; the backend rejects IPv6 addresses.
		let error = '';

		if (!isValidIPv4(properties.ip)) {
			error = 'Node IP must be an IPv4 address';
		} else if (!isValidIPv4(properties.leaderIp)) {
			error = 'Cluster Node IP must be an IPv4 address';
		} else if (!properties.clusterKey) {
			error = 'Join Key is required';
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

			await refreshClusterAfterLifecycleChange();
			toast.success('Cluster join started; state verification will continue in the background', {
				position: 'bottom-center'
			});
			open = false;
			properties = options;
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
				placeholder="Cluster Node IP (192.168.1.1)"
				classes="flex-1 space-y-1.5 w-1/2"
			/>

			<Input
				type="password"
				id="cluster-key"
				placeholder="Join Key"
				class="w-1/2"
				autocomplete="off"
				bind:value={properties.clusterKey}
				showPasswordOnFocus={true}
			/>
		</div>

		{#if replaced.length > 0}
			<Alert.Root variant="destructive">
				<Alert.Title>
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--alert-circle-outline] h-4 w-4 shrink-0"></span>
						<span>The cluster's permissions replace this node's</span>
					</div>
				</Alert.Title>
				<Alert.Description>
					<p>These are removed; root and admin keep full access:</p>
					<ul class="max-h-32 list-inside list-disc space-y-1 overflow-y-auto">
						{#each replaced as line (line)}
							<li>{line}</li>
						{/each}
					</ul>
				</Alert.Description>
			</Alert.Root>
		{/if}

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
