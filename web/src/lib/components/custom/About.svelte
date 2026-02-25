<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { mode } from 'mode-watcher';

	interface Props {
		open: boolean;
	}

	let { open = $bindable() }: Props = $props();

	const sponsors = [
		{
			name: 'Alchemilla',
			url: 'https://alchemilla.io',
			light: '/logo/sponsors/Alchemilla-Dark.png',
			dark: '/logo/sponsors/Alchemilla-White.png'
		},
		{
			name: 'FreeBSD Foundation',
			url: 'https://freebsdfoundation.org',
			light: '/logo/sponsors/FreeBSD-Red.png',
			dark: '/logo/sponsors/FreeBSD-White.png'
		},
		{
			name: 'IPTechnics',
			url: 'https://iptechnics.com',
			light: '/logo/sponsors/IP-Technics-Dark.png',
			dark: '/logo/sponsors/IP-Technics-White.png'
		}
	];
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-md sm:max-w-lg p-0 overflow-hidden">
		<div class="relative flex items-center justify-center border-b bg-muted/10 p-5">
			<Dialog.Close
				class="absolute right-3 top-3 flex h-7 w-7 items-center justify-center rounded-full opacity-60 transition-all hover:bg-muted hover:opacity-100"
				onclick={() => (open = false)}
			>
				<span class="icon-[material-symbols--close-rounded] h-5 w-5"></span>
			</Dialog.Close>

			<div class="flex items-center space-x-2.5">
				<img
					src={mode.current === 'dark' ? '/logo/white.svg' : '/logo/black.svg'}
					alt="Sylve Logo"
					class="h-7 w-auto"
				/>
				<h1 class="text-xl font-light tracking-[.3em] uppercase">Sylve</h1>
			</div>
		</div>

		<div class="max-h-[80vh] overflow-y-auto px-5 pt-0 pb-6 space-y-5">
			<p class="text-sm leading-relaxed text-foreground/80 text-justify">
				Sylve is a lightweight control plane for the <strong>FreeBSD ecosystem</strong>, unifying
				Bhyve, Jails, ZFS, PF, and more into a streamlined management interface. Powered by a
				high-performance Go backend and a responsive SvelteKit frontend, it makes complex
				infrastructure simple.
			</p>

			<div class="space-y-3">
				<h3 class="text-[10px] font-bold uppercase tracking-widest text-muted-foreground/70">
					Sponsors
				</h3>

				<div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
					{#each sponsors as sponsor}
						<a
							href={sponsor.url}
							target="_blank"
							class="flex h-20 items-center justify-center rounded-md border bg-muted/5 p-3 transition-colors hover:bg-muted/20"
							title={sponsor.name}
						>
							<img
								src={mode.current === 'dark' ? sponsor.dark : sponsor.light}
								alt={sponsor.name}
								class="h-10 w-full object-contain"
							/>
						</a>
					{/each}

					<a
						href="https://github.com/sponsors/AlchemillaHQ"
						target="_blank"
						class="group flex h-20 flex-col items-center justify-center rounded-md border border-dashed border-primary/30 bg-primary/5 p-2 transition-all hover:border-primary/60 hover:bg-primary/10"
					>
						<span
							class="icon-[mdi--heart] text-lg text-pink-500 group-hover:scale-110 transition-transform"
						></span>
						<span class="mt-1 text-[9px] font-bold uppercase text-primary">Join us</span>
					</a>
				</div>
			</div>

			<div class="flex items-center justify-between border-t pt-4">
				<div class="flex flex-col space-y-0.5">
					<p class="text-[10px] text-muted-foreground">
						Released under the <a
							href="https://github.com/AlchemillaHQ/Sylve/blob/master/LICENSE"
							target="_blank"
							class="underline hover:text-primary transition-colors">BSD 2-Clause License</a
						>
					</p>
					<p class="text-[10px] text-muted-foreground">
						Support us on <a
							href="https://github.com/sponsors/AlchemillaHQ"
							target="_blank"
							class="underline hover:text-primary transition-colors">GitHub</a
						>
					</p>
				</div>
				<div class="flex items-center space-x-4">
					<a
						aria-label="GitHub"
						href="https://github.com/AlchemillaHQ/Sylve"
						target="_blank"
						class="text-muted-foreground hover:text-foreground transition-colors"
					>
						<span class="icon-[mdi--github] text-lg"></span>
					</a>
					<a
						aria-label="Discord"
						href="https://discord.gg/bJB826JvXK"
						target="_blank"
						class="text-muted-foreground hover:text-foreground transition-colors"
					>
						<span class="icon-[mdi--discord] text-lg"></span>
					</a>
				</div>
			</div>
		</div>
	</Dialog.Content>
</Dialog.Root>
