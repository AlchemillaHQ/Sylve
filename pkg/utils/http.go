// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	once         sync.Once
	sharedClient *http.Client
)

func GetTokenFromHeader(r http.Header) (string, error) {
	token := r.Get("Authorization")
	if token != "" {
		if len(token) < 8 || !strings.HasPrefix(token, "Bearer ") {
			return "", fmt.Errorf("invalid authorization header format")
		}
		return RemoveSpaces(token[7:]), nil
	}

	wsProtocol := r.Get("Sec-WebSocket-Protocol")
	if wsProtocol != "" {
		parts := strings.Split(wsProtocol, ",")
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "Bearer" {
			return RemoveSpaces(strings.TrimSpace(parts[1])), nil
		}
		return "", errors.New("invalid websocket protocol header format")
	}

	return "", errors.New("no token provided")
}

func GetClusterTokenFromHeader(r http.Header) (string, error) {
	if v := r.Get("ClusterToken"); v != "" {
		if len(v) < 8 || !strings.HasPrefix(v, "Bearer ") {
			return "", fmt.Errorf("invalid ClusterToken header format")
		}
		return RemoveSpaces(v[7:]), nil
	}

	if v := r.Get("X-Cluster-Authorization"); v != "" {
		if len(v) < 8 || !strings.HasPrefix(v, "Bearer ") {
			return "", fmt.Errorf("invalid X-Cluster-Authorization header format")
		}
		return RemoveSpaces(v[7:]), nil
	}

	if v := r.Get("X-Cluster-Token"); v != "" {
		if len(v) < 8 || !strings.HasPrefix(v, "Bearer ") {
			return "", fmt.Errorf("invalid X-Cluster-Token header format")
		}
		return RemoveSpaces(v[7:]), nil
	}

	if v := r.Get("Sec-WebSocket-Protocol"); v != "" {
		text := RemoveSpaces(v)
		data, err := hex.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("failed to decode hex: %w", err)
		}

		var obj struct {
			Hostname string `json:"hostname"`
			Token    string `json:"token"`
		}

		if err := json.Unmarshal(data, &obj); err != nil {
			return "", fmt.Errorf("failed to unmarshal json: %w", err)
		}

		if obj.Token == "" {
			return "", errors.New("no_token_provided")
		}

		return obj.Token, nil
	}

	return "", errors.New("no cluster token provided")
}

func GetCurrentHostnameFromHeader(r http.Header, rC *http.Request) (string, error) {
	if v := r.Get("X-Current-Hostname"); v != "" {
		return RemoveSpaces(v), nil
	}

	if v := r.Get("Sec-WebSocket-Protocol"); v != "" {
		text := RemoveSpaces(v)
		data, err := hex.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("failed to decode hex: %w", err)
		}

		var obj struct {
			Hostname string `json:"hostname"`
			Token    string `json:"token"`
		}

		if err := json.Unmarshal(data, &obj); err != nil {
			return "", fmt.Errorf("failed to unmarshal json: %w", err)
		}

		if obj.Hostname == "" {
			return "", errors.New("no_current_hostname_provided")
		}

		return obj.Hostname, nil
	}

	if v := rC.URL.Query().Get("auth"); v != "" {
		text := RemoveSpaces(v)
		data, err := hex.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("failed to decode hex: %w", err)
		}

		var obj struct {
			Hostname string `json:"hostname"`
			Token    string `json:"token"`
		}

		if err := json.Unmarshal(data, &obj); err != nil {
			return "", fmt.Errorf("failed to unmarshal json: %w", err)
		}

		if obj.Hostname == "" {
			return "", errors.New("no_current_hostname_provided")
		}

		return obj.Hostname, nil
	}

	return "", errors.New("no_current_hostname_provided")
}

func GetIdFromParam(c *gin.Context) (int, error) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func FlatHeaders(c *gin.Context) map[string]string {
	var flatHeaders = make(map[string]string)
	for key, value := range c.Request.Header {
		if len(value) > 0 {
			flatHeaders[key] = value[0]
		}
	}
	return flatHeaders
}

func intraClusterClient() *http.Client {
	once.Do(func() {
		tr := &http.Transport{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 100,
			MaxConnsPerHost:     100,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		}
		sharedClient = &http.Client{
			Timeout:   8 * time.Second,
			Transport: tr,
		}
	})
	return sharedClient
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if ctx != nil {
		return context.WithTimeout(ctx, d)
	}
	return context.WithTimeout(context.Background(), d)
}

func HTTPPostJSON(url string, payload any, headers map[string]string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Accept-Encoding") == "" {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := intraClusterClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var reader io.ReadCloser
	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	default:
		reader = resp.Body
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(reader)
		return fmt.Errorf("http error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func HTTPPostJSONRead(url string, payload any, headers map[string]string) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Accept-Encoding") == "" {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := intraClusterClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var reader io.ReadCloser
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, resp.StatusCode, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	} else {
		reader = resp.Body
	}

	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("http error %d: %s", resp.StatusCode, string(b))
	}
	return b, resp.StatusCode, nil
}

func HTTPGetJSONRead(url string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Accept-Encoding") == "" {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := intraClusterClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var reader io.ReadCloser
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, resp.StatusCode, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	} else {
		reader = resp.Body
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("http error %d: %s", resp.StatusCode, string(data))
	}
	return data, resp.StatusCode, nil
}

func ParamUint(c *gin.Context, name string) (uint, error) {
	param := c.Param(name)
	value, err := strconv.ParseUint(param, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s parameter: %w", name, err)
	}
	return uint(value), nil
}
