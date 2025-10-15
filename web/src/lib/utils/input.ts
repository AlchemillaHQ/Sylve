/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { SwitchList } from '$lib/types/network/switch';

export function generateComboboxOptions(values: string[], additional?: string[]) {
	const combined = [...values, ...(additional ?? [])];
	return combined
		.map((option) => ({
			label: option,
			value: option
		}))
		.filter((option, index, self) => self.findIndex((o) => o.value === option.value) === index);
}

export function generateSwitchOptions(
	switches: SwitchList,
	ignoreIds?: string[]
): { label: string; value: string }[] {
	const standardSwitches = (switches.standard ?? []).map((sw) => ({
		label: sw.name,
		value: `${sw.id}-stan-${sw.name}`
	}));

	const managedSwitches = (switches.manual ?? []).map((sw) => ({
		label: sw.name,
		value: `${sw.id}-man-${sw.name}`
	}));

	const filteredStandardSwitches = ignoreIds
		? standardSwitches.filter((sw) => {
				const id = parseInt(sw.value.split('-')[0]);
				// ignoreIds are like <id>-stan-<name> so check against that
				// return !ignoreIds.includes(id);
				return !ignoreIds.includes(sw.value);
			})
		: standardSwitches;

	const filteredManagedSwitches = ignoreIds
		? managedSwitches.filter((sw) => {
				const id = parseInt(sw.value.split('-')[0]);
				// return !ignoreIds.includes(id);
				return !ignoreIds.includes(sw.value);
			})
		: managedSwitches;

	return [...filteredStandardSwitches, ...filteredManagedSwitches];
}
