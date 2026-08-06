// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificates

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	managedBrokerBaseURL                       = "https://sylve.app"
	managedBrokerMaximumResponseBodySize int64 = 1 << 20
	managedBrokerRequestTimeout                = 20 * time.Second
)

type managedBrokerOrder struct {
	ID             string `json:"id"`
	Hostname       string `json:"hostname"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	RetryAt        string `json:"retry_at"`
	NotBefore      string `json:"not_before"`
	NotAfter       string `json:"not_after"`
	CertificatePEM string `json:"certificate_pem"`
}

type managedBrokerOrderSummary struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Status   string `json:"status"`
}

type managedBrokerHTTPError struct {
	StatusCode  int
	Message     string
	RetryAfter  time.Duration
	ActiveOrder *managedBrokerOrderSummary
}

type permanentManagedBrokerError struct {
	err error
}

func (e *permanentManagedBrokerError) Error() string {
	return e.err.Error()
}

func (e *permanentManagedBrokerError) Unwrap() error {
	return e.err
}

func permanentManagedBrokerFailure(format string, args ...any) error {
	return &permanentManagedBrokerError{err: fmt.Errorf(format, args...)}
}

func (e *managedBrokerHTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Sylve.app certificate broker returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("Sylve.app certificate broker returned HTTP %d: %s", e.StatusCode, e.Message)
}

func (s *Service) createManagedBrokerOrder(ctx context.Context, token, orderID, csrPEM string) (managedBrokerOrder, error) {
	payload := struct {
		ID  string `json:"id"`
		CSR string `json:"csr"`
	}{ID: orderID, CSR: csrPEM}

	status, data, headers, err := s.doManagedBrokerRequest(ctx, token, http.MethodPost, "/api/tls/orders", payload)
	if err != nil {
		return managedBrokerOrder{}, err
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return managedBrokerOrder{}, s.managedBrokerResponseError(status, headers, data)
	}
	var order managedBrokerOrder
	if err := decodeManagedBrokerJSON(data, &order); err != nil {
		return managedBrokerOrder{}, permanentManagedBrokerFailure("decode Sylve.app certificate order response: %w", err)
	}
	return order, nil
}

func (s *Service) getManagedBrokerOrder(ctx context.Context, token, orderID string) (managedBrokerOrder, error) {
	status, data, headers, err := s.doManagedBrokerRequest(ctx, token, http.MethodGet, "/api/tls/orders/"+orderID, nil)
	if err != nil {
		return managedBrokerOrder{}, err
	}
	if status != http.StatusOK {
		return managedBrokerOrder{}, s.managedBrokerResponseError(status, headers, data)
	}
	var order managedBrokerOrder
	if err := decodeManagedBrokerJSON(data, &order); err != nil {
		return managedBrokerOrder{}, permanentManagedBrokerFailure("decode Sylve.app certificate order response: %w", err)
	}
	return order, nil
}

func (s *Service) cancelManagedBrokerOrder(ctx context.Context, token, orderID string) error {
	status, data, headers, err := s.doManagedBrokerRequest(ctx, token, http.MethodDelete, "/api/tls/orders/"+orderID, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrManagedBrokerRequestFailed, err)
	}
	switch status {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("%w: %w", ErrManagedBrokerRequestFailed, s.managedBrokerResponseError(status, headers, data))
	}
}

func (s *Service) doManagedBrokerRequest(ctx context.Context, token, method, endpoint string, payload any) (int, []byte, http.Header, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("encode Sylve.app certificate broker request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	baseURL := strings.TrimRight(s.managedBrokerURL, "/")
	if baseURL == "" {
		baseURL = managedBrokerBaseURL
	}
	request, err := http.NewRequestWithContext(ctx, method, baseURL+endpoint, body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("create Sylve.app certificate broker request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	baseClient := s.managedHTTPClient
	if baseClient == nil {
		baseClient = &http.Client{Timeout: managedBrokerRequestTimeout}
	}
	client := *baseClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("request Sylve.app certificate broker: %w", err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, managedBrokerMaximumResponseBodySize+1))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read Sylve.app certificate broker response: %w", err)
	}
	if int64(len(data)) > managedBrokerMaximumResponseBodySize {
		return 0, nil, nil, permanentManagedBrokerFailure("Sylve.app certificate broker response is too large")
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && len(data) > 0 {
		mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if parseErr != nil || mediaType != "application/json" {
			return 0, nil, nil, permanentManagedBrokerFailure("Sylve.app certificate broker returned a non-JSON response")
		}
	}
	return response.StatusCode, data, response.Header.Clone(), nil
}

func (s *Service) managedBrokerResponseError(statusCode int, headers http.Header, data []byte) error {
	var response struct {
		Error string                     `json:"error"`
		Order *managedBrokerOrderSummary `json:"order"`
	}
	_ = decodeManagedBrokerJSON(data, &response)
	return &managedBrokerHTTPError{
		StatusCode:  statusCode,
		Message:     strings.TrimSpace(response.Error),
		RetryAfter:  s.parseManagedRetryAfter(headers.Get("Retry-After")),
		ActiveOrder: response.Order,
	}
}

func (s *Service) parseManagedRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := retryAt.Sub(s.currentTime().UTC()); delay > 0 {
			return delay
		}
	}
	return 0
}

func decodeManagedBrokerJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("response contains multiple JSON values")
		}
		return err
	}
	return nil
}
