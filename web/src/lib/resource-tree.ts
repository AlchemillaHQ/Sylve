/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { SimpleJail } from '$lib/types/jail/jail';

export type ResourceTreeView = 'server' | 'folder';
export type ResourceTreeSortKey = 'id' | 'name';
export type ResourceTreeState = 'active' | 'inactive' | 'orphan' | 'partial';
export type ResourceTreeResourceType =
	| 'vm'
	| 'jail'
	| 'container'
	| 'stack'
	| 'jail-template'
	| 'vm-template';
export type ResourceTreeDensity = 'comfortable' | 'compact';

export interface ResourceTreePreferences {
	view: ResourceTreeView;
	sortKey: ResourceTreeSortKey;
	groupTemplates: boolean;
	groupGuestTypes: boolean;
	density: ResourceTreeDensity;
}

export const DEFAULT_RESOURCE_TREE_PREFERENCES: ResourceTreePreferences = {
	view: 'server',
	sortKey: 'id',
	groupTemplates: true,
	groupGuestTypes: false,
	density: 'comfortable'
};

export interface ResourceTreeItem {
	id: string;
	label: string;
	icon: string;
	href?: string;
	state?: ResourceTreeState;
	resourceId?: number;
	resourceType?: ResourceTreeResourceType;
	jailType?: SimpleJail['type'];
	nodeHostname?: string;
	nextGuestId?: number;
	meta?: string;
	stack?: string;
	stackTitle?: string;
	count?: number;
	children?: ResourceTreeItem[];
}

export interface ResourceTreeResource extends ResourceTreeItem {
	resourceId: number;
	resourceType: ResourceTreeResourceType;
	sortId: number;
	sortName: string;
}

export interface ResourceTreeNodeInput {
	id: string;
	label: string;
	icon: string;
	href: string;
	resources: ResourceTreeResource[];
	nextGuestId?: number;
}

interface BuildResourceTreeOptions {
	nodes: ResourceTreeNodeInput[];
	preferences: ResourceTreePreferences;
	rootIcon: string;
	nextGuestId?: number;
}

const nameCollator = new Intl.Collator(undefined, {
	numeric: true,
	sensitivity: 'base'
});

const resourceTypeRank: Record<ResourceTreeResourceType, number> = {
	vm: 0,
	jail: 1,
	container: 2,
	stack: 2,
	'vm-template': 3,
	'jail-template': 4
};

export function normalizeResourceTreePreferences(value: unknown): ResourceTreePreferences {
	if (!value || typeof value !== 'object') {
		return { ...DEFAULT_RESOURCE_TREE_PREFERENCES };
	}

	const candidate = value as Partial<ResourceTreePreferences>;
	return {
		view: candidate.view === 'folder' ? 'folder' : 'server',
		sortKey: candidate.sortKey === 'name' ? 'name' : 'id',
		groupTemplates:
			typeof candidate.groupTemplates === 'boolean'
				? candidate.groupTemplates
				: DEFAULT_RESOURCE_TREE_PREFERENCES.groupTemplates,
		groupGuestTypes:
			typeof candidate.groupGuestTypes === 'boolean'
				? candidate.groupGuestTypes
				: DEFAULT_RESOURCE_TREE_PREFERENCES.groupGuestTypes,
		density: candidate.density === 'compact' ? 'compact' : 'comfortable'
	};
}

export function isResourceTreeGroup(item: ResourceTreeItem): boolean {
	return !item.resourceType && !item.href && !!item.children;
}

export function filterResourceTree(items: ResourceTreeItem[], query: string): ResourceTreeItem[] {
	const q = query.trim().toLowerCase();
	if (!q) return items;

	function walk(item: ResourceTreeItem): ResourceTreeItem | null {
		const selfMatch =
			item.label.toLowerCase().includes(q) || (item.meta?.toLowerCase().includes(q) ?? false);

		if (selfMatch) {
			return item;
		}

		if (!item.children) {
			return null;
		}

		const filteredChildren = item.children
			.map(walk)
			.filter((child): child is ResourceTreeItem => child !== null);

		if (filteredChildren.length === 0) {
			return null;
		}

		return { ...item, children: filteredChildren };
	}

	return items.map(walk).filter((item): item is ResourceTreeItem => item !== null);
}

