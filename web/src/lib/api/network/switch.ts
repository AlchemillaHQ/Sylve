/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { APIResponseSchema, type APIResponse } from '$lib/types/common';
import { SwitchListSchema, type SwitchList, type VLANPortPolicy } from '$lib/types/network/switch';
import { apiRequest } from '$lib/utils/http';
import z from 'zod/v4';

export async function getSwitches(hostname?: string): Promise<SwitchList | APIResponse> {
	return await apiRequest('/network/switch', SwitchListSchema, 'GET', undefined, { hostname });
}

export async function createManualSwitch(
	name: string,
	bridge: string
): Promise<number | APIResponse> {
	const body = {
		name,
		bridge
	};

	return await apiRequest('/network/switch/manual', z.number().int().positive(), 'POST', body);
}

export async function deleteManualSwitch(id: number): Promise<APIResponse> {
	return await apiRequest(`/network/switch/manual/${id}`, APIResponseSchema, 'DELETE');
}

export type SwitchManualAddresses = {
	network4: string;
	gateway4: string;
	network6: string;
	gateway6: string;
};

export type StandardSwitchMACSource =
	| { mode: 'port'; port: string }
	| { mode: 'object'; macObjectId: number };

export type StandardSwitchVLANConfig = {
	vlanFiltering: boolean;
	defaultAccessVlan: number | null;
	hostVlan: number | null;
	portPolicies: Record<string, VLANPortPolicy>;
};

export type StandardSwitchConfig = {
	mtu: number;
	vlan: number;
	network4: number;
	gateway4: number;
	network6: number;
	gateway6: number;
	private: boolean;
	ports: string[];
	bridgeMac: StandardSwitchMACSource;
	disableIPv6: boolean;
	slaac: boolean;
	dhcp: boolean;
	defaultRoute: boolean;
	defaultRoute6: boolean;
	disableBridgeOffloads: boolean;
	vlanConfig: StandardSwitchVLANConfig;
	manual?: SwitchManualAddresses;
	confirmRCConflicts?: boolean;
	confirmHostLayer3Removal?: boolean;
};

export type CreateStandardSwitchRequest = StandardSwitchConfig & { name: string };

const emptyManualAddresses: SwitchManualAddresses = {
	network4: '',
	gateway4: '',
	network6: '',
	gateway6: ''
};

function standardSwitchRequestBody<T extends StandardSwitchConfig>(request: T) {
	const {
		vlanConfig,
		manual = emptyManualAddresses,
		confirmRCConflicts = false,
		confirmHostLayer3Removal = false,
		...config
	} = request;
	return {
		...config,
		vlanFiltering: vlanConfig.vlanFiltering,
		defaultAccessVlan: vlanConfig.defaultAccessVlan,
		hostVlan: vlanConfig.hostVlan,
		portPolicies: vlanConfig.portPolicies,
		confirmRCConflicts,
		confirmHostLayer3Removal,
		network4Manual: manual.network4,
		gateway4Manual: manual.gateway4,
		network6Manual: manual.network6,
		gateway6Manual: manual.gateway6
	};
}

export async function createSwitch(
	request: CreateStandardSwitchRequest
): Promise<number | APIResponse> {
	return await apiRequest(
		'/network/switch/standard',
		z.number().int().positive(),
		'POST',
		standardSwitchRequestBody(request)
	);
}

export async function deleteSwitch(id: number): Promise<APIResponse> {
	return await apiRequest(`/network/switch/standard/${id}`, APIResponseSchema, 'DELETE');
}

export async function updateSwitch(
	id: number,
	request: StandardSwitchConfig
): Promise<APIResponse> {
	return await apiRequest(
		`/network/switch/standard/${id}`,
		APIResponseSchema,
		'PUT',
		standardSwitchRequestBody(request)
	);
}
