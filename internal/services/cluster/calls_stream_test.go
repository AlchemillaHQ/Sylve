// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil"
)

type streamTestRequest struct {
	Name string `json:"name"`
}

type streamTestResponse struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

func newStreamTestService(t *testing.T) *Service {
	t.Helper()
	s := NewClusterService(testutil.NewSQLiteTestDB(t), nil, nil).(*Service)
	s.NodeID = "node-a"
	OnNodeStream(s, "count", func(_ context.Context, _ string, req streamTestRequest, body io.Reader) (streamTestResponse, error) {
		n, err := io.Copy(io.Discard, body)
		return streamTestResponse{Name: req.Name, Bytes: n}, err
	})
	return s
}

func TestStreamToNodeRunsALocalStreamCall(t *testing.T) {
	s := newStreamTestService(t)
	resp, err := StreamToNode[streamTestRequest, streamTestResponse](
		t.Context(), s, "node-a", "count", streamTestRequest{Name: "disk"}, strings.NewReader("hello"),
	)
	if err != nil || resp.Name != "disk" || resp.Bytes != 5 {
		t.Fatalf("response %+v, err %v", resp, err)
	}
	if _, err := StreamToNode[streamTestRequest, streamTestResponse](
		t.Context(), s, "node-a", "missing", streamTestRequest{}, strings.NewReader(""),
	); err == nil || !strings.Contains(err.Error(), "call_unknown") {
		t.Fatalf("unknown stream call error = %v", err)
	}
}

func TestServeNodeStreamRejectsAnotherNodesStream(t *testing.T) {
	s := newStreamTestService(t)
	if _, err := s.ServeNodeStream(
		t.Context(), "node-b", "node-c", 0, "count", json.RawMessage(`{}`), strings.NewReader("x"),
	); err == nil || !strings.Contains(err.Error(), "call_node_mismatch") {
		t.Fatalf("stream for another node: err = %v", err)
	}
	// A plain node call can't be reached through the stream endpoint.
	OnNode(s, "plain", func(context.Context, string, struct{}) (struct{}, error) { return struct{}{}, nil })
	if _, err := s.ServeNodeStream(
		t.Context(), "node-b", "node-a", 0, "plain", json.RawMessage(`{}`), strings.NewReader(""),
	); err == nil || !strings.Contains(err.Error(), "call_unknown") {
		t.Fatalf("plain call through stream endpoint: err = %v", err)
	}
}
