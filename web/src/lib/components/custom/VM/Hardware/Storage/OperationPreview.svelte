<script lang="ts">
	import {
		emulationLabel,
		type OperationPreviewData,
		type StorageEmulation
	} from './storage-dialog';

	interface Props {
		preview: OperationPreviewData;
		icon: string;
		emulation: StorageEmulation;
		rid: number;
	}

	let { preview, icon, emulation, rid }: Props = $props();
</script>

<aside class="flex min-w-0 flex-col rounded-xl border bg-card p-4 shadow-sm lg:h-full">
	<div class="flex h-8 items-center justify-between gap-3">
		<p class="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
			Operation preview
		</p>
		<span
			class="flex size-8 items-center justify-center rounded-md border bg-muted/40 text-muted-foreground"
		>
			<span class={icon + ' size-4'}></span>
		</span>
	</div>

	<div class="mt-3 space-y-2">
		<div class="min-w-0 space-y-1">
			<p class="flex h-7 items-center text-sm font-medium">Source</p>
			<div class="flex h-9 min-w-0 items-center rounded-md border bg-background px-3">
				<p class="truncate text-sm font-medium" title={preview.source}>
					{preview.source}
				</p>
			</div>
		</div>
		<div class="flex h-5 items-center gap-2 px-1 text-muted-foreground">
			<span class="h-px flex-1 bg-border"></span>
			<span class="inline-flex items-center gap-1 text-[10px] font-medium">
				{preview.action}
				<span class="icon-[lucide--arrow-down] size-3.5"></span>
			</span>
			<span class="h-px flex-1 bg-border"></span>
		</div>
		<div class="min-w-0 space-y-1">
			<p class="flex h-7 items-center text-sm font-medium">Result</p>
			<div class="flex h-9 min-w-0 items-center rounded-md border bg-background px-3">
				<p class="truncate text-sm font-medium" title={preview.result}>
					{preview.result}
				</p>
			</div>
		</div>
	</div>

	<div class="mt-3 flex items-center gap-2 text-xs text-muted-foreground">
		<span class="h-px flex-1 bg-border"></span>
		<span class="icon-[lucide--cable] size-3.5"></span>
		<span>{emulationLabel(emulation)}</span>
		<span class="icon-[lucide--arrow-right] size-3.5"></span>
		<span class="font-medium text-foreground">VM {rid}</span>
		<span class="h-px flex-1 bg-border"></span>
	</div>

	<div class="mt-auto flex items-start gap-2.5 pt-4 text-xs leading-5 text-muted-foreground">
		<span
			class={preview.warning
				? 'icon-[lucide--triangle-alert] mt-0.5 size-4 shrink-0 text-amber-500'
				: 'icon-[lucide--shield-check] mt-0.5 size-4 shrink-0'}
		></span>
		<p>{preview.note}</p>
	</div>
</aside>
