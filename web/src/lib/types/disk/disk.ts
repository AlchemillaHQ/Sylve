/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { z } from 'zod/v4';

export const DeviceInfoSchema = z.object({
	name: z.string(),
	info_name: z.string(),
	type: z.string(),
	protocol: z.string()
});

export const PartitionSchema = z.object({
	uuid: z.string(),
	name: z.string(),
	usage: z.string(),
	size: z.number()
});

export const ATASmartAttributeSchema = z.object({
	page: z.number().default(0),
	id: z.number(),
	name: z.string(),
	value: z.number().optional(),
	worst: z.number().optional(),
	thresh: z.number().optional(),
	raw_value: z.number(),
	raw_string: z.string(),
	state: z.number().default(0),
	when_failed: z.string().default(''),
	pre_failure: z.boolean().default(false),
	online: z.boolean().default(false),
	performance: z.boolean().default(false),
	error_rate: z.boolean().default(false),
	event_count: z.boolean().default(false),
	auto_keep: z.boolean().default(false)
});

export const SCSISelfTestResultSchema = z.object({
	type: z.string(),
	status: z.string(),
	lifetime_hours: z.number(),
	lba: z.number(),
	lba_valid: z.boolean(),
	sense_key: z.number(),
	asc: z.number(),
	ascq: z.number()
});

export const SmartDataSchema = z.object({
	device: DeviceInfoSchema,
	passed: z.boolean(),
	health_known: z.boolean().default(false),
	checksum_valid: z.boolean().default(false),
	power_on_hours: z.number(),
	power_cycle_count: z.number(),
	temperature: z.number(),
	self_test_status: z.object({ status: z.string(), remaining_pct: z.number() }).optional(),
	smart_capability: z.number().default(0),
	scsi_self_test_results: z.array(SCSISelfTestResultSchema).optional(),
	attributes: z.array(ATASmartAttributeSchema).nullable().optional()
});

export const NvmeCriticalWarningStateSchema = z.object({
	availableSpare: z.number(),
	temperature: z.number(),
	deviceReliability: z.number(),
	readOnly: z.number(),
	volatileMemoryBackup: z.number()
});

export const SmartNVMeSchema = z.object({
	device: DeviceInfoSchema,
	passed: z.boolean(),
	health_known: z.boolean().default(false),
	power_on_hours: z.number(),
	power_on_hours_exact: z.string().default(''),
	power_cycle_count: z.number(),
	power_cycle_count_exact: z.string().default(''),
	temperature: z.number(),

	criticalWarning: z.string(),
	criticalWarningState: NvmeCriticalWarningStateSchema,
	availableSpare: z.number(),
	availableSpareThreshold: z.number(),
	percentageUsed: z.number(),
	dataUnitsRead: z.number(),
	dataUnitsReadExact: z.string().default(''),
	dataUnitsWritten: z.number(),
	dataUnitsWrittenExact: z.string().default(''),
	hostReadCommands: z.number(),
	hostReadCommandsExact: z.string().default(''),
	hostWriteCommands: z.number(),
	hostWriteCommandsExact: z.string().default(''),
	controllerBusyTime: z.number(),
	controllerBusyTimeExact: z.string().default(''),
	unsafeShutdowns: z.number(),
	unsafeShutdownsExact: z.string().default(''),
	mediaErrors: z.number(),
	mediaErrorsExact: z.string().default(''),
	errorInfoLogEntries: z.number(),
	errorInfoLogEntriesExact: z.string().default(''),
	warningCompositeTempTime: z.number(),
	errorCompositeTempTime: z.number(),
	temperature1TransitionCnt: z.number(),
	temperature2TransitionCnt: z.number(),
	totalTimeForTemperature1: z.number(),
	totalTimeForTemperature2: z.number()
});

export const DiskSchema = z.object({
	uuid: z.string(),
	identityStable: z.boolean().default(false),
	device: z.string(),
	type: z.string(),
	usage: z.string(),
	size: z.number(),
	model: z.string(),
	serial: z.string(),
	gpt: z.boolean(),
	smartData: z.union([SmartNVMeSchema, SmartDataSchema, z.null()]).optional(),
	wearOut: z.string(),
	partitions: z.array(PartitionSchema).default([])
});

export const DiskActionSchema = z.object({
	device: z.string()
});

export const SmartSelfTestKindSchema = z.enum([
	'offline',
	'default',
	'short',
	'extended',
	'conveyance',
	'selective',
	'short_captive',
	'extended_captive',
	'conveyance_captive',
	'selective_captive'
]);

export const SmartSelfTestStartKindSchema = z.enum(['short', 'extended', 'conveyance']);

