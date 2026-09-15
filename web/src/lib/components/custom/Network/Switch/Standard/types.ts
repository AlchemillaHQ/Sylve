/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { VLANPolicyDraft } from '$lib/utils/network/switch/standard';

export type SwitchTab = 'general' | 'ports' | 'ipv4' | 'ipv6';

export interface StandardSwitchFormState {
	open: boolean;
	oldName?: string;
	name: string;
	mtu: string | number;
	vlan: string | number;
	disableIPv6: boolean;
	private: boolean;
	bridgeMacMode: '' | 'port' | 'object';
	dhcp: boolean;
	slaac: boolean;
	defaultRoute: boolean;
	defaultRoute6: boolean;
	disableBridgeOffloads: boolean;
	vlanFiltering: boolean;
	defaultAccessVlan: string | number;
	hostVlan: string | number;
	portPolicies: Record<string, VLANPolicyDraft>;
}

export interface ComboBoxState<T extends string | string[]> {
	open: boolean;
	value: T;
}

export interface StandardSwitchFormComboBoxes {
	ipv4: ComboBoxState<string>;
	ipv4Gw: ComboBoxState<string>;
	ipv6: ComboBoxState<string>;
	ipv6Gw: ComboBoxState<string>;
	ports: ComboBoxState<string[]>;
	bridgeMacPort: ComboBoxState<string>;
	bridgeMacObject: ComboBoxState<string>;
}

export interface SelectOption {
	label: string;
	value: string;
	disabled?: boolean;
	description?: string;
}
