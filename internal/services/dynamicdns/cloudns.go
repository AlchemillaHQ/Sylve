// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicdns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
)

const (
	cloudnsAPIBaseURL = "https://api.cloudns.net"

	cloudnsRecordsPath      = "/dns/records.json"
	cloudnsZoneInfoPath     = "/dns/get-zone-info.json"
	cloudnsRecordsStatsPath = "/dns/get-records-stats.json"
	cloudnsAvailableTTLPath = "/dns/get-available-ttl.json"
	cloudnsAddRecordPath    = "/dns/add-record.json"
	cloudnsModRecordPath    = "/dns/mod-record.json"

	cloudnsApexHost = "@"

	cloudnsMaximumResponseBytes int64 = 1 << 20
	cloudnsValidationTimeout          = 20 * time.Second
	cloudnsRecordsPerPage             = 100
	cloudnsMaximumRecordPages         = 50

	cloudnsMissingZoneDescription = "missing domain-name"
	cloudnsAuthenticationPrefix   = "invalid authentication, incorrect"
	cloudnsAccessHint             = "this can also be an API-user access or IP allowlist restriction; verify the API user's access level and allowlist settings in ClouDNS"
)

var cloudnsPermanentDescriptions = []string{
	"invalid ttl",
	"this is not a valid ip address",
	"this record type is not supported",
}

type CloudnsProvider struct {
	BaseURL string
	Client  *http.Client
}

type cloudnsEnvelope struct {
	Status            string `json:"status"`
	StatusDescription string `json:"statusDescription"`
}

type cloudnsZone struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Zone        string `json:"zone"`
	Status      string `json:"status"`
	CloudMaster string `json:"cloud-master"`
}

type cloudnsRecordPayload struct {
	ID         *string         `json:"id"`
	Type       *string         `json:"type"`
	Host       *string         `json:"host"`
	Record     json.RawMessage `json:"record"`
	TTL        string          `json:"ttl"`
	Status     *int            `json:"status"`
	GeodnsCode string          `json:"geodns-location-code"`
}

type cloudnsRecord struct {
	ID        string
	NumericID int64
	Host      string
	Address   netip.Addr
	TTL       int
	Status    int
}

func NewCloudnsProvider() *CloudnsProvider {
	return &CloudnsProvider{
		BaseURL: cloudnsAPIBaseURL,
		Client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (p *CloudnsProvider) ID() string {
	return dynamicDNSModels.ProviderCloudns
}

func (p *CloudnsProvider) Validate(ctx context.Context, password, hostname, recordType string, settings map[string]string) (map[string]string, error) {
	authID, err := cloudnsAuthID(settings)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(password) == "" {
		return nil, newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS API password is required"))
	}
	if !isRecordType(recordType) {
		return nil, newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS supports A, AAAA, or BOTH records"))
	}

	runCtx, cancel := context.WithTimeout(ctx, cloudnsValidationTimeout)
	defer cancel()

	if err := p.probe(runCtx, authID, password); err != nil {
		return nil, err
	}

	zone, err := p.findZone(runCtx, authID, password, hostname)
	if err != nil {
		return nil, err
	}

	host, err := cloudnsRelativeHost(hostname, zone)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		CloudnsSettingAuthID: authID,
		CloudnsSettingZone:   zone,
		CloudnsSettingHost:   host,
	}, nil
}

func (p *CloudnsProvider) Upsert(ctx context.Context, password string, settings map[string]string, hostname, recordType string, address netip.Addr) error {
	authID, err := cloudnsAuthID(settings)
	if err != nil {
		return err
	}
	if strings.TrimSpace(password) == "" {
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS API password is required"))
	}
	zone, host, err := cloudnsTarget(settings, hostname)
	if err != nil {
		return err
	}
	if !cloudnsAddressMatchesType(recordType, address) {
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS record type does not match the resolved address"))
	}

	records, err := p.listRecords(ctx, authID, password, zone, host, recordType)
	if err != nil {
		return err
	}

	active := make([]cloudnsRecord, 0, len(records))
	for _, record := range records {
		if record.Status == 1 {
			active = append(active, record)
		}
	}
	if len(records) > 0 && len(active) == 0 {
		return newProviderError(
			providerErrorPermanent,
			0,
			fmt.Errorf("all ClouDNS %s records for %q are inactive; activate the intended record in ClouDNS or delete it", recordType, hostname),
		)
	}

	desired := address
	if recordType == dynamicDNSModels.RecordTypeA {
		desired = address.Unmap()
	}
	desiredValue := desired.String()

	if len(active) > 0 {
		for _, record := range active {
			if record.Address.Unmap() == desired.Unmap() {
				continue
			}
			if err := p.modifyRecord(ctx, authID, password, zone, record, desiredValue); err != nil {
				return err
			}
		}
		return nil
	}

	ttl, err := p.availableTTL(ctx, authID, password, zone)
	if err != nil {
		return err
	}
	return p.addRecord(ctx, authID, password, zone, host, recordType, desiredValue, ttl)
}

func (p *CloudnsProvider) probe(ctx context.Context, authID, password string) error {
	body, err := p.post(ctx, authID, password, cloudnsRecordsStatsPath, nil)
	if err != nil {
		return err
	}
	if description, failed := cloudnsFailedEnvelope(body); failed {
		return cloudnsBodyError(description, password, false)
	}

	var payload json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS credentials response was not valid JSON"))
	}
	return nil
}

