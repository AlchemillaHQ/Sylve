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
	"strconv"
	"strings"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
)

const (
	sylveDDNSBaseURL                     = "https://sylve.app"
	sylvePublicationDelayedMessage       = "record saved but dns publication is delayed"
	sylveMaximumResponseBodySize   int64 = 1 << 20
)

type SylveProvider struct {
	BaseURL string
	Client  *http.Client
	now     func() time.Time
}

type sylveStatusResponse struct {
	Hostname string `json:"hostname"`
	IPv4     string `json:"ipv4"`
	IPv6     string `json:"ipv6"`
	Paused   *bool  `json:"paused"`
}

type sylveUpdateResponse struct {
	Hostname string `json:"hostname"`
	Changed  *bool  `json:"changed"`
}

func NewSylveProvider() *SylveProvider {
	return &SylveProvider{
		BaseURL: sylveDDNSBaseURL,
		Client: &http.Client{
			Timeout: 15 * time.Second,
		},
		now: time.Now,
	}
}

func (p *SylveProvider) ID() string {
	return dynamicDNSModels.ProviderSylve
}

func (p *SylveProvider) Validate(ctx context.Context, token, hostname, recordType string, _ map[string]string) (map[string]string, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("Sylve.app update token is required")
	}
	if !isRecordType(recordType) {
		return nil, fmt.Errorf("Sylve.app DDNS supports A, AAAA, or BOTH records")
	}

	status, err := p.status(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to verify Sylve.app update token: %w", err)
	}
	if err := validateSylveStatus(status, hostname); err != nil {
		return nil, err
	}

	return map[string]string{}, nil
}

func (p *SylveProvider) AddressMatches(ctx context.Context, token string, _ map[string]string, hostname, recordType string, address netip.Addr) (bool, error) {
	if err := validateSylveAddress(recordType, address); err != nil {
		return false, newProviderError(providerErrorPermanent, 0, err)
	}

	status, err := p.status(ctx, token)
	if err != nil {
		return false, err
	}
	if err := validateSylveStatus(status, hostname); err != nil {
		return false, err
	}

	current := status.IPv4
	if recordType == dynamicDNSModels.RecordTypeAAAA {
		current = status.IPv6
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return false, nil
	}

	parsed, err := netip.ParseAddr(current)
	if err != nil {
		return false, newProviderError(providerErrorTransient, 0, fmt.Errorf("Sylve.app DDNS returned an invalid %s address", recordType))
	}
	return parsed.Unmap() == address.Unmap(), nil
}

func (p *SylveProvider) Upsert(ctx context.Context, token string, _ map[string]string, hostname, recordType string, address netip.Addr) error {
	if strings.TrimSpace(token) == "" {
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("Sylve.app update token is required"))
	}
	if err := validateSylveAddress(recordType, address); err != nil {
		return newProviderError(providerErrorPermanent, 0, err)
	}

	payload := struct {
		IPv4 string `json:"ipv4,omitempty"`
		IPv6 string `json:"ipv6,omitempty"`
	}{}
	if recordType == dynamicDNSModels.RecordTypeA {
		payload.IPv4 = address.Unmap().String()
	} else {
		payload.IPv6 = address.String()
	}

	var result sylveUpdateResponse
	if err := p.doJSON(ctx, token, http.MethodPost, "/api/ddns/update", payload, &result); err != nil {
		return fmt.Errorf("failed to update Sylve.app %s record: %w", recordType, err)
	}
	responseHostname, err := normalizeHostname(result.Hostname)
	if err != nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("Sylve.app DDNS update returned an invalid hostname"))
	}
	if responseHostname != hostname {
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("Sylve.app DDNS update returned an unexpected hostname"))
	}
	if result.Changed == nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("Sylve.app DDNS update response did not include changed status"))
	}

	return nil
}

func (p *SylveProvider) status(ctx context.Context, token string) (sylveStatusResponse, error) {
	var status sylveStatusResponse
	if err := p.doJSON(ctx, token, http.MethodGet, "/api/ddns/status", nil, &status); err != nil {
		return sylveStatusResponse{}, err
	}
	return status, nil
}

