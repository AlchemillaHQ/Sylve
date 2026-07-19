<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { mode } from 'mode-watcher';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { getPoolsResponse } from '$lib/api/zfs/pool';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Alert from '$lib/components/ui/alert/index.js';
	import { initialize } from '$lib/api/basic';
	import { toast } from 'svelte-sonner';
	import Button from '$lib/components/ui/button/button.svelte';
	import { resource } from 'runed';
	import type { AvailableService } from '$lib/types/system/settings';
	import { INITIALIZATION_SERVICES } from '$lib/system-services';
	import { getBasicHealth } from '$lib/api/system/system';
	import { isAPIResponse } from '$lib/utils/http';
	import type { InitializeResponse } from '$lib/types/basic';

	interface Props {
		initialized: boolean;
	}

	let { initialized = $bindable() }: Props = $props();

	const pools = resource(() => 'initialization-pools', async () => {
		const response = await getPoolsResponse(true);
		if (response.status !== 'success' || !Array.isArray(response.data)) {
			const error = Array.isArray(response.error)
				? response.error.join(', ')
				: response.error || response.message || 'Unable to load ZFS pools';
			throw new Error(error);
		}

		return response.data;
	});

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
		if (initializationAccepted) {
			await verifyInitialization();
			return;
		}

		initializing = true;
		try {
			const pools = properties.pools.combobox.values;
			const services = INITIALIZATION_SERVICES.filter(
				(service) => properties.services[service.id]
			).map((service) => service.id);

			const response = await initialize(pools, services);
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
				{:else if pools.error}
					<div class="flex items-center justify-between gap-3 text-sm text-destructive" role="alert">
						<span>Existing ZFS pools could not be loaded. You can continue without one.</span>
						<Button variant="outline" size="sm" onclick={() => void pools.refetch()}>Retry</Button>
					</div>
				{:else if pools.current?.length === 0}
					<p class="text-sm text-muted-foreground">
						No existing pools were found. You can continue and create one later in Sylve.
					</p>
				{/if}

				<Label class="text-sm font-medium text-gray-600 dark:text-gray-300">Services</Label>

				<div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
					{#each INITIALIZATION_SERVICES as service (service.id)}
						<CustomCheckbox
							id={`initialization-service-${service.id}`}
							label={service.label}
							bind:checked={properties.services[service.id]}
							classes="flex items-center gap-2"
							disabled={initializing || verifying || initializationAccepted}
						></CustomCheckbox>
					{/each}
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
						onclick={startInit}
						type="button"
						size="sm"
						disabled={initializing || verifying}
					>
						{#if initializing}
							Initializing...
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
