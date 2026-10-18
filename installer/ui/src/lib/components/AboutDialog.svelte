<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog';
	import { buttonVariants } from '$lib/components/ui/button';
	import Heart from '@lucide/svelte/icons/heart';
	import X from '@lucide/svelte/icons/x';
	import { cn } from '$lib/utils';

	let { dark = false }: { dark?: boolean } = $props();

	const sponsors = [
		{
			name: 'Alchemilla',
			light: '/logo/sponsors/Alchemilla-Dark.webp',
			dark: '/logo/sponsors/Alchemilla-White.webp'
		},
		{
			name: 'FreeBSD Foundation',
			light: '/logo/sponsors/FreeBSD-Red.webp',
			dark: '/logo/sponsors/FreeBSD-White.webp'
		},
		{
			name: 'IP Technics',
			light: '/logo/sponsors/IP-Technics-Dark.webp',
			dark: '/logo/sponsors/IP-Technics-White.webp'
		},
		{
			name: 'Zelta',
			light: '/logo/sponsors/Zelta-BlackYellow.svg',
			dark: '/logo/sponsors/Zelta-BlackWhite.svg',
			imageClass: 'h-10 w-full max-w-28 scale-125 rounded-sm object-cover brightness-0 dark:brightness-100'
		}
	];
</script>

<Dialog.Root>
	<Dialog.Trigger
		class={cn(
			buttonVariants({ variant: 'outline', size: 'icon' }),
			'h-[22px] w-[22px] rounded-md [&_svg]:size-3.5'
		)}
		aria-label="About"
		title="About"
	>
		<Heart class="size-3.5" />
	</Dialog.Trigger>

	<Dialog.Content class="max-w-md overflow-hidden p-0 sm:max-w-lg" showCloseButton={false}>
		<div class="relative flex items-center justify-center border-b bg-muted/10 p-6">
			<div class="flex items-center space-x-2.5">
				<img
					src={dark ? '/logo/freebsd.svg' : '/logo/freebsd-black.svg'}
					alt="FreeBSD"
					class="h-7 w-auto"
				/>
				<span class="text-xl font-light text-muted-foreground">+</span>
				<img src={dark ? '/logo/white.svg' : '/logo/black.svg'} alt="Sylve" class="h-7 w-auto" />
			</div>
			<Dialog.Close
				class="absolute top-7 right-6 text-muted-foreground transition-colors hover:text-foreground focus:outline-none disabled:pointer-events-none"
			>
				<X class="h-5 w-5" />
				<span class="sr-only">Close</span>
			</Dialog.Close>
		</div>

		<div class="max-h-[80vh] space-y-5 overflow-y-auto px-6 pt-5 pb-6">
			<p class="text-sm leading-relaxed text-foreground/80 text-justify">
				Sylve is a lightweight control plane for the <strong>FreeBSD ecosystem</strong>, unifying
				Bhyve, Jails, ZFS, Networking, and more into a streamlined management interface. Powered by
				a high-performance Go backend and a responsive SvelteKit frontend, it makes complex
				infrastructure simple.
			</p>

			<div class="space-y-3">
				<h3 class="text-[10px] font-bold tracking-widest text-muted-foreground/70 uppercase">
					Sponsors &amp; Technologies
				</h3>
				<div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
					{#each sponsors as sponsor (sponsor.name)}
						<div
							class="flex h-20 items-center justify-center rounded-md border bg-muted/5 p-3"
							title={sponsor.name}
						>
							<img
								src={dark ? sponsor.dark : sponsor.light}
								alt={sponsor.name}
								class={sponsor.imageClass ?? 'h-10 w-full scale-110 object-contain'}
							/>
						</div>
					{/each}
				</div>
			</div>

			<p class="border-t pt-3 text-center text-[10px] leading-relaxed text-muted-foreground">
				Released under the BSD 2-Clause License · © 2026 The FreeBSD Foundation.
			</p>
		</div>
	</Dialog.Content>
</Dialog.Root>
