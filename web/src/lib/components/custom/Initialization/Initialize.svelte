<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { mode } from 'mode-watcher';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { getPoolsResponse } from '$lib/api/zfs/pool';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Alert from '$lib/components/ui/alert/index.js';
	import { getInitializeHostState, initialize } from '$lib/api/basic';
	import { toast } from 'svelte-sonner';
	import Button from '$lib/components/ui/button/button.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import HostChanges from './HostChanges.svelte';
	import { onMount } from 'svelte';
	import { resource } from 'runed';
	import type { AvailableService } from '$lib/types/system/settings';
	import {
		INITIALIZATION_SERVICE_GROUPS,
		INITIALIZATION_SERVICES,
		pfInUse
	} from '$lib/system-services';
	import { getBasicHealth } from '$lib/api/system/system';
	import { isAPIResponse } from '$lib/utils/http';
	import type { InitializeHostState, InitializeResponse } from '$lib/types/basic';

	interface Props {
		initialized: boolean;
	}

	let { initialized = $bindable() }: Props = $props();

	const pools = resource(
		() => 'initialization-pools',
		async () => {
			const response = await getPoolsResponse(true);
			return response.status === 'success' && Array.isArray(response.data) ? response.data : [];
		}
	);

	let properties = $state({
		pools: {
			combobox: {
				open: false,
				values: [] as string[]
			}
		},
		services: Object.fromEntries(
			INITIALIZATION_SERVICES.map((service) => [service.id, service.defaultEnabled])
		) as Record<AvailableService, boolean>
	});
	let shownErrors = $state([] as string[]);
	let initializing = $state(false);
	let verifying = $state(false);
	let initializationAccepted = $state(false);
	let reviewing = $state(false);
	let reviewOpen = $state(false);
	let hostState = $state<InitializeHostState | null>(null);

	let selectedServices = $derived(
		INITIALIZATION_SERVICES.filter((service) => properties.services[service.id]).map(
			(service) => service.id
		)
	);

	async function loadHostState() {
		const state = await getInitializeHostState();
		hostState = isAPIResponse(state) ? null : state;
	}

	onMount(async () => {
		await loadHostState();
		if (hostState && pfInUse(hostState.pf)) {
			properties.services.firewall = false;
		}
	});

	async function reviewInit() {
		if (initializing || verifying || reviewing) return;
		if (initializationAccepted) {
			await verifyInitialization();
			return;
		}

		reviewing = true;
		try {
			await loadHostState();
			reviewOpen = true;
		} finally {
			reviewing = false;
		}
	}

	function getInitializationErrors(response: InitializeResponse): string[] {
		if (Array.isArray(response.data) && response.data.length > 0) {
			return response.data;
		}
		if (Array.isArray(response.error)) {
			return response.error;
		}
		if (response.error) {
			return [response.error];
		}

		return [response.message || 'Initialization failed'];
	}

	async function verifyInitialization() {
		if (verifying) return;

		verifying = true;
		try {
			const health = await getBasicHealth();
			if (isAPIResponse(health)) {
				shownErrors = [
					'Initialization was accepted, but its status could not be verified. Check the status again.'
				];
				return;
			}

			if (!health.initialized) {
				shownErrors = ['Initialization has not been confirmed yet. Check the status again.'];
				return;
			}

			shownErrors = [];
			toast.success('Sylve initialized', {
				position: 'bottom-center'
			});
			initialized = true;
		} finally {
			verifying = false;
		}
	}

	async function startInit() {
		if (initializing || verifying) return;

		initializing = true;
		try {
			const pools = properties.pools.combobox.values;
			const response = await initialize(pools, selectedServices);
			if (response.status !== 'success') {
				shownErrors = getInitializationErrors(response);
				return;
			}

			initializationAccepted = true;
			shownErrors = [];
			await verifyInitialization();
		} finally {
			initializing = false;
		}
	}
</script>

<Dialog.Root open={true}>
	<Dialog.Content
		overlayClass="bg-background"
		class="bg-card text-card-foreground max-h-[90dvh] overflow-y-auto"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
		showCloseButton={false}
	>
		<Dialog.Header>
			<div class="flex w-full items-center justify-between">
				<Dialog.Title class="flex-1 text-center">
					<div class="flex items-center justify-center space-x-2">
						{#if mode.current === 'dark'}
							<img src="/logo/white.svg" alt="Sylve Logo" class="h-8 w-auto max-w-25" />
						{:else}
							<img src="/logo/black.svg" alt="Sylve Logo" class="h-8 w-auto max-w-25" />
						{/if}
						<p class="font-normal tracking-[.45em]">SYLVE</p>
					</div>
				</Dialog.Title>
			</div>
		</Dialog.Header>

		<div class="flex flex-col gap-4">
			<ComboBox
				bind:open={properties.pools.combobox.open}
				label="ZFS Storage Pools"
				bind:value={properties.pools.combobox.values}
				data={generateComboboxOptions((pools.current ?? []).map((p) => p.name))}
				classes="flex-1 space-y-3"
				placeholder="Select Pools"
				width="w-full"
				multiple={true}
				disabled={pools.loading || initializing || verifying || initializationAccepted}
			></ComboBox>

			{#if pools.loading}
				<p class="text-sm text-muted-foreground" role="status">Loading existing ZFS pools...</p>
			{/if}

			<div class="space-y-2">
				<Label class="text-sm font-medium text-gray-600 dark:text-gray-300">Services</Label>
				<div class="grid grid-cols-1 gap-1 sm:grid-cols-2">
					{#each INITIALIZATION_SERVICE_GROUPS as group (group.label)}
						<fieldset class="min-w-0 rounded-md border px-3 pb-3">
							<legend class="px-1 text-sm font-medium text-muted-foreground">{group.label}</legend>

							<div class="flex flex-col gap-3 pt-1">
								{#each group.services as service (service.id)}
									<CustomCheckbox
										id={`initialization-service-${service.id}`}
										label={service.label}
										bind:checked={properties.services[service.id]}
										classes="flex items-center gap-2"
										disabled={initializing || verifying || initializationAccepted}
									></CustomCheckbox>
								{/each}
							</div>
						</fieldset>
					{/each}
				</div>
			</div>
		</div>

		{#if shownErrors.length > 0}
			<Alert.Root variant="destructive" role="alert">
				<Alert.Title>
					<div class="flex items-center gap-1">
						<span class="icon-[mdi--alert-circle-outline] h-4 w-4 shrink-0 text-red-600"></span>
						<span>Initialization needs attention</span>
					</div>
				</Alert.Title>
				<Alert.Description>
					<ul class="list-inside list-disc text-sm">
						{#each shownErrors as error, index (`${index}-${error}`)}
							<li>{error}</li>
						{/each}
					</ul>
				</Alert.Description>
			</Alert.Root>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button
					onclick={reviewInit}
					type="button"
					size="sm"
					disabled={initializing || verifying || reviewing}
				>
					{#if initializing}
						Initializing...
					{:else if reviewing}
						Checking host...
					{:else if verifying}
						Checking status...
					{:else if initializationAccepted}
						Check status
					{:else}
						Initialize
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	bind:open={reviewOpen}
	class="sm:max-w-2xl"
	title="Review changes to this host"
	description="Initializing with the selected services makes these changes to this host."
	confirmLabel="Initialize"
	actions={{
		onConfirm: startInit,
		onCancel: () => (reviewOpen = false)
	}}
>
	<HostChanges services={selectedServices} {hostState} />
</AlertDialog>
