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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

func GetIdFromParam(c *gin.Context) (int, error) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func HTTPPostJSON(url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