func (p *CloudnsProvider) findZone(ctx context.Context, authID, password, hostname string) (string, error) {
	labels := strings.Split(hostname, ".")
	for index := 0; index <= len(labels)-2; index++ {
		if err := ctx.Err(); err != nil {
			return "", newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS zone discovery was cancelled: %w", err))
		}

		candidate := strings.Join(labels[index:], ".")
		body, err := p.post(ctx, authID, password, cloudnsZoneInfoPath, url.Values{"domain-name": {candidate}})
		if err != nil {
			return "", err
		}

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 {
			continue
		}
		if description, failed := cloudnsFailedEnvelope(trimmed); failed {
			if strings.EqualFold(strings.TrimSpace(description), cloudnsMissingZoneDescription) {
				continue
			}
			return "", cloudnsBodyError(description, password, false)
		}

		var zone cloudnsZone
		if err := json.Unmarshal(trimmed, &zone); err != nil {
			return "", newProviderError(providerErrorTransient, 0, fmt.Errorf("invalid ClouDNS zone response for %q", candidate))
		}

		name := strings.TrimSpace(zone.Name)
		zoneType := strings.ToLower(strings.TrimSpace(zone.Type))
		status := strings.TrimSpace(zone.Status)
		if name == "" || zoneType == "" || status == "" {
			return "", newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS zone response for %q is missing required fields", candidate))
		}
		if !strings.EqualFold(name, candidate) {
			return "", newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS zone response for %q did not match the requested zone", candidate))
		}

		name = strings.ToLower(name)
		if err := cloudnsZoneSupportError(name, zoneType, status); err != nil {
			return "", err
		}
		return name, nil
	}

	return "", newProviderError(providerErrorPermanent, 0, fmt.Errorf("no ClouDNS zone was found for %q", hostname))
}

func cloudnsZoneSupportError(name, zoneType, status string) error {
	if strings.HasSuffix(name, ".arpa") {
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS zone %q is a reverse zone, which is not supported", name))
	}
	if zoneType != "master" {
		switch {
		case strings.Contains(zoneType, "cloud"):
			return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS zone %q is a cloud zone, which is not supported in this version", name))
		case strings.Contains(zoneType, "slave"), strings.Contains(zoneType, "secondary"):
			return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS zone %q is a slave zone, which is not supported", name))
		case strings.Contains(zoneType, "geodns"):
			return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS zone %q is a GeoDNS zone, which is not supported", name))
		default:
			return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS zone %q has unsupported type %q", name, zoneType))
		}
	}

	switch status {
	case "1":
		return nil
	case "0":
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS zone %q is inactive", name))
	default:
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS zone %q returned an unrecognized status", name))
	}
}

func cloudnsRelativeHost(hostname, zone string) (string, error) {
	if strings.EqualFold(hostname, zone) {
		return cloudnsApexHost, nil
	}
	suffix := "." + zone
	if !strings.HasSuffix(hostname, suffix) {
		return "", newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS zone %q does not contain hostname %q", zone, hostname))
	}
	host := strings.TrimSuffix(hostname, suffix)
	if host == "" {
		return cloudnsApexHost, nil
	}
	return host, nil
}

