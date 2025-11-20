<script lang="ts">
	import { createQuery } from '@tanstack/svelte-query';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { mode } from 'mode-watcher';
	import ComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { getPools } from '$lib/api/zfs/pool';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import * as Alert from '$lib/components/ui/alert/index.js';
	import { initialize } from '$lib/api/basic';
	import { toast } from 'svelte-sonner';
	import Button from '$lib/components/ui/button/button.svelte';

	const results = createQuery(() => ({
		queryKey: ['pool-list'],
		queryFn: async () => {
			return getPools(true);
		},
		refetchOnMount: 'always',
		keepPreviousData: true
	}));

	let pools = $derived(results.data || []);
	let reload: boolean = $state(false);

	$effect(() => {
		if (reload) {
			results.refetch();
			reload = false;
		}
	});

	let options = {
		pools: {
			combobox: {
				open: false,
				values: [] as string[]
			}
		},
		services: {
			sambaServer: false,
			dhcpServer: true,
			virtualization: true,
			jails: true,
			wolServer: false
		}
	};

	let properties = $state(options);
	let shownErrors = $state([] as string[]);

	async function startInit() {
		const pools = properties.pools.combobox.values;
		const services = [];
		if (properties.services.virtualization) services.push('virtualization');
		if (properties.services.jails) services.push('jails');
		if (properties.services.sambaServer) services.push('sambaServer');
		if (properties.services.dhcpServer) services.push('dhcpServer');
		if (properties.services.wolServer) services.push('wolServer');

		const errors = await initialize(pools, services);
		if (errors.length === 0) {
			shownErrors = [];
			reload = true;
			toast.success('Sylve initialized', {
				position: 'bottom-center'
			});
		} else {
			shownErrors = errors;
		}
	}
</script>

<Dialog.Root open={true}>
	<Dialog.Content
		overlayClass="bg-background"
		class="bg-card text-card-foreground"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
	>
		<Dialog.Header>
			<div class="flex w-full items-center justify-between">
				<Dialog.Title class="flex-1 text-center">
					<div class="flex items-center justify-center space-x-2">
						{#if mode.current === 'dark'}
							<img src="/logo/white.svg" alt="Sylve Logo" class="h-8 w-auto max-w-[100px]" />
						{:else}
							<img src="/logo/black.svg" alt="Sylve Logo" class="h-8 w-auto max-w-[100px]" />
						{/if}
						<p class="font-normal tracking-[.45em]">SYLVE</p>
					</div>
				</Dialog.Title>
			</div>
		</Dialog.Header>

		<div class="flex flex-col gap-4">
			<ComboBox
				bind:open={properties.pools.combobox.open}
				label={'ZFS Storage Pools'}
				bind:value={properties.pools.combobox.values}
				data={generateComboboxOptions(pools.map((p) => p.name))}
				classes="flex-1 space-y-3"
				placeholder="Select Pools"
				width="w-full"
				multiple={true}
			></ComboBox>

			<Label class="text-sm font-medium text-gray-600 dark:text-gray-300">Services</Label>

			<div class="grid grid-cols-3 gap-2">
				<CustomCheckbox
					label="Virtualization"
					bind:checked={properties.services.virtualization}
					classes="flex items-center gap-2"
				></CustomCheckbox>
				<CustomCheckbox
					label="Jails"
					bind:checked={properties.services.jails}
					classes="flex items-center gap-2"
				></CustomCheckbox>
				<CustomCheckbox
					label="Samba Server"
					bind:checked={properties.services.sambaServer}
					classes="flex items-center gap-2"
				></CustomCheckbox>
				<CustomCheckbox
					label="DHCP Server"
					bind:checked={properties.services.dhcpServer}
					classes="flex items-center gap-2"
				></CustomCheckbox>
				<CustomCheckbox
					label="WOL Server"
					bind:checked={properties.services.wolServer}
					classes="flex items-center gap-2"
				></CustomCheckbox>
			</div>
		</div>

		{#if shownErrors.length > 0}
			<Alert.Root variant="destructive">
				<span class="icon-[mdi--alert-circle-outline] h-5 w-5 flex-shrink-0 text-red-600"></span>
				<Alert.Title>We've hit the following errors during initialization</Alert.Title>
				<Alert.Description>
					<ul class="list-inside list-disc text-sm">
						{#each shownErrors as error}
							<li>{error}</li>
						{/each}
					</ul>
				</Alert.Description>
			</Alert.Root>
		{/if}

		<Dialog.Footer class="flex justify-end">
			<div class="flex w-full items-center justify-end gap-2">
				<Button onclick={startInit} type="submit" size="sm">{'Initialize'}</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
