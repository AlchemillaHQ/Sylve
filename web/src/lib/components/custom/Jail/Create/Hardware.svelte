<script lang="ts">
	import { getCPUInfo } from '$lib/api/info/cpu';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import humanFormat from 'human-format';
	import { resource } from 'runed';

	interface Props {
		cpuCores: number;
		ram: number;
		startAtBoot: boolean;
		bootOrder: number;
		resourceLimits: boolean;
		devfsRuleset: string;
	}

	let {
		cpuCores = $bindable(),
		ram = $bindable(),
		startAtBoot = $bindable(),
		bootOrder = $bindable(),
		resourceLimits = $bindable(),
		devfsRuleset = $bindable()
	}: Props = $props();

	let humanSize = $state('1024 M');
	let cpuInfo = resource(
		() => 'cpu-info',
		async () => {
			const result = await getCPUInfo('current');
			return result;
		}
	);

	$effect(() => {
		if (cpuCores && cpuInfo.current) {
			if (cpuCores > cpuInfo.current.logicalCores) {
				cpuCores = cpuInfo.current.logicalCores - 1;
			}
		}

		try {
			const p = humanFormat.parse.raw(humanSize);
			ram = p.factor * p.value;
		} catch {
			ram = 1024;
		}
	});

	let customDevfsRuleset = $state(false);
</script>

<div class="flex flex-col gap-4 p-4">
	<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
		<CustomValueInput
			label="CPU Cores"
			placeholder="1"
			type="number"
			bind:value={cpuCores}
			classes="flex-1 space-y-1.5"
			disabled={!resourceLimits}
		/>

		<CustomValueInput
			label="Memory Size"
			placeholder="10G"
			bind:value={humanSize}
			classes="flex-1 space-y-1.5"
			disabled={!resourceLimits}
		/>

		<CustomValueInput
			label="Boot Order"
			placeholder="1"
			type="number"
			bind:value={bootOrder}
			classes="flex-1 space-y-1.5"
		/>
	</div>

	<div class="mt-2 flex flex-row gap-2">
		<CustomCheckbox
			label="Start On Boot"
			bind:checked={startAtBoot}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Resource Limits"
			bind:checked={resourceLimits}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Custom Devfs Ruleset"
			bind:checked={customDevfsRuleset}
			classes="flex items-center gap-2"
		></CustomCheckbox>
	</div>

	{#if customDevfsRuleset}
		<CustomValueInput
			label="Devfs Ruleset"
			placeholder="Leave empty for default ruleset"
			bind:value={devfsRuleset}
			classes="flex-1 space-y-1.5"
			disabled={!customDevfsRuleset}
			type="textarea"
		/>
	{/if}
</div>