func (p *CloudnsProvider) listRecords(ctx context.Context, authID, password, zone, host, recordType string) ([]cloudnsRecord, error) {
	apiHost := cloudnsAPIHost(host)
	var records []cloudnsRecord
	seen := make(map[string]struct{})

	for page := 1; page <= cloudnsMaximumRecordPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record listing was cancelled: %w", err))
		}

		params := url.Values{}
		params.Set("domain-name", zone)
		params.Set("host", apiHost)
		params.Set("type", recordType)
		params.Set("rows-per-page", strconv.Itoa(cloudnsRecordsPerPage))
		params.Set("page", strconv.Itoa(page))

		body, err := p.post(ctx, authID, password, cloudnsRecordsPath, params)
		if err != nil {
			return nil, err
		}

		pageRecords, rawKeys, err := cloudnsParseRecordPage(body, password, host, recordType)
		if err != nil {
			return nil, err
		}
		for _, key := range rawKeys {
			if _, ok := seen[key]; ok {
				return nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record listing repeated record id %q across pages", key))
			}
			seen[key] = struct{}{}
		}
		records = append(records, pageRecords...)

		if len(rawKeys) < cloudnsRecordsPerPage {
			if len(records) == 0 && len(seen) > 0 {
				return nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record listing returned entries that did not match the requested host and type"))
			}
			sort.Slice(records, func(first, second int) bool {
				return records[first].NumericID < records[second].NumericID
			})
			return records, nil
		}
	}

	return nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record listing exceeded %d pages", cloudnsMaximumRecordPages))
}

func cloudnsParseRecordPage(body []byte, password, host, recordType string) ([]cloudnsRecord, []string, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record listing returned an empty response"))
	}
	if description, failed := cloudnsFailedEnvelope(trimmed); failed {
		return nil, nil, cloudnsBodyError(description, password, true)
	}

	if trimmed[0] == '[' {
		var entries []json.RawMessage
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("invalid ClouDNS record listing response"))
		}
		if len(entries) != 0 {
			return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record listing returned an unexpected array"))
		}
		return nil, nil, nil
	}
	if trimmed[0] != '{' {
		return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("invalid ClouDNS record listing response"))
	}

	var payloads map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payloads); err != nil || payloads == nil {
		return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("invalid ClouDNS record listing response"))
	}
	if len(payloads) == 0 {
		return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record listing returned an unsupported empty object"))
	}

	keys := make([]string, 0, len(payloads))
	records := make([]cloudnsRecord, 0, len(payloads))
	for key, raw := range payloads {
		keys = append(keys, key)

		var payload cloudnsRecordPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("invalid ClouDNS record entry %q", key))
		}
		if payload.Type == nil || strings.TrimSpace(*payload.Type) == "" {
			return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record entry %q is missing its record type", key))
		}
		if payload.Host == nil {
			return nil, nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record entry %q is missing its host", key))
		}

		recordTypeValue := strings.ToUpper(strings.TrimSpace(*payload.Type))
		hostValue := *payload.Host
		if !cloudnsHostMatches(hostValue, host) || !strings.EqualFold(recordTypeValue, recordType) {
			continue
		}

		record, err := cloudnsBuildRecord(key, payload, recordTypeValue, hostValue)
		if err != nil {
			return nil, nil, err
		}
		records = append(records, record)
	}

	return records, keys, nil
}

