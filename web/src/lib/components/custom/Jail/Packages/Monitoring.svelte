<script lang="ts">
	import { setPackageMonitoring } from '$lib/api/jail/packages';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import Combobox from '$lib/components/ui/custom-input/combobox.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { PackageMonitoring } from '$lib/types/jail/packages';
	import { packageReason } from '$lib/types/jail/packages';
	import { isAPIResponse, isRequestCancellation } from '$lib/utils/http';
	import { convertDbTime, cronToHuman } from '$lib/utils/time';
	import { onDestroy, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	let {
		open = $bindable(),
		node,
		ctId,
		monitoring,
		afterChange
	}: {
		open: boolean;
		node: string;
		ctId: number;
		monitoring: PackageMonitoring;
		afterChange: () => void;
	} = $props();
	let frequency = $state(
		untrack(() =>
			monitoring.enabled ? monitoring.cronExpr || String(monitoring.intervalHours) : 'never'
		)
	);
	let frequencyOpen = $state(false);
	const presets = [
		{ label: 'Never', value: 'never' },
		{ label: 'Every 6 hours', value: '6' },
		{ label: 'Every 12 hours', value: '12' },
		{ label: 'Daily', value: '24' }
	];
	const customCron = $derived(!presets.some((preset) => preset.value === frequency));
	const cronDescription = $derived(customCron ? cronToHuman(frequency.trim()) : '');
	const invalidFrequency = $derived(
		customCron &&
			(frequency.length > 256 || frequency.trim().split(/\s+/).length !== 5 || !cronDescription)
	);
	let saving = $state(false);
	const controller = new AbortController();
	onDestroy(() => controller.abort());
	const date = (value: string | null) => (value ? convertDbTime(value) : 'Never');
	async function save() {
		if (saving || invalidFrequency) return;
		saving = true;
		try {
			const result = await setPackageMonitoring(
				ctId,
				frequency !== 'never',
				frequency === 'never' || customCron ? monitoring.intervalHours : Number(frequency),
				frequency === 'never' ? monitoring.cronExpr : customCron ? frequency.trim() : '',
				{
					hostname: node,
					signal: controller.signal
				}
			);
			if (controller.signal.aborted) return;
			if (isAPIResponse(result)) {
				toast.error(packageReason(result.message, 'Monitoring settings could not be saved.'), {
					position: 'bottom-center'
				});
				return;
			}
			toast.success('Package monitoring saved', { position: 'bottom-center' });
			afterChange();
			open = false;
		} catch (cause) {
			if (!isRequestCancellation(cause)) throw cause;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-md"
		controlsClass="top-5"
		aria-describedby={undefined}
	>
		<Dialog.Header class="border-b px-6 py-5 pr-12 text-left">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--clock-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Package Monitoring"
				/>
			</Dialog.Title>
		</Dialog.Header>
		<div class="min-h-0 space-y-5 overflow-y-auto px-6 py-5 text-sm">
			<div class="space-y-4">
				<Combobox
					label="Frequency"
					bind:open={frequencyOpen}
					bind:value={frequency}
					data={presets}
					placeholder="Select or enter a cron expression"
					hint="Custom cron uses the node's local time: minute hour day month weekday (0–6, Sunday = 0)."
					allowCustom={true}
					disallowEmpty={true}
					width="w-full"
					disabled={saving}
				/>
				{#if customCron}<p
						class="text-xs"
						class:text-destructive={invalidFrequency}
						role={invalidFrequency ? 'alert' : 'status'}
					>
						{invalidFrequency
							? 'Enter a valid five-field cron expression.'
							: `${cronDescription} (node local time)`}
					</p>{/if}
				<p class="text-muted-foreground text-xs leading-relaxed">
					Audits can run while the jail is stopped if its filesystem is mounted. Packages are never
					upgraded automatically.
				</p>
			</div>
			<section class="space-y-3 border-t pt-5">
				<h3 class="font-medium">Recent activity</h3>
				<dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-xs">
					<dt class="text-muted-foreground">Last successful audit</dt>
					<dd class="text-right">{date(monitoring.lastSuccessAt)}</dd>
					<dt class="text-muted-foreground">Last attempt</dt>
					<dd class="text-right">{date(monitoring.lastAttemptAt)}</dd>
					<dt class="text-muted-foreground">Next audit</dt>
					<dd class="text-right">
						{monitoring.enabled ? date(monitoring.nextAuditAt) : 'Not scheduled'}
					</dd>
					{#if monitoring.failureCount > 0}
						<dt class="text-muted-foreground">Consecutive failures</dt>
						<dd class="text-right">{monitoring.failureCount}</dd>
					{/if}
					{#if monitoring.skipReason}
						<dt class="text-muted-foreground">Last skipped</dt>
						<dd class="text-right">{packageReason(monitoring.skipReason)}</dd>
					{/if}
				</dl>
			</section>
		</div>
		<Dialog.Footer class="border-t px-6 py-4">
			<Button onclick={save} disabled={saving || invalidFrequency}
				>{saving ? 'Saving...' : 'Save'}</Button
			>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
