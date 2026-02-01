<script>
	import { getJWTClaims, logOut } from '$lib/api/auth';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import { mode, toggleMode } from 'mode-watcher';
	import CreateJail from './Jail/Create/CreateJail.svelte';
	import CreateVM from './VM/Create/CreateVM.svelte';
	import { storage, languageArr } from '$lib';
	import { loadLocale } from 'wuchale/load-utils';

	let options = {
		createVM: {
			open: false,
			minimize: false
		},
		createJail: {
			open: false,
			minimize: false
		},
		menuItems: [{ icon: 'mdi--palette', label: 'Color Theme', shortcut: '⌘⇧T' }]
	};

	let properties = $state(options);
	let jwt = $state(getJWTClaims());
</script>

<header class="sticky top-0 flex h-[5vh] items-center gap-4 border-x border-b px-2 md:h-[4vh]">
	<nav
		class="hidden flex-col gap-2 text-lg font-medium md:items-center md:gap-2 md:text-sm lg:flex lg:flex-row lg:gap-4"
	>
		<div class="flex items-center space-x-2">
			{#if mode.current === 'dark'}
				<img src="/logo/white.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
			{:else}
				<img src="/logo/black.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
			{/if}
			<!-- @wc-ignore -->
			<p class="font-normal tracking-[.45em]">SYLVE</p>
		</div>
	</nav>
	<Sheet.Root>
		<Sheet.Trigger>
			<Button variant="outline" size="icon" class="h-7 shrink-0 lg:hidden">
				<span class="icon-[material-symbols--menu-rounded] h-6 w-6"></span>
				<span class="sr-only">Toggle navigation menu</span>
			</Button>
		</Sheet.Trigger>
		<Sheet.Content side="left">
			<!-- mobile view -->
			<nav class="flex flex-col text-lg font-medium">
				<div class="mt-4 flex items-center space-x-2">
					<img src="/logo/white.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
					<!-- @wc-ignore -->
					<p class="font-normal tracking-[.45em]">SYLVE</p>
				</div>
				<p class="mt-4 whitespace-nowrap">Virtual Environment 0.0.1</p>
			</nav>
		</Sheet.Content>
	</Sheet.Root>
	<div class="flex w-full items-center justify-end gap-2 md:ml-auto">
		<div class="mr-2 hidden items-center gap-4 lg:inline-flex">
			{#if storage.enabledServices?.includes('virtualization')}
				<Button
					class="relative h-6"
					size="sm"
					onclick={() => {
						properties.createVM.open = true;
						properties.createVM.minimize = false;
					}}
				>
					<div class="flex items-center gap-2">
						<span class="icon-[material-symbols--monitor-outline-rounded] h-4 w-4"></span>
						<span>Create VM</span>
					</div>

					{#if properties.createVM.minimize}
						<span
							class="absolute -right-1 -top-0.5 h-3 w-3 rounded-full border border-white bg-yellow-400"
							title="VM creation form minimized"
						></span>
					{/if}
				</Button>
			{/if}

			{#if storage.enabledServices?.includes('jails')}
				<Button
					class="relative h-6"
					size="sm"
					onclick={() => {
						properties.createJail.open = true;
						properties.createJail.minimize = false;
					}}
				>
					<div class="flex items-center gap-2">
						<span class="icon-[hugeicons--prison] h-4 w-4"></span>
						<span>Create Jail</span>
					</div>

					{#if properties.createJail.minimize}
						<span
							class="absolute -right-1 -top-0.5 h-3 w-3 rounded-full border border-white bg-yellow-400"
							title="Jail creation form minimized"
						></span>
					{/if}
				</Button>
			{/if}

			{#if properties.createVM.open || properties.createVM.minimize}
				<CreateVM
					bind:open={properties.createVM.open}
					bind:minimize={properties.createVM.minimize}
				/>
			{/if}

			{#if properties.createJail.open || properties.createJail.minimize}
				<CreateJail
					bind:open={properties.createJail.open}
					bind:minimize={properties.createJail.minimize}
				/>
			{/if}
		</div>
		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				<Button variant="outline" size="sm" class="h-6.5">
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--user] h-4 w-4"></span>

						<span>{jwt?.custom_claims.username}</span>
						<span class="icon-[famicons--chevron-down] h-4 w-4"></span>
					</div>
					<span class="sr-only">Toggle user menu</span></Button
				>
			</DropdownMenu.Trigger>
			<DropdownMenu.Content class="w-56">
				<DropdownMenu.Group>
					{#each properties.menuItems as { icon, label, shortcut }}
						<DropdownMenu.Item class="cursor-pointer" onclick={toggleMode}>
							<span class="icon-[{icon}] mr-2 h-4 w-4"></span>

							<span>{label}</span>
							{#if shortcut}
								<DropdownMenu.Shortcut>{shortcut}</DropdownMenu.Shortcut>
							{/if}
						</DropdownMenu.Item>
					{/each}

					<DropdownMenu.Sub>
						<DropdownMenu.Root>
							<DropdownMenu.Trigger
								class=" hover:bg-accent h-6.5 flex w-full cursor-pointer items-center justify-start px-2.5 py-4 text-left"
							>
								<span class="icon-[meteor-icons--language] mr-4 h-4 w-4"></span>
								Language
							</DropdownMenu.Trigger>
							<DropdownMenu.Content class="w-48">
								<DropdownMenu.Group>
									{#each languageArr as { value, label }}
										<DropdownMenu.CheckboxItem
											class="cursor-pointer"
											checked={storage.language === value}
											onclick={() => {
												storage.language = value;
												loadLocale(value);
											}}
										>
											{label}
										</DropdownMenu.CheckboxItem>
									{/each}
								</DropdownMenu.Group>
							</DropdownMenu.Content>
						</DropdownMenu.Root></DropdownMenu.Sub
					>
				</DropdownMenu.Group>

				<DropdownMenu.Separator />
				<DropdownMenu.Item class="cursor-pointer" onclick={() => logOut()}>
					<span class="icon-[ic--twotone-logout] mr-2 h-4 w-4"></span>
					<span>Log Out</span>
					<DropdownMenu.Shortcut>⌘⇧Q</DropdownMenu.Shortcut>
				</DropdownMenu.Item>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>
</header>