func cloudnsBuildRecord(key string, payload cloudnsRecordPayload, recordType, host string) (cloudnsRecord, error) {
	numericID, err := strconv.ParseInt(strings.TrimSpace(key), 10, 64)
	if err != nil || numericID <= 0 {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record listing returned an invalid record id"))
	}
	if payload.ID != nil {
		parsed, err := strconv.ParseInt(strings.TrimSpace(*payload.ID), 10, 64)
		if err != nil || parsed != numericID {
			return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d returned a mismatched id field", numericID))
		}
	}

	ttl, err := strconv.Atoi(strings.TrimSpace(payload.TTL))
	if err != nil || ttl <= 0 {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d returned an invalid TTL", numericID))
	}
	if payload.Status == nil {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d did not include an active status", numericID))
	}
	if *payload.Status != 0 && *payload.Status != 1 {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d returned an unrecognized status", numericID))
	}
	if strings.TrimSpace(payload.GeodnsCode) != "" {
		return cloudnsRecord{}, newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS record %d is a GeoDNS location record, which is not supported", numericID))
	}

	rawRecord := bytes.TrimSpace(payload.Record)
	if len(rawRecord) == 0 {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d did not include a value", numericID))
	}
	if rawRecord[0] != '"' {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d returned an unsupported value shape", numericID))
	}

	var value string
	if err := json.Unmarshal(rawRecord, &value); err != nil {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d returned an invalid value", numericID))
	}
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d returned an invalid address", numericID))
	}
	if !cloudnsAddressMatchesType(recordType, address) {
		return cloudnsRecord{}, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS record %d address does not match its record type", numericID))
	}

	return cloudnsRecord{
		ID:        strconv.FormatInt(numericID, 10),
		NumericID: numericID,
		Host:      host,
		Address:   address,
		TTL:       ttl,
		Status:    *payload.Status,
	}, nil
}

func (p *CloudnsProvider) availableTTL(ctx context.Context, authID, password, zone string) (int, error) {
	params := url.Values{"domain-name": {zone}}
	body, err := p.post(ctx, authID, password, cloudnsAvailableTTLPath, params)
	if err != nil {
		return 0, err
	}
	if description, failed := cloudnsFailedEnvelope(body); failed {
		return 0, cloudnsBodyError(description, password, true)
	}

	var values []json.RawMessage
	if err := json.Unmarshal(body, &values); err != nil {
		return 0, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS available TTL response was not an array"))
	}
	smallest := 0
	for _, raw := range values {
		value, ok := cloudnsTTLValue(raw)
		if !ok {
			return 0, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS available TTL response contained an invalid value"))
		}
		if smallest == 0 || value < smallest {
			smallest = value
		}
	}
	if smallest <= 0 {
		return 0, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS returned no usable TTL for the zone"))
	}
	return smallest, nil
}

func cloudnsTTLValue(raw json.RawMessage) (int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0, false
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return 0, false
		}
		value, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || value <= 0 {
			return 0, false
		}
		return value, true
	}
	var value int
	if err := json.Unmarshal(trimmed, &value); err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func (p *CloudnsProvider) addRecord(ctx context.Context, authID, password, zone, host, recordType, value string, ttl int) error {
	params := url.Values{}
	params.Set("domain-name", zone)
	params.Set("record-type", recordType)
	params.Set("host", cloudnsAPIHost(host))
	params.Set("record", value)
	params.Set("ttl", strconv.Itoa(ttl))

	body, err := p.post(ctx, authID, password, cloudnsAddRecordPath, params)
	if err != nil {
		return err
	}
	return cloudnsRequireWriteSuccess(body, password)
}

func (p *CloudnsProvider) modifyRecord(ctx context.Context, authID, password, zone string, record cloudnsRecord, value string) error {
	params := url.Values{}
	params.Set("domain-name", zone)
	params.Set("record-id", record.ID)
	params.Set("host", record.Host)
	params.Set("record", value)
	params.Set("ttl", strconv.Itoa(record.TTL))

	body, err := p.post(ctx, authID, password, cloudnsModRecordPath, params)
	if err != nil {
		return err
	}
	return cloudnsRequireWriteSuccess(body, password)
}

func (p *CloudnsProvider) post(ctx context.Context, authID, password, endpoint string, params url.Values) ([]byte, error) {
	form := url.Values{}
	for key, values := range params {
		form[key] = append([]string(nil), values...)
	}
	form.Set("auth-id", authID)
	form.Set("auth-password", password)

	baseURL := strings.TrimRight(p.BaseURL, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("failed to create ClouDNS request: %w", err))
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS request failed: %w", err))
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, cloudnsMaximumResponseBytes+1))
	if err != nil {
		return nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("failed to read ClouDNS response: %w", err))
	}
	if int64(len(data)) > cloudnsMaximumResponseBytes {
		return nil, newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS response exceeded %d bytes", cloudnsMaximumResponseBytes))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, cloudnsHTTPError(response.StatusCode, response.Header.Get("Retry-After"), data, password)
	}

	return data, nil
}

