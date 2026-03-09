<script lang="ts">
	import { page } from '$app/state';
	import { revokeJWT } from '$lib/api/auth';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Card from '$lib/components/ui/card/index.js';
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { mode } from 'mode-watcher';
	import { onDestroy, onMount } from 'svelte';
	import { languageArr, storage } from '$lib';
	import { loadLocale } from 'wuchale/load-utils';
	import type { Locales } from '$lib/types/common';

	interface Props {
		onLogin: (
			username: string,
			password: string,
			type: string,
			remember: boolean,
			toLoginPath: string
		) => void;
		onPasskeyLogin: (remember: boolean, toLoginPath: string) => void;
		loading: boolean;
		loadingPasskey: boolean;
	}

	let toLoginPath = $derived(page.url.pathname);
	let {
		onLogin,
		onPasskeyLogin,
		loading = $bindable(),
		loadingPasskey = $bindable()
	}: Props = $props();

	let username = $state('');
	let password = $state('');
	let language = $derived(storage.language ?? 'en');
	let authType = $state('sylve');
	let remember = $state(false);

	$effect(() => {
		if (language) {
			loadLocale((language || 'en') as Locales);
			storage.language = language;
		}
	});

	$effect(() => {
		if (page.url.search.includes('loggedOut')) {
			revokeJWT();
		}
	});

	async function handleKeydown(event: KeyboardEvent) {
		if (event.key === 'Enter') {
			event.preventDefault();
			try {
				onLogin(username, password, authType, remember, toLoginPath);
			} catch (error) {
				console.error('Login error:', error);
			}
		}
	}

	onMount(() => {
		window.addEventListener('keydown', handleKeydown);
	});

	onDestroy(() => {
		window.removeEventListener('keydown', handleKeydown);
	});
</script>

