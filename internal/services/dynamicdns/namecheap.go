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
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	dynamicDNSModels "github.com/alchemillahq/sylve/internal/db/models/dynamicdns"
)

const namecheapDynamicDNSBaseURL = "https://dynamicdns.park-your-domain.com"

type NamecheapProvider struct {
	BaseURL string
	Client  *http.Client
}

type namecheapResponse struct {
	ErrCount int `xml:"ErrCount"`
	Errors   struct {
		Values []string `xml:",any"`
	} `xml:"errors"`
	Done bool `xml:"Done"`
}

func namecheapCharsetReader(label string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "utf-16", "utf-16le", "utf-16be":
		// Namecheap declares UTF-16 but sends the Dynamic DNS response as UTF-8.
		return input, nil
	default:
		return nil, fmt.Errorf("unsupported namecheap XML encoding %q", label)
	}
}

func NewNamecheapProvider() *NamecheapProvider {
	return &NamecheapProvider{
		BaseURL: namecheapDynamicDNSBaseURL,
		Client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (p *NamecheapProvider) ID() string {
	return dynamicDNSModels.ProviderNamecheap
}

func (p *NamecheapProvider) Validate(_ context.Context, password, hostname, recordType string, settings map[string]string) (map[string]string, error) {
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("namecheap Dynamic DNS password is required")
	}
	if recordType != dynamicDNSModels.RecordTypeA {
		return nil, fmt.Errorf("namecheap Dynamic DNS supports A records only")
	}

	domain := strings.TrimSuffix(strings.TrimSpace(settings[NamecheapSettingDomain]), ".")
	normalizedDomain, err := normalizeHostname(domain)
	if err != nil {
		return nil, fmt.Errorf("namecheap domain is invalid")
	}

	host := "@"
	if hostname != normalizedDomain {
		if !strings.HasSuffix(hostname, "."+normalizedDomain) {
			return nil, fmt.Errorf("hostname must be within the configured namecheap domain")
		}
		host = strings.TrimSuffix(hostname, "."+normalizedDomain)
	}

	return map[string]string{
		NamecheapSettingDomain: domain,
		NamecheapSettingHost:   host,
	}, nil
}

func (p *NamecheapProvider) Upsert(ctx context.Context, password string, settings map[string]string, _ string, recordType string, address netip.Addr) error {
	if recordType != dynamicDNSModels.RecordTypeA || !address.Is4() {
		return fmt.Errorf("namecheap Dynamic DNS supports IPv4 A records only")
	}

	domain := strings.TrimSpace(settings[NamecheapSettingDomain])
	host := strings.TrimSpace(settings[NamecheapSettingHost])
	if domain == "" || host == "" {
		return fmt.Errorf("namecheap domain and host are not configured")
	}

	query := url.Values{}
	query.Set("host", host)
	query.Set("domain", domain)
	query.Set("password", password)
	query.Set("ip", address.Unmap().String())

	baseURL := strings.TrimRight(p.BaseURL, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/update?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("failed to create namecheap update request: %w", err)
	}

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("namecheap update request failed: %w", err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read namecheap update response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("namecheap Dynamic DNS returned HTTP %d", response.StatusCode)
	}

	var result namecheapResponse
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = namecheapCharsetReader
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("invalid namecheap update response: %w", err)
	}
	if result.ErrCount != 0 {
		message := strings.Join(result.Errors.Values, "; ")
		if message == "" {
			message = "unknown error"
		}
		return fmt.Errorf("namecheap Dynamic DNS update failed: %s", message)
	}
	if !result.Done {
		return fmt.Errorf("namecheap Dynamic DNS update did not complete")
	}

	return nil
}
