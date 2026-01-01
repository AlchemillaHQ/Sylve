/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { subscribeToFocus, subscribeToOnline } from '$lib/runes/queryEvents';
import { onMount, onDestroy } from 'svelte';

type ErrorMessage = string | null;

export type QueryConfig<TData> = {
	key?: unknown;
	queryFn: () => Promise<TData>;
	manual?: boolean;
	initialData?: TData;
	refetchInterval?: number;
	refetchIntervalInBackground?: boolean;
	refetchOnWindowFocus?: boolean;
	refetchOnReconnect?: boolean;
	refetchOnMount?: boolean;
	onSuccess?: (data: TData) => void;
	onError?: (err: ErrorMessage) => void;
};

export type QueryResult<TData> = {
	readonly data: TData;
	readonly loading: boolean;
	readonly error: ErrorMessage;
	refetch: () => void;
};

type InferQueryResult<F> = F extends () => QueryConfig<infer T> & { initialData: T }
	? QueryResult<T>
	: F extends () => QueryConfig<infer T>
		? QueryResult<T | null>
		: never;

export function useQuery<TData>(
	getConfig: () => QueryConfig<TData> & { initialData: TData }
): QueryResult<TData>;
export function useQuery<TData>(getConfig: () => QueryConfig<TData>): QueryResult<TData | null>;
export function useQuery<TData>(getConfig: () => QueryConfig<TData>): QueryResult<TData | null> {
	const initialConfig = getConfig();

	let data = $state<TData | null>(
		initialConfig.initialData !== undefined ? initialConfig.initialData : null
	);
	let loading = $state<boolean>(initialConfig.initialData === undefined);
	let error = $state<ErrorMessage>(null);

	let currentRunId = 0;
	let intervalId: ReturnType<typeof setInterval> | null = null;
	let hasMounted = false;
	let unsubscribeFocus: (() => void) | null = null;
	let unsubscribeOnline: (() => void) | null = null;

	const setLoading = (isLoading = true) => {
		loading = isLoading;
		if (isLoading) error = null;
	};

	const runQuery = async () => {
		const config = getConfig();
		const runId = ++currentRunId;

		setLoading(true);

		try {
			const result = await config.queryFn();

			if (runId !== currentRunId) return;

			data = result;
			error = null;
			loading = false;

			config.onSuccess?.(result);
		} catch (e: any) {
			if (runId !== currentRunId) return;

			const message: ErrorMessage = e?.message ?? e?.errorMessage ?? 'Unexpected error occurred';

			error = message;
			loading = false;

			config.onError?.(message);
		}
	};

	const handleVisibilityOrFocus = () => {
		if (typeof document === 'undefined') return;
		const config = getConfig();

		const shouldRefetchOnFocus = config.refetchOnWindowFocus ?? true;
		if (!shouldRefetchOnFocus) return;

		if (document.visibilityState === 'visible') {
			void runQuery();
		}
	};

	const handleOnline = () => {
		const config = getConfig();

		const shouldRefetchOnReconnect = config.refetchOnReconnect ?? true;
		if (!shouldRefetchOnReconnect) return;

		if (typeof navigator === 'undefined' || !navigator.onLine) return;

		void runQuery();
	};

	$effect(() => {
		const config = getConfig();

		if (intervalId) {
			clearInterval(intervalId);
			intervalId = null;
		}

		if (config.refetchInterval) {
			const allowBackground = config.refetchIntervalInBackground ?? false;

			intervalId = setInterval(() => {
				if (!allowBackground && typeof document !== 'undefined') {
					if (document.visibilityState !== 'visible') return;
				}
				void runQuery();
			}, config.refetchInterval);
		}

		if (hasMounted) {
			const shouldRefetchOnMount = config.refetchOnMount ?? true;
			if (shouldRefetchOnMount) {
				void runQuery();
			}
		}
	});

	onMount(() => {
		hasMounted = true;

		const config = getConfig();
		const shouldRefetchOnMount = config.refetchOnMount ?? true;
		if (shouldRefetchOnMount) {
			void runQuery();
		}

		// subscribe this query instance to the shared listeners
		if (typeof window !== 'undefined' && typeof document !== 'undefined') {
			unsubscribeFocus = subscribeToFocus(handleVisibilityOrFocus);
			unsubscribeOnline = subscribeToOnline(handleOnline);
		}
	});

	onDestroy(() => {
		if (intervalId) clearInterval(intervalId);

		if (unsubscribeFocus) {
			unsubscribeFocus();
			unsubscribeFocus = null;
		}
		if (unsubscribeOnline) {
			unsubscribeOnline();
			unsubscribeOnline = null;
		}
	});

	return {
		get data() {
			return data;
		},
		get loading() {
			return loading;
		},
		get error() {
			return error;
		},
		refetch: () => {
			void runQuery();
		}
	};
}

export function useQueries<TFactories extends Record<string, () => QueryConfig<any>>>(
	getMany: () => TFactories
): {
	[K in keyof TFactories]: InferQueryResult<TFactories[K]>;
} & {
	refetchAll: () => void;
	refetch: (...keys: (keyof TFactories)[]) => void;
} {
	const factories = getMany();

	const queriesObj = {} as {
		[K in keyof TFactories]: InferQueryResult<TFactories[K]>;
	};

	for (const key in factories) {
		const factory = factories[key];
		queriesObj[key] = useQuery(factory as any) as any;
	}

	const refetchAll = () => {
		for (const key in queriesObj) {
			queriesObj[key].refetch();
		}
	};

	const refetch = (...keys: (keyof TFactories)[]) => {
		for (const key of keys) {
			queriesObj[key].refetch();
		}
	};

	return Object.assign(queriesObj, { refetchAll, refetch });
}
