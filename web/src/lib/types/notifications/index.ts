import { z } from 'zod/v4';

export const NotificationSeveritySchema = z.enum(['info', 'warning', 'error', 'critical']);

export const NotificationSchema = z.object({
	id: z.number(),
	kind: z.string(),
	title: z.string(),
	body: z.string(),
	severity: NotificationSeveritySchema,
	source: z.string(),
	fingerprint: z.string(),
	metadata: z.record(z.string(), z.string()).default({}),
	occurrenceCount: z.number(),
	firstOccurredAt: z.string(),
	lastOccurredAt: z.string(),
	dismissedAt: z.string().nullable().optional(),
	createdAt: z.string().optional(),
	updatedAt: z.string().optional()
});

export const NotificationsListSchema = z.object({
	items: z.array(NotificationSchema),
	total: z.number()
});

export const NotificationsCountSchema = z.object({
	active: z.number()
});

export const NotificationConfigSchema = z.object({
	transports: z.array(
		z.object({
			id: z.number(),
			name: z.string(),
			type: z.enum(['ntfy', 'smtp']),
			enabled: z.boolean(),
			ntfy: z
				.object({
					baseUrl: z.string(),
					topic: z.string(),
					hasAuthToken: z.boolean()
				})
				.optional(),
			email: z
				.object({
					smtpHost: z.string(),
					smtpPort: z.number(),
					smtpUsername: z.string(),
					smtpFrom: z.string(),
					smtpUseTls: z.boolean(),
					recipients: z.array(z.string()),
					hasPassword: z.boolean()
				})
				.optional()
		})
	)
});

export const NotificationRuleSchema = z.object({
	id: z.number(),
	kind: z.string(),
	templateKey: z.string(),
	templateLabel: z.string(),
	targetKey: z.string(),
	targetLabel: z.string(),
	active: z.boolean(),
	uiEnabled: z.boolean(),
	ntfyEnabled: z.boolean(),
	emailEnabled: z.boolean()
});

export const NotificationRuleTemplateTargetSchema = z.object({
	key: z.string(),
	label: z.string()
});

export const NotificationRuleTemplateSchema = z.object({
	key: z.string(),
	label: z.string(),
	description: z.string(),
	targetType: z.string(),
	targets: z.array(NotificationRuleTemplateTargetSchema)
});

export const NotificationRulesConfigSchema = z.object({
	rules: z.array(NotificationRuleSchema),
	templates: z.array(NotificationRuleTemplateSchema)
});

export type Notification = z.infer<typeof NotificationSchema>;
export type NotificationsList = z.infer<typeof NotificationsListSchema>;
export type NotificationsCount = z.infer<typeof NotificationsCountSchema>;
export type NotificationConfig = z.infer<typeof NotificationConfigSchema>;
export type NotificationRule = z.infer<typeof NotificationRuleSchema>;
export type NotificationRuleTemplateTarget = z.infer<typeof NotificationRuleTemplateTargetSchema>;
export type NotificationRuleTemplate = z.infer<typeof NotificationRuleTemplateSchema>;
export type NotificationRulesConfig = z.infer<typeof NotificationRulesConfigSchema>;

export type UpdateNotificationConfigInput = {
	transports: Array<{
		id?: number;
		name: string;
		type: 'ntfy' | 'smtp';
		enabled: boolean;
		ntfy: {
			baseUrl: string;
			topic: string;
			authToken?: string;
		} | null;
		email: {
			smtpHost: string;
			smtpPort: number;
			smtpUsername: string;
			smtpFrom: string;
			smtpUseTls: boolean;
			recipients: string[];
			smtpPassword?: string;
		} | null;
	}>;
};

export type UpdateNotificationRulesInput = {
	rules: Array<{
		id?: number;
		kind: string;
		pool?: string;
		templateKey?: string;
		targetKey?: string;
		uiEnabled: boolean;
		ntfyEnabled: boolean;
		emailEnabled: boolean;
	}>;
};

export type CreateNotificationRuleInput = {
	templateKey: string;
	targetKey: string;
	uiEnabled: boolean;
	ntfyEnabled: boolean;
	emailEnabled: boolean;
};

export type UpdateNotificationRuleInput = {
	uiEnabled: boolean;
	ntfyEnabled: boolean;
	emailEnabled: boolean;
};
