<script lang="ts">
	import '@fontsource/noto-sans';
	import '@fontsource/noto-sans/700.css';

	import { goto } from '$app/navigation';
	import { navigating } from '$app/stores';
	import { isTokenValid, login } from '$lib/api/auth';
	import Login from '$lib/components/custom/Login.svelte';
	import Shell from '$lib/components/Skeleton/Shell.svelte';
	import { store as token } from '$lib/stores/auth';
	import { hostname } from '$lib/stores/basic';
	import { QueryClient, QueryClientProvider } from '@sveltestack/svelte-query';
	import { ModeWatcher } from 'mode-watcher';
	import { onMount, tick } from 'svelte';
	import '../app.css';

	const queryClient = new QueryClient();
	let { children } = $props();
	let isLoggedIn = $state(false);
	let isLoading = $state(true);

	$effect(() => {
		if (isLoggedIn && $hostname && !$navigating) {
			const path = window.location.pathname;
			if (path === '/' || !path.startsWith(`/${$hostname}`)) {
				goto(`/${$hostname}/summary`, { replaceState: true });
			}
		}
	});

	onMount(async () => {
		if ($token) {
			await sleep(800);
			try {
				if (await isTokenValid()) {
					isLoggedIn = true;
				} else {
					$token = '';
				}
			} catch (error) {
				console.error('Token validation error:', error);
				$token = '';
			}
		}

		isLoading = false;
		await tick();
	});

	function sleep(ms: number) {
		return new Promise((resolve) => setTimeout(resolve, ms));
	}

	async function handleLogin(
		username: string,
		password: string,
		type: string,
		language: string,
		remember: boolean
	) {
		isLoading = true;
		try {
			if (await login(username, password, type, remember)) {
				isLoggedIn = true;
				const path = window.location.pathname;
				if (path === '/' || !path.startsWith(`/${$hostname}`)) {
					await goto(`/${$hostname}/summary`, { replaceState: true });
				}
			} else {
				alert('Login failed');
			}
		} catch (error) {
			console.error('Login error:', error);
			alert('Login failed: An error occurred');
		} finally {
			isLoading = false;
		}
		return;
	}
</script>

<ModeWatcher />

{#if isLoading}
	<div class="flex h-screen w-full items-center justify-center">
		<div
			class="apply h-10 w-10 animate-spin rounded-[50%] border-4 border-solid border-[rgba(0,0,0,0.1)] border-t-[#3498db]"
		></div>
	</div>
{:else if isLoggedIn && $hostname}
	<QueryClientProvider client={queryClient}>
		<Shell>
			{@render children()}
		</Shell>
	</QueryClientProvider>
{:else}
	<Login onLogin={handleLogin} />
{/if}

<style>
	@keyframes spin {
		0% {
			transform: rotate(0deg);
		}
		100% {
			transform: rotate(360deg);
		}
	}
</style>
