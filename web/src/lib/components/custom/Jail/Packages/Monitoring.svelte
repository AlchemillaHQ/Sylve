<script lang="ts">
	import { setPackageMonitoring } from '$lib/api/jail/packages';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import type { PackageMonitoring } from '$lib/types/jail/packages';
	import { packageReason } from '$lib/types/jail/packages';
	import { isAPIResponse } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
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
	let interval = $state(
		untrack(() => (monitoring.enabled ? String(monitoring.intervalHours) : 'never'))
	);
	let saving = $state(false);
	const controller = new AbortController();
	onDestroy(() => controller.abort());
	const date = (value: string | null) => (value ? convertDbTime(value) : 'Never');
	async function save() {
		if (saving) return;
		saving = true;
		try {
			const result = await setPackageMonitoring(
				ctId,
				interval !== 'never',
				interval === 'never' ? monitoring.intervalHours : Number(interval),
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
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-md"
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
				<SimpleSelect
					label="Frequency"
					value={interval}
					onChange={(value) => (interval = value)}
					disabled={saving}
					options={[
						{ label: 'Never', value: 'never' },
						{ label: 'Every 6 hours', value: '6' },
						{ label: 'Every 12 hours', value: '12' },
						{ label: 'Daily', value: '24' }
					]}
				/>
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
			<Button onclick={save} disabled={saving}>{saving ? 'Saving...' : 'Save'}</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
