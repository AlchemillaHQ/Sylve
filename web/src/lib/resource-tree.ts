export type ResourceTreeView = 'server' | 'folder';
export type ResourceTreeSortKey = 'id' | 'name';
export type ResourceTreeState = 'active' | 'inactive' | 'orphan';
export type ResourceTreeResourceType = 'vm' | 'jail' | 'jail-template' | 'vm-template';

export interface ResourceTreePreferences {
	view: ResourceTreeView;
	sortKey: ResourceTreeSortKey;
	groupTemplates: boolean;
	groupGuestTypes: boolean;
}

export const DEFAULT_RESOURCE_TREE_PREFERENCES: ResourceTreePreferences = {
	view: 'server',
	sortKey: 'id',
	groupTemplates: true,
	groupGuestTypes: false
};

export interface ResourceTreeItem {
	id: string;
	label: string;
	icon: string;
	href?: string;
	state?: ResourceTreeState;
	resourceId?: number;
	resourceType?: ResourceTreeResourceType;
	nodeHostname?: string;
	nextGuestId?: number;
	meta?: string;
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
	'vm-template': 2,
	'jail-template': 3
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
				: DEFAULT_RESOURCE_TREE_PREFERENCES.groupGuestTypes
	};
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

function sortedResources(
	resources: ResourceTreeResource[],
	sortKey: ResourceTreeSortKey
): ResourceTreeResource[] {
	return [...resources].sort((a, b) => compareResources(a, b, sortKey));
}

function group(
	id: string,
	label: string,
	icon: string,
	children: ResourceTreeItem[]
): ResourceTreeItem | null {
	if (children.length === 0) return null;
	return { id, label, icon, children };
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
		const vmResources = (preferences.groupTemplates ? guests : node.resources).filter(isVMResource);
		const jailResources = (preferences.groupTemplates ? guests : node.resources).filter(
			(resource) => !isVMResource(resource)
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

	const children = sortedResources(
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
		(resource) => resource.resourceType === 'vm' || (!preferences.groupTemplates && resource.resourceType === 'vm-template')
	);
	const jailResources = resources.filter(
		(resource) => resource.resourceType === 'jail' || (!preferences.groupTemplates && resource.resourceType === 'jail-template')
	);
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