func cloudnsHTTPError(statusCode int, retryAfterHeader string, body []byte, password string) error {
	detail := ""
	if description, failed := cloudnsFailedEnvelope(body); failed {
		detail = cloudnsSanitize(description, password)
	}

	message := fmt.Sprintf("ClouDNS API returned HTTP %d", statusCode)
	if detail != "" {
		message += ": " + detail
	}

	switch {
	case statusCode == http.StatusUnauthorized:
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("%s", message))
	case statusCode == http.StatusForbidden:
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("%s; %s", message, cloudnsAccessHint))
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError:
		return newProviderError(providerErrorTransient, cloudnsRetryAfter(retryAfterHeader), fmt.Errorf("%s", message))
	default:
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("%s", message))
	}
}

func cloudnsRequireWriteSuccess(body []byte, password string) error {
	var envelope cloudnsEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS write response was not valid JSON"))
	}

	switch envelope.Status {
	case "Success":
		return nil
	case "Failed":
		return cloudnsBodyError(envelope.StatusDescription, password, true)
	default:
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS write response did not confirm success"))
	}
}

func cloudnsBodyError(description, password string, savedZone bool) error {
	normalized := strings.ToLower(strings.TrimSpace(description))

	detail := cloudnsSanitize(description, password)
	if detail == "" {
		detail = "the API did not include a description"
	}

	switch {
	case strings.HasPrefix(normalized, cloudnsAuthenticationPrefix):
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS rejected the credentials: %s", detail))
	case savedZone && normalized == cloudnsMissingZoneDescription:
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS zone is missing or no longer accessible: %s", detail))
	}
	for _, permanent := range cloudnsPermanentDescriptions {
		if strings.HasPrefix(normalized, permanent) {
			return newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS API request failed: %s", detail))
		}
	}

	return newProviderError(providerErrorTransient, 0, fmt.Errorf("ClouDNS API request failed: %s; %s", detail, cloudnsAccessHint))
}

func cloudnsFailedEnvelope(body []byte) (string, bool) {
	var envelope cloudnsEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", false
	}
	if envelope.Status != "Failed" {
		return "", false
	}
	return strings.TrimSpace(envelope.StatusDescription), true
}

func cloudnsAuthID(settings map[string]string) (string, error) {
	raw := strings.TrimSpace(settings[CloudnsSettingAuthID])
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return "", newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS API user ID must be a positive decimal number"))
	}
	return strconv.FormatUint(value, 10), nil
}

func cloudnsTarget(settings map[string]string, hostname string) (string, string, error) {
	zone := strings.ToLower(strings.TrimSpace(settings[CloudnsSettingZone]))
	host, ok := settings[CloudnsSettingHost]
	host = strings.TrimSpace(host)
	if zone == "" || !ok || host == "" {
		return "", "", newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS zone and host are not configured"))
	}

	expected := zone
	if host != cloudnsApexHost {
		expected = host + "." + zone
	}
	if !strings.EqualFold(expected, hostname) {
		return "", "", newProviderError(providerErrorPermanent, 0, fmt.Errorf("ClouDNS settings target %q but the entry hostname is %q", expected, hostname))
	}
	return zone, host, nil
}

func cloudnsAPIHost(host string) string {
	if host == cloudnsApexHost {
		return ""
	}
	return host
}

func cloudnsHostMatches(recordHost, host string) bool {
	if host == cloudnsApexHost {
		return recordHost == "" || recordHost == cloudnsApexHost
	}
	return strings.EqualFold(recordHost, host)
}

func cloudnsAddressMatchesType(recordType string, address netip.Addr) bool {
	switch recordType {
	case dynamicDNSModels.RecordTypeA:
		return address.Is4()
	case dynamicDNSModels.RecordTypeAAAA:
		return address.Is6() && !address.Is4In6()
	default:
		return false
	}
}

func cloudnsSanitize(value, password string) string {
	trimmed := strings.TrimSpace(value)
	if password == "" {
		return trimmed
	}
	trimmed = strings.ReplaceAll(trimmed, password, "[REDACTED]")
	if encoded := url.QueryEscape(password); encoded != password {
		trimmed = strings.ReplaceAll(trimmed, encoded, "[REDACTED]")
	}
	return trimmed
}

func cloudnsRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
	}
	return 0
}
