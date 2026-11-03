/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import z from 'zod/v4';

export const ONLY_CACHE = true;
export const SEVEN_DAYS = 7 * 24 * 60 * 60 * 1000;
export const ALWAYS_REFRESH = 0;

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

export function sleep(ms: number) {
	return new Promise((resolve) => setTimeout(resolve, ms));
}

export function getObjectSchemaDefaults<Schema extends z.ZodObject>(schema: Schema) {
	return Object.fromEntries(
		Object.entries(schema.shape).map(([key, value]) => {
			return [key, value.unwrap().def.defaultValue];
		})
	);
}

export function plural(
	num: number,
	candidates: unknown[],
	rule = (n: number) => (n === 1 ? 0 : 1)
) {
	const index = rule(num);
	return String(candidates[index]).replace('#', String(num));
}

export function cssVar(name: string): string {
	return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChild<T> = T extends { child?: any } ? Omit<T, 'child'> : T;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChildren<T> = T extends { children?: any } ? Omit<T, 'children'> : T;
export type WithoutChildrenOrChild<T> = WithoutChildren<WithoutChild<T>>;
export type WithElementRef<T, U extends HTMLElement = HTMLElement> = T & { ref?: U | null };