export const SmartSelfTestCapabilitiesSchema = z.object({
	protocol: z.string().default(''),
	scope: z.string().default(''),
	namespace_id: z.number().default(0),
	supported: z.boolean().default(false),
	single_operation: z.boolean().default(false),
	execution_support_known: z.boolean().default(false),
	offline: z.boolean().default(false),
	default: z.boolean().default(false),
	short: z.boolean().default(false),
	extended: z.boolean().default(false),
	conveyance: z.boolean().default(false),
	selective: z.boolean().default(false),
	short_captive: z.boolean().optional().default(false),
	extended_captive: z.boolean().optional().default(false),
	conveyance_captive: z.boolean().optional().default(false),
	selective_captive: z.boolean().optional().default(false),
	abort: z.boolean().default(false),
	result_log: z.boolean().default(false),
	progress: z.boolean().default(false),
	offline_duration_minutes: z.number().default(0),
	short_duration_minutes: z.number().default(0),
	extended_duration_minutes: z.number().default(0),
	conveyance_duration_minutes: z.number().default(0)
});

export const SmartSelfTestResultSchema = z.object({
	protocol: z.string().optional().default(''),
	type: z.string().default(''),
	mode: z.string().optional().default(''),
	status: z.string().default(''),
	outcome: z.string().optional().default(''),
	remaining_pct: z.number().default(-1),
	lifetime_hours: z.number().default(0),
	lifetime_hours_exact: z.string().default(''),
	lba: z.number().optional().default(0),
	lba_exact: z.string().default(''),
	lba_valid: z.boolean().optional().default(false),
	nsid: z.number().optional().default(0),
	nsid_valid: z.boolean().optional().default(false),
	segment_num: z.number().optional().default(0),
	sense_key: z.number().optional().default(0),
	asc: z.number().optional().default(0),
	ascq: z.number().optional().default(0),
	status_code_type: z.number().optional().default(0),
	status_code_type_valid: z.boolean().optional().default(false),
	status_code: z.number().optional().default(0),
	status_code_valid: z.boolean().optional().default(false),
	checkpoint: z.number().optional().default(0),
	parameter_code: z.number().optional().default(0),
	vendor_specific: z.number().optional().default(0),
	started_at: z.string().nullable().optional().default(null)
});

export const SmartSelfTestStatusSchema = z.object({
	protocol: z.string().default(''),
	namespace_id: z.number().default(0),
	state: z.string().default('idle'),
	execution_status: z.string().default(''),
	type: z.string().default(''),
	running: z.boolean().default(false),
	progress_pct: z.number().default(-1),
	progress_known: z.boolean().default(false),
	remaining_pct: z.number().default(-1),
	remaining_known: z.boolean().default(false),
	estimated_duration_minutes: z.number().default(0),
	offline_collection_status: z.string().default(''),
	offline_collection_running: z.boolean().default(false),
	checksum_valid: z.boolean().default(true),
	results: z.array(SmartSelfTestResultSchema).default([])
});

export const SmartSelfTestDetailsSchema = z.object({
	device: z.string(),
	capabilities: SmartSelfTestCapabilitiesSchema,
	status: SmartSelfTestStatusSchema
});

export const SmartSelfTestScheduleTypeSchema = z.enum(['short', 'extended']);

export const SmartSelfTestScheduleSchema = z.object({
	id: z.number(),
	diskKey: z.string(),
	device: z.string(),
	model: z.string().default(''),
	serial: z.string().default(''),
	testType: SmartSelfTestScheduleTypeSchema,
	cronExpr: z.string(),
	enabled: z.boolean(),
	queuedAt: z.string().nullable().optional().default(null),
	lastRunAt: z.string().nullable().optional().default(null),
	nextRunAt: z.string().nullable().optional().default(null),
	lastStatus: z.string().default('idle'),
	lastError: z.string().default(''),
	progressPct: z.number().default(-1),
	progressKnown: z.boolean().default(false),
	estimatedMinutes: z.number().default(0)
});

export type SmartAttribute = Record<
	string,
	string | number | boolean | Record<string, string | number | boolean>
>;

export type DeviceInfo = z.infer<typeof DeviceInfoSchema>;
export type ATASmartAttribute = z.infer<typeof ATASmartAttributeSchema>;
export type SmartData = z.infer<typeof SmartDataSchema>;
export type SmartNVMe = z.infer<typeof SmartNVMeSchema>;
export type Disk = z.infer<typeof DiskSchema>;
export type Partition = z.infer<typeof PartitionSchema>;
export type SmartSelfTestKind = z.infer<typeof SmartSelfTestKindSchema>;
export type SmartSelfTestStartKind = z.infer<typeof SmartSelfTestStartKindSchema>;
export type SmartSelfTestCapabilities = z.infer<typeof SmartSelfTestCapabilitiesSchema>;
export type SmartSelfTestResult = z.infer<typeof SmartSelfTestResultSchema>;
export type SmartSelfTestStatus = z.infer<typeof SmartSelfTestStatusSchema>;
export type SmartSelfTestDetails = z.infer<typeof SmartSelfTestDetailsSchema>;
export type SmartSelfTestScheduleType = z.infer<typeof SmartSelfTestScheduleTypeSchema>;
export type SmartSelfTestSchedule = z.infer<typeof SmartSelfTestScheduleSchema>;

export type SmartSelfTestScheduleInput = {
	device: string;
	testType: SmartSelfTestScheduleType;
	cronExpr: string;
	enabled: boolean;
};