export function collectResourceTreeIds(nodes: ResourceTreeItem[]): string[] {
	const ids: string[] = [];
	for (const node of nodes) {
		ids.push(node.id);
		if (node.children?.length) {
			ids.push(...collectResourceTreeIds(node.children));
		}
	}
	return ids;
}

function isTemplate(resource: ResourceTreeResource): boolean {
	return resource.resourceType === 'vm-template' || resource.resourceType === 'jail-template';
}

function isVMResource(resource: ResourceTreeResource): boolean {
	return resource.resourceType === 'vm' || resource.resourceType === 'vm-template';
}

function isContainerResource(resource: ResourceTreeResource): boolean {
	return resource.resourceType === 'container';
}

function compareResources(
	a: ResourceTreeResource,
	b: ResourceTreeResource,
	sortKey: ResourceTreeSortKey
): number {
	if (sortKey === 'name') {
		const byName = nameCollator.compare(a.sortName, b.sortName);
		if (byName !== 0) return byName;

		const byId = a.sortId - b.sortId;
		if (byId !== 0) return byId;
	} else {
		const byId = a.sortId - b.sortId;
		if (byId !== 0) return byId;

		const byName = nameCollator.compare(a.sortName, b.sortName);
		if (byName !== 0) return byName;
	}

	const byType = resourceTypeRank[a.resourceType] - resourceTypeRank[b.resourceType];
	if (byType !== 0) return byType;

	return nameCollator.compare(a.id, b.id);
}

export function stackMemberName(name: string, stack: string): string {
	const prefix = `${stack}-`.toLowerCase();
	return stack && name.length > prefix.length && name.toLowerCase().startsWith(prefix)
		? name.slice(prefix.length)
		: name;
}

function stacked(
	resources: ResourceTreeResource[],
	sortKey: ResourceTreeSortKey
): ResourceTreeResource[] {
	const stacks = new Map<string, ResourceTreeResource[]>();
	const rest: ResourceTreeResource[] = [];
	for (const resource of resources) {
		if (resource.resourceType !== 'container' || !resource.stack) {
			rest.push(resource);
			continue;
		}
		const key = `${resource.nodeHostname}/${resource.stack}`;
		stacks.set(key, [...(stacks.get(key) ?? []), resource]);
	}
	for (const members of stacks.values()) {
		const [first] = members;
		const title = first.stackTitle || first.stack || '';
		const running = members.filter((member) => member.state === 'active').length;
		rest.push({
			id: `stack-${first.nodeHostname}-${first.stack}`,
			label: title,
			icon: 'lucide--layers',
			resourceType: 'stack',
			resourceId: Math.min(...members.map((member) => member.resourceId)),
			sortId: Math.min(...members.map((member) => member.sortId)),
			sortName: title,
			nodeHostname: first.nodeHostname,
			meta: first.meta,
			stack: first.stack,
			stackTitle: first.stackTitle,
			state: running === members.length ? 'active' : running > 0 ? 'partial' : 'inactive',
			children: members
				.map((member) => ({ ...member, meta: undefined }))
				.sort((a, b) => compareResources(a, b, sortKey))
		});
	}
	return rest;
}

function sortedResources(
	resources: ResourceTreeResource[],
	sortKey: ResourceTreeSortKey
): ResourceTreeResource[] {
	return stacked(resources, sortKey).sort((a, b) => compareResources(a, b, sortKey));
}

function group(
	id: string,
	label: string,
	icon: string,
	children: ResourceTreeItem[],
	count?: number
): ResourceTreeItem | null {
	if (children.length === 0) return null;
	return { id, label, icon, children, count };
}

function compactGroups(groups: Array<ResourceTreeItem | null>): ResourceTreeItem[] {
	return groups.filter((item): item is ResourceTreeItem => item !== null);
}

