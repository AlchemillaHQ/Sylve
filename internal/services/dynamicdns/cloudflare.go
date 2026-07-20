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
	"strconv"
	"strings"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
)

const cloudflareAPIBaseURL = "https://api.cloudflare.com/client/v4"

type CloudflareProvider struct {
	BaseURL string
	Client  *http.Client
}

type cloudflareResponse[T any] struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     T `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type cloudflareZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cloudflareRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func NewCloudflareProvider() *CloudflareProvider {
	return &CloudflareProvider{
		BaseURL: cloudflareAPIBaseURL,
		Client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (p *CloudflareProvider) ID() string {
	return dynamicDNSModels.ProviderCloudflare
}

func (p *CloudflareProvider) Validate(ctx context.Context, token, hostname, _ string, settings map[string]string) (map[string]string, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("cloudflare API token is required")
	}

	var tokenStatus cloudflareResponse[struct {
		Status string `json:"status"`
	}]
	if err := p.do(ctx, token, http.MethodGet, "/user/tokens/verify", nil, &tokenStatus); err != nil {
		return nil, fmt.Errorf("failed to verify cloudflare API token: %w", err)
	}
	if !strings.EqualFold(tokenStatus.Result.Status, "active") {
		return nil, fmt.Errorf("cloudflare API token is not active")
	}

	zone, err := p.findZone(ctx, token, hostname)
	if err != nil {
		return nil, err
	}

	validated := cloneSettings(settings)
	validated["zoneId"] = zone.ID
	validated["zoneName"] = zone.Name
	return validated, nil
}

func (p *CloudflareProvider) Upsert(ctx context.Context, token string, settings map[string]string, hostname, recordType string, address netip.Addr) error {
	zoneID := strings.TrimSpace(settings["zoneId"])
	if zoneID == "" {
		return fmt.Errorf("cloudflare zone is not configured")
	}

	records, err := p.listRecords(ctx, token, zoneID, hostname, recordType)
	if err != nil {
		return err
	}

	if len(records) == 0 {
		payload := struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Content string `json:"content"`
			TTL     int    `json:"ttl"`
			Proxied bool   `json:"proxied"`
		}{
			Type:    recordType,
			Name:    hostname,
			Content: address.String(),
			TTL:     1,
			Proxied: false,
		}
		var response cloudflareResponse[cloudflareRecord]
		if err := p.do(ctx, token, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", payload, &response); err != nil {
			return fmt.Errorf("failed to create cloudflare %s record: %w", recordType, err)
		}
		return nil
	}

	for _, record := range records {
		if record.Content == address.String() {
			continue
		}

		payload := struct {
			Content string `json:"content"`
		}{Content: address.String()}
		var response cloudflareResponse[cloudflareRecord]
		endpoint := "/zones/" + url.PathEscape(zoneID) + "/dns_records/" + url.PathEscape(record.ID)
		if err := p.do(ctx, token, http.MethodPatch, endpoint, payload, &response); err != nil {
			return fmt.Errorf("failed to update cloudflare %s record: %w", recordType, err)
		}
	}

	return nil
}

func (p *CloudflareProvider) findZone(ctx context.Context, token, hostname string) (cloudflareZone, error) {
	labels := strings.Split(strings.TrimSuffix(hostname, "."), ".")
	if len(labels) < 2 {
		return cloudflareZone{}, fmt.Errorf("hostname %q does not contain a DNS zone", hostname)
	}

	for index := 0; index <= len(labels)-2; index++ {
		candidate := strings.Join(labels[index:], ".")
		query := url.Values{}
		query.Set("name", candidate)
		query.Set("per_page", "50")

		var response cloudflareResponse[[]cloudflareZone]
		if err := p.do(ctx, token, http.MethodGet, "/zones?"+query.Encode(), nil, &response); err != nil {
			return cloudflareZone{}, fmt.Errorf("failed to look up cloudflare zone: %w", err)
		}
		for _, zone := range response.Result {
			if strings.EqualFold(zone.Name, candidate) {
				return zone, nil
			}
		}
	}

	return cloudflareZone{}, fmt.Errorf("no cloudflare zone was found for %q", hostname)
}

func (p *CloudflareProvider) listRecords(ctx context.Context, token, zoneID, hostname, recordType string) ([]cloudflareRecord, error) {
	var records []cloudflareRecord
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("name", hostname)
		query.Set("type", recordType)
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", "100")

		var response cloudflareResponse[[]cloudflareRecord]
		endpoint := "/zones/" + url.PathEscape(zoneID) + "/dns_records?" + query.Encode()
		if err := p.do(ctx, token, http.MethodGet, endpoint, nil, &response); err != nil {
			return nil, fmt.Errorf("failed to list cloudflare %s records: %w", recordType, err)
		}

		for _, record := range response.Result {
			if record.Type == recordType && strings.EqualFold(record.Name, hostname) {
				records = append(records, record)
			}
		}

		if response.ResultInfo.TotalPages <= page || len(response.Result) == 0 {
			return records, nil
		}
	}
}

func (p *CloudflareProvider) do(ctx context.Context, token, method, endpoint string, payload any, result any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to encode cloudflare request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	baseURL := strings.TrimRight(p.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, method, baseURL+endpoint, body)
	if err != nil {
		return fmt.Errorf("failed to create cloudflare request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare request failed: %w", err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read cloudflare response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("cloudflare API returned HTTP %d: %s", response.StatusCode, cloudflareErrorMessage(data))
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("invalid cloudflare response: %w", err)
	}

	return cloudflareResponseError(data)
}

func cloudflareResponseError(data []byte) error {
	var response struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("invalid cloudflare response: %w", err)
	}
	if response.Success {
		return nil
	}

	if len(response.Errors) == 0 {
		return fmt.Errorf("cloudflare API request was unsuccessful")
	}

	messages := make([]string, 0, len(response.Errors))
	for _, apiError := range response.Errors {
		if apiError.Code == 0 {
			messages = append(messages, apiError.Message)
			continue
		}
		messages = append(messages, fmt.Sprintf("%d: %s", apiError.Code, apiError.Message))
	}
	return fmt.Errorf("cloudflare API request failed: %s", strings.Join(messages, "; "))
}

func cloudflareErrorMessage(data []byte) string {
	if err := cloudflareResponseError(data); err != nil {
		return err.Error()
	}
	return "unexpected response"
}