<div class="fixed inset-0 flex items-center justify-center px-3">
	<Card.Root class="w-full max-w-lg rounded-lg shadow-lg">
		<Card.Header class="flex flex-row items-center justify-center gap-2">
			{#if mode.current === 'dark'}
				<img src="/logo/white.svg" alt="Sylve Logo" class="mt-2 h-8 w-auto" />
			{:else}
				<img src="/logo/black.svg" alt="Sylve Logo" class="h-8 w-auto" />
			{/if}
			<!-- @wc-ignore -->
			<p class="ml-2 text-xl font-medium tracking-[.45em] text-gray-800 dark:text-white">SYLVE</p>
		</Card.Header>

		<Card.Content class="space-y-4 p-6">
			<div class="flex items-center gap-2">
				<Label for="username" class="w-44">Username</Label>
				<Input
					id="username"
					class="h-8 w-full"
					type="text"
					placeholder="Enter your username"
					bind:value={username}
					autocomplete="off"
					required
				/>
			</div>
			<div class="flex items-center gap-2">
				<Label for="password" class="w-44">Password</Label>
				<Input
					id="password"
					type="password"
					placeholder="●●●●●●●●"
					autocomplete="off"
					class="h-8 w-full"
					bind:value={password}
					required
				/>
			</div>

			<div class="flex items-center gap-2">
				<Label for="realm" class="w-44">Realm</Label>
				<Select.Root type="single" bind:value={authType}>
					<Select.Trigger class="h-8 w-full">
						{#if authType === 'pam'}
							PAM
						{:else if authType === 'sylve'}
							Sylve
						{/if}
					</Select.Trigger>
					<Select.Content>
						<Select.Item value="pam">PAM</Select.Item>
						<Select.Item value="sylve">Sylve</Select.Item>
					</Select.Content>
				</Select.Root>
			</div>

			<!-- @wc-ignore -->
			<div class="flex items-center gap-2" title="Language selection is disabled for now">
				<Label for="language" class="w-44">Language</Label>
				<Select.Root type="single" bind:value={language}>
					<Select.Trigger class="h-8 w-full">
						{languageArr.find((lang) => lang.value === language)?.label || 'Select Language'}
					</Select.Trigger>
					<Select.Content>
						<Select.Item value="zh-CN">Simplified Chinese (简体中文)</Select.Item>
						<Select.Item value="en">English</Select.Item>
						<Select.Item value="hi">Hindi (हिन्दी)</Select.Item>
						<Select.Item value="mal">Malayalam (മലയാളം)</Select.Item>
					</Select.Content>
				</Select.Root>
			</div>
		</Card.Content>

		<Card.Footer class="flex items-center justify-between">
			<div class="flex items-center space-x-2">
				<Checkbox id="remember" bind:checked={remember} />
				<Label for="remember" class="text-sm font-medium">Remember Me</Label>
			</div>
			<div class="flex items-center gap-2">
				<Button
					onclick={() => {
						onPasskeyLogin(remember, toLoginPath);
					}}
					size="sm"
					variant="outline"
					class="rounded-md"
				>
					{#if loadingPasskey}
						<span class="apple-fp" aria-hidden="true">
							<svg class="apple-fp-svg" viewBox="0 0 24 24" fill="none" role="presentation">
								<g class="fp-ghost">
									<path d="M12 3.4C8.4 3.4 5.5 6.3 5.5 9.9V12" />
									<path d="M12 5.7c2.4 0 4.3 1.9 4.3 4.3v2" />
									<path d="M8.4 9.9v2.5c0 2 1.6 3.6 3.6 3.6s3.6-1.6 3.6-3.6V9.9" />
									<path d="M4 11.4v.8c0 4.4 3.6 8 8 8s8-3.6 8-8v-.8" />
									<path d="M2.8 11.2v1c0 5 4.1 9.1 9.2 9.1s9.2-4.1 9.2-9.1v-1" />
								</g>
								<g class="fp-active">
									<path
										class="fp-stroke fp-fwd"
										style="--d: 0ms"
										pathLength="100"
										d="M12 3.4C8.4 3.4 5.5 6.3 5.5 9.9V12"
									/>
									<path
										class="fp-stroke fp-rev"
										style="--d: 70ms"
										pathLength="100"
										d="M12 5.7c2.4 0 4.3 1.9 4.3 4.3v2"
									/>
									<path
										class="fp-stroke fp-fwd"
										style="--d: 140ms"
										pathLength="100"
										d="M8.4 9.9v2.5c0 2 1.6 3.6 3.6 3.6s3.6-1.6 3.6-3.6V9.9"
									/>
									<path
										class="fp-stroke fp-rev"
										style="--d: 210ms"
										pathLength="100"
										d="M4 11.4v.8c0 4.4 3.6 8 8 8s8-3.6 8-8v-.8"
									/>
									<path
										class="fp-stroke fp-fwd"
										style="--d: 280ms"
										pathLength="100"
										d="M2.8 11.2v1c0 5 4.1 9.1 9.2 9.1s9.2-4.1 9.2-9.1v-1"
									/>
								</g>
							</svg>
							<span class="apple-fp-sheen"></span>
						</span>
					{:else}
						<span class="icon-[mdi--fingerprint] mr-1 h-4 w-4"></span>
						Passkey
					{/if}
				</Button>

				<Button
					onclick={() => {
						onLogin(username, password, authType, remember, toLoginPath);
					}}
					size="sm"
					class="w-20 rounded-md bg-blue-700 text-white hover:bg-blue-600"
				>
					{#if loading}
						<span class="icon-[line-md--loading-loop] h-4 w-4"></span>
					{:else}
						Login
					{/if}
				</Button>
			</div>
		</Card.Footer>
	</Card.Root>
</div>

<style>
	.apple-fp {
		position: relative;
		display: inline-flex;
		height: 1.05rem;
		width: 1.05rem;
		align-items: center;
		justify-content: center;
		margin-right: 0.42rem;
		overflow: hidden;
		border-radius: 9999px;
	}

	.apple-fp-svg {
		height: 100%;
		width: 100%;
	}

	.fp-ghost path {
		stroke: currentColor;
		stroke-width: 1.45;
		opacity: 0.2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.fp-stroke {
		stroke: currentColor;
		stroke-width: 1.45;
		stroke-linecap: round;
		stroke-linejoin: round;
		stroke-dasharray: 100;
		stroke-dashoffset: 100;
		opacity: 0;
		animation-duration: 1.95s;
		animation-timing-function: cubic-bezier(0.42, 0, 0.2, 1);
		animation-iteration-count: infinite;
		animation-delay: var(--d);
	}

	.fp-fwd {
		animation-name: fp-fill-erase-fwd;
	}

	.fp-rev {
		animation-name: fp-fill-erase-rev;
	}

	.apple-fp-sheen {
		position: absolute;
		inset: -30% -40%;
		background: linear-gradient(
			120deg,
			rgba(255, 255, 255, 0) 38%,
			rgba(255, 255, 255, 0.85) 50%,
			rgba(255, 255, 255, 0) 62%
		);
		mix-blend-mode: screen;
		opacity: 0;
		transform: translateX(-150%) rotate(10deg);
		animation: fp-sheen 1.95s cubic-bezier(0.42, 0, 0.2, 1) infinite;
	}

	@keyframes fp-fill-erase-fwd {
		0%,
		12% {
			opacity: 0;
			stroke-dashoffset: 100;
		}

		36% {
			opacity: 0.98;
			stroke-dashoffset: 0;
		}

		58% {
			opacity: 0.92;
			stroke-dashoffset: 0;
		}

		88% {
			opacity: 0;
			stroke-dashoffset: -100;
		}

		100% {
			opacity: 0;
			stroke-dashoffset: -100;
		}
	}

	@keyframes fp-fill-erase-rev {
		0%,
		12% {
			opacity: 0;
			stroke-dashoffset: -100;
		}

		36% {
			opacity: 0.98;
			stroke-dashoffset: 0;
		}

		58% {
			opacity: 0.92;
			stroke-dashoffset: 0;
		}

		88% {
			opacity: 0;
			stroke-dashoffset: 100;
		}

		100% {
			opacity: 0;
			stroke-dashoffset: 100;
		}
	}

	@keyframes fp-sheen {
		0%,
		42% {
			opacity: 0;
			transform: translateX(-150%) rotate(10deg);
		}

		48% {
			opacity: 0.35;
		}

		64% {
			opacity: 0.42;
			transform: translateX(28%) rotate(10deg);
		}

		100% {
			opacity: 0;
			transform: translateX(180%) rotate(10deg);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.fp-stroke,
		.apple-fp-sheen {
			animation: none;
			opacity: 0.9;
			stroke-dashoffset: 0;
		}
	}
</style>