func (p *SylveProvider) doJSON(ctx context.Context, token, method, endpoint string, payload any, result any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return newProviderError(providerErrorPermanent, 0, fmt.Errorf("failed to encode Sylve.app DDNS request: %w", err))
		}
		body = bytes.NewReader(encoded)
	}

	baseURL := strings.TrimRight(p.BaseURL, "/")
	request, err := http.NewRequestWithContext(ctx, method, baseURL+endpoint, body)
	if err != nil {
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("failed to create Sylve.app DDNS request: %w", err))
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("Sylve.app DDNS request failed: %w", err))
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, sylveMaximumResponseBodySize))
	if err != nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("failed to read Sylve.app DDNS response: %w", err))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return p.responseError(response.StatusCode, response.Header.Get("Retry-After"), data)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("invalid Sylve.app DDNS response: %w", err))
	}
	return nil
}

func (p *SylveProvider) responseError(statusCode int, retryAfterValue string, data []byte) error {
	message := sylveErrorMessage(data)
	detail := fmt.Sprintf("Sylve.app DDNS returned HTTP %d", statusCode)
	if message != "" {
		detail += ": " + message
	}

	if statusCode == http.StatusServiceUnavailable && strings.Contains(strings.ToLower(message), sylvePublicationDelayedMessage) {
		return newProviderError(providerErrorPending, 0, fmt.Errorf("%s", detail))
	}

	switch statusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return newProviderError(providerErrorTransient, p.retryAfter(retryAfterValue), fmt.Errorf("%s", detail))
	case http.StatusInternalServerError, http.StatusServiceUnavailable:
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("%s", detail))
	default:
		if statusCode >= http.StatusInternalServerError {
			return newProviderError(providerErrorTransient, 0, fmt.Errorf("%s", detail))
		}
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("%s", detail))
	}
}

func (p *SylveProvider) retryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		now := time.Now
		if p.now != nil {
			now = p.now
		}
		delay := retryAt.Sub(now().UTC())
		if delay > 0 {
			return delay
		}
	}
	return 0
}

func validateSylveStatus(status sylveStatusResponse, hostname string) error {
	statusHostname, err := normalizeHostname(status.Hostname)
	if err != nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("Sylve.app DDNS status returned an invalid hostname"))
	}
	if statusHostname != hostname {
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("Sylve.app update token belongs to %q, not %q", statusHostname, hostname))
	}
	if status.Paused == nil {
		return newProviderError(providerErrorTransient, 0, fmt.Errorf("Sylve.app DDNS status did not include paused state"))
	}
	if *status.Paused {
		return newProviderError(providerErrorPermanent, 0, fmt.Errorf("Sylve.app hostname %q is paused", statusHostname))
	}
	if raw := strings.TrimSpace(status.IPv4); raw != "" {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.Is4() {
			return newProviderError(providerErrorTransient, 0, fmt.Errorf("Sylve.app DDNS status returned an invalid IPv4 address"))
		}
	}
	if raw := strings.TrimSpace(status.IPv6); raw != "" {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.Is6() {
			return newProviderError(providerErrorTransient, 0, fmt.Errorf("Sylve.app DDNS status returned an invalid IPv6 address"))
		}
	}
	return nil
}

func validateSylveAddress(recordType string, address netip.Addr) error {
	switch {
	case recordType == dynamicDNSModels.RecordTypeA && address.Is4():
		return nil
	case recordType == dynamicDNSModels.RecordTypeAAAA && address.Is6():
		return nil
	default:
		return fmt.Errorf("Sylve.app DDNS record type does not match the resolved address")
	}
}

func sylveErrorMessage(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return ""
	}

	var response struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(data, &response); err == nil {
		if message := strings.TrimSpace(response.Message); message != "" {
			return message
		}
		if len(response.Error) != 0 {
			var message string
			if err := json.Unmarshal(response.Error, &message); err == nil && strings.TrimSpace(message) != "" {
				return strings.TrimSpace(message)
			}
		}
	}

	return trimmed
}
