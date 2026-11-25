// See https://svelte.dev/docs/kit/types#app.d.ts
// for information about these interfaces
declare global {
	const __SYLVE_DEMO__: boolean;

	namespace App {
		// interface Error {}
		// interface Locals {}
		// interface PageData {}
		interface PageState {
			explorerPath?: string;
			loggedOut?: boolean;
		}
		// interface Platform {}
	}
}

declare module '@fontsource/noto-sans';
declare module '@fontsource/noto-sans/700.css';

export {};