function buildServerNodeChildren(
	node: ResourceTreeNodeInput,
	preferences: ResourceTreePreferences
): ResourceTreeItem[] {
	const templates = node.resources.filter(isTemplate);
	const guests = node.resources.filter((resource) => !isTemplate(resource));

	if (preferences.groupGuestTypes) {
		const resources = preferences.groupTemplates ? guests : node.resources;
		const vmResources = resources.filter(isVMResource);
		const containerResources = resources.filter(isContainerResource);
		const jailResources = resources.filter(
			(resource) => !isVMResource(resource) && !isContainerResource(resource)
		);

		return compactGroups([
			group(
				`server:${node.id}:virtual-machines`,
				'Virtual Machines',
				'material-symbols--monitor-outline',
				sortedResources(vmResources, preferences.sortKey)
			),
			group(
				`server:${node.id}:jails`,
				'Jails',
				'hugeicons--prison',
				sortedResources(jailResources, preferences.sortKey)
			),
			group(
				`server:${node.id}:containers`,
				'Containers',
				'lucide--container',
				sortedResources(containerResources, preferences.sortKey),
				containerResources.length
			),
			preferences.groupTemplates
				? group(
						`server:${node.id}:templates`,
						'Templates',
						'mdi--layers-outline',
						sortedResources(templates, preferences.sortKey)
					)
				: null
		]);
	}

	const children: ResourceTreeItem[] = sortedResources(
		preferences.groupTemplates ? guests : node.resources,
		preferences.sortKey
	);

	if (preferences.groupTemplates) {
		const templateGroup = group(
			`server:${node.id}:templates`,
			'Templates',
			'mdi--layers-outline',
			sortedResources(templates, preferences.sortKey)
		);
		if (templateGroup) children.push(templateGroup);
	}

	return children;
}

function buildServerView(
	nodes: ResourceTreeNodeInput[],
	preferences: ResourceTreePreferences
): ResourceTreeItem[] {
	return [...nodes]
		.sort((a, b) => nameCollator.compare(a.label, b.label))
		.map((node) => {
			const children = buildServerNodeChildren(node, preferences);
			return {
				id: node.id,
				label: node.label,
				icon: node.icon,
				href: node.href,
				nextGuestId: node.nextGuestId,
				children: children.length > 0 ? children : undefined
			};
		});
}

function withNodeMeta(
	resource: ResourceTreeResource,
	node: ResourceTreeNodeInput,
	showNodeMeta: boolean
): ResourceTreeResource {
	return {
		...resource,
		meta: showNodeMeta ? node.label : undefined
	};
}

function buildFolderView(
	nodes: ResourceTreeNodeInput[],
	preferences: ResourceTreePreferences
): ResourceTreeItem[] {
	const sortedNodes = [...nodes].sort((a, b) => nameCollator.compare(a.label, b.label));
	const showNodeMeta = sortedNodes.length > 1;
	const resources = sortedNodes.flatMap((node) =>
		node.resources.map((resource) => withNodeMeta(resource, node, showNodeMeta))
	);

	const vmResources = resources.filter(
		(resource) =>
			resource.resourceType === 'vm' ||
			(!preferences.groupTemplates && resource.resourceType === 'vm-template')
	);
	const jailResources = resources.filter(
		(resource) =>
			resource.resourceType === 'jail' ||
			(!preferences.groupTemplates && resource.resourceType === 'jail-template')
	);
	const containerResources = resources.filter(isContainerResource);
	const templates = resources.filter(isTemplate);

	return compactGroups([
		group(
			'folder:nodes',
			'Nodes',
			'fluent--storage-20-filled',
			sortedNodes.map((node) => ({
				id: node.id,
				label: node.label,
				icon: node.icon,
				href: node.href
			}))
		),
		group(
			'folder:virtual-machines',
			'Virtual Machines',
			'material-symbols--monitor-outline',
			sortedResources(vmResources, preferences.sortKey)
		),
		group(
			'folder:jails',
			'Jails',
			'hugeicons--prison',
			sortedResources(jailResources, preferences.sortKey)
		),
		group(
			'folder:containers',
			'Containers',
			'lucide--container',
			sortedResources(containerResources, preferences.sortKey),
			containerResources.length
		),
		preferences.groupTemplates
			? group(
					'folder:templates',
					'Templates',
					'mdi--layers-outline',
					sortedResources(templates, preferences.sortKey)
				)
			: null
	]);
}

export function buildResourceTree({
	nodes,
	preferences,
	rootIcon,
	nextGuestId
}: BuildResourceTreeOptions): ResourceTreeItem[] {
	const children =
		preferences.view === 'folder'
			? buildFolderView(nodes, preferences)
			: buildServerView(nodes, preferences);

	return [
		{
			id: 'datacenter',
			label: 'Data Center',
			icon: rootIcon,
			href: '/datacenter/summary',
			nextGuestId,
			children: children.length > 0 ? children : undefined
		}
	];
}
