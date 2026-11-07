// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
)

// A stream call is a node call whose request body is a byte stream, such as a
// zfs send, so its typed request travels in CallRequestHeader instead.
const CallRequestHeader = "X-Sylve-Call-Request"

type streamFunc func(ctx context.Context, issuer string, payload json.RawMessage, body io.Reader) (any, error)

func OnNodeStream[Req, Resp any](
	s *Service,
	name string,
	fn func(ctx context.Context, issuer string, req Req, body io.Reader) (Resp, error),
) {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	if s.calls == nil {
		s.calls = make(map[string]registeredCall)
	}
	s.calls[name] = registeredCall{stream: func(ctx context.Context, issuer string, payload json.RawMessage, body io.Reader) (any, error) {
		var req Req
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("call_request_invalid: %s: %w", name, err)
		}
		return fn(ctx, issuer, req, body)
	}}
}

func (s *Service) registeredStream(name string) (streamFunc, error) {
	s.callsOnce.Do(s.registerCalls)
	s.callsMu.RLock()
	defer s.callsMu.RUnlock()
	call, ok := s.calls[name]
	if !ok || call.stream == nil {
		return nil, fmt.Errorf("call_unknown: %s", name)
	}
	return call.stream, nil
}

// StreamToNode sends body to the stream call name on nodeID over the
// intra-cluster mTLS channel and returns its typed response once the node has
// read the whole stream.
func StreamToNode[Req, Resp any](
	ctx context.Context,
	s *Service,
	nodeID, name string,
	req Req,
	body io.Reader,
) (Resp, error) {
	var resp Resp
	payload, err := json.Marshal(req)
	if err != nil {
		return resp, fmt.Errorf("call_request_invalid: %s: %w", name, err)
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == s.LocalNodeID() {
		call, err := s.registeredStream(name)
		if err != nil {
			return resp, err
		}
		result, err := call(ctx, nodeID, payload, body)
		if typed, ok := result.(Resp); ok {
			resp = typed
		}
		return resp, err
	}
	host, err := s.nodeAPIHost(nodeID)
	if err != nil {
		return resp, err
	}
	response, err := utils.HTTPStreamContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/intra-cluster/stream/%s", ClusterAPIHost(host), url.PathEscape(name)),
		body,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/octet-stream",
			CallNodeHeader:    nodeID,
			CallIndexHeader:   strconv.FormatUint(s.AppliedIndex(), 10),
			CallRequestHeader: string(payload),
		},
		4<<20,
	)
	if err != nil {
		return resp, fmt.Errorf("call_forward_failed: %s: %w", name, err)
	}
	result, err := callResult(name, response)
	return decodeCallResult[Resp](name, result, err)
}

func (s *Service) ServeNodeStream(
	ctx context.Context,
	issuer, nodeID string,
	index uint64,
	name string,
	payload json.RawMessage,
	body io.Reader,
) (CallResult, error) {
	call, err := s.registeredStream(name)
	if err != nil {
		return CallResult{}, err
	}
	return s.serveOnNode(ctx, nodeID, index, name, func() (any, error) {
		return call(ctx, issuer, payload, body)
	})
}
