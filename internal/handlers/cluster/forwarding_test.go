// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

type forwardRecorder struct{ *httptest.ResponseRecorder }

func (forwardRecorder) CloseNotify() <-chan bool { return make(chan bool) }

func newClusterForwardTestContext(
	method string,
	path string,
	body string,
	headers map[string]string,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(forwardRecorder{recorder})
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("UserID", uint(7))
	c.Set("Username", "forward-user")
	c.Set("AuthType", "sylve")
	c.Set("AuthScope", "local")
	for key, value := range headers {
		c.Request.Header.Set(key, value)
	}
	return c, recorder
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func serveForwards(t *testing.T, handler http.Handler) *[]string {
	t.Helper()
	var hosts []string
	original := forwardTransport
	forwardTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})
	t.Cleanup(func() { forwardTransport = original })
	return &hosts
}

func TestForwardToPassesTheRequestAndTheAnswerThrough(t *testing.T) {
	answers := []struct {
		status      int
		contentType string
		body        string
	}{
		{http.StatusOK, "application/json", `{"status":"success"}`},
		{http.StatusBadRequest, "application/json", `{"message":"invalid"}`},
		{http.StatusConflict, "text/plain; charset=utf-8", "already running"},
		{http.StatusServiceUnavailable, "application/problem+json", `{"detail":"leader busy"}`},
	}
	var seen *http.Request
	var seenBody string
	hosts := serveForwards(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		body, _ := io.ReadAll(r.Body)
		seenBody = string(body)
		answer := answers[0]
		if index, err := strconv.Atoi(r.URL.Query().Get("case")); err == nil {
			answer = answers[index]
		}
		w.Header().Set("Content-Type", answer.contentType)
		w.Header().Set("Connection", "X-Private-Hop")
		w.Header().Set("X-Private-Hop", "secret")
		w.WriteHeader(answer.status)
		_, _ = w.Write([]byte(answer.body))
	}))
	service := &cluster.Service{AuthService: authForwardStub{}}

	for index, answer := range answers {
		t.Run(http.StatusText(answer.status), func(t *testing.T) {
			c, recorder := newClusterForwardTestContext(
				http.MethodPost,
				fmt.Sprintf("/api/cluster/test?case=%d&hash=browser-hash&auth=00", index),
				`{"request":true}`,
				map[string]string{
					"Accept-Encoding":            "gzip, deflate, br",
					"Idempotency-Key":            "operation-1",
					"X-Correlation-ID":           "correlation-1",
					"Authorization":              "Bearer browser-token",
					"Cookie":                     "session=browser",
					authService.ClusterKeyHeader: "join-secret",
					cluster.JoinerPinHeader:      "forged-pin",
					authService.ProxyUserHeader:  `{"id":999,"username":"forged","authType":"sylve"}`,
				},
			)
			ForwardTo(c, service, "member:8184", nil)

			if recorder.Code != answer.status || recorder.Body.String() != answer.body ||
				recorder.Header().Get("Content-Type") != answer.contentType {
				t.Fatalf("answer = %d %q %q, want %d %q %q", recorder.Code, recorder.Header().Get("Content-Type"),
					recorder.Body.String(), answer.status, answer.contentType, answer.body)
			}
			if recorder.Header().Get("X-Private-Hop") != "" {
				t.Fatalf("a hop-by-hop header came back: %v", recorder.Header())
			}
		})
	}

	if len(*hosts) != len(answers) || (*hosts)[0] != "member:8184" {
		t.Fatalf("forwarded to %v", *hosts)
	}
	if seen.URL.Path != "/api/cluster/test" || seen.URL.Query().Get("case") != "3" ||
		seen.URL.Query().Has("hash") || seen.URL.Query().Has("auth") {
		t.Fatalf("forwarded URL %s", seen.URL)
	}
	if seenBody != `{"request":true}` || seen.Header.Get("Idempotency-Key") != "operation-1" ||
		seen.Header.Get("X-Correlation-ID") != "correlation-1" {
		t.Fatalf("forwarded body %q headers %v", seenBody, seen.Header)
	}
	want := serviceInterfaces.ProxiedUser{Username: "admin", AuthType: "sylve"}.Header()
	if got := seen.Header.Get(authService.ProxyUserHeader); got != want {
		t.Fatalf("forwarded for %q, want the signed-in admin %q", got, want)
	}
	for _, name := range []string{"Authorization", "Cookie", authService.ClusterKeyHeader, cluster.JoinerPinHeader, "Accept-Encoding"} {
		if got := seen.Header.Get(name); got != "" {
			t.Fatalf("the client's %s went along: %q", name, got)
		}
	}
}

func TestForwardToSendsTheBodyTheHandlerRead(t *testing.T) {
	var seenBody string
	var seenLength int64
	serveForwards(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenBody, seenLength = string(body), r.ContentLength
		w.WriteHeader(http.StatusAccepted)
	}))
	c, recorder := newClusterForwardTestContext(http.MethodPost, "/api/cluster/restore", `{"snapshot":" raw "}`, nil)
	_, _ = io.ReadAll(c.Request.Body)

	ForwardTo(c, &cluster.Service{AuthService: authForwardStub{}}, "runner:8184", []byte(`{"snapshot":"canonical"}`))
	c.Writer.WriteHeaderNow()
	if recorder.Code != http.StatusAccepted || seenBody != `{"snapshot":"canonical"}` || seenLength != int64(len(seenBody)) {
		t.Fatalf("answer %d, runner got %q (length %d)", recorder.Code, seenBody, seenLength)
	}
}

func TestForwardToAnswersWhatItCantForward(t *testing.T) {
	service := &cluster.Service{AuthService: authForwardStub{}}

	t.Run("a member's request", func(t *testing.T) {
		hosts := serveForwards(t, http.NotFoundHandler())
		c, recorder := newClusterForwardTestContext(http.MethodPost, "/api/cluster/test", `{}`, nil)
		c.Set("AuthScope", "cluster")
		c.Set("ClusterCaller", authService.ClusterCallerUser)
		ForwardTo(c, service, "member:8184", nil)
		if recorder.Code != http.StatusMisdirectedRequest || !strings.Contains(recorder.Body.String(), "node_forward_second_hop") ||
			len(*hosts) != 0 {
			t.Fatalf("answer %d %s, forwarded to %v", recorder.Code, recorder.Body.String(), *hosts)
		}
	})

	t.Run("an unreachable member", func(t *testing.T) {
		original := forwardTransport
		forwardTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		})
		t.Cleanup(func() { forwardTransport = original })
		c, recorder := newClusterForwardTestContext(http.MethodPost, "/api/cluster/test", `{}`, nil)
		ForwardTo(c, service, "member:8184", nil)
		if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "node_unreachable") {
			t.Fatalf("answer %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("a body over the limit", func(t *testing.T) {
		member := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		defer member.Close()
		c, recorder := newClusterForwardTestContext(http.MethodPost, "/api/cluster/notes", strings.Repeat("x", 64<<10), nil)
		c.Request.Body = http.MaxBytesReader(recorder, c.Request.Body, 4)
		ForwardTo(c, service, strings.TrimPrefix(member.URL, "https://"), nil)
		if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "request_body_too_large") {
			t.Fatalf("answer %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("a node that isn't a member", func(t *testing.T) {
		database := newClusterHandlerTestDB(t, &clusterModels.ClusterNode{})
		for _, node := range []clusterModels.ClusterNode{
			{NodeUUID: "node-b", Hostname: "twin"}, {NodeUUID: "node-c", Hostname: "twin"},
		} {
			if err := database.Create(&node).Error; err != nil {
				t.Fatal(err)
			}
		}
		service := cluster.NewClusterService(database, authForwardStub{}, nil).(*cluster.Service)
		for node, want := range map[string]string{"twin": "node_name_ambiguous", "nobody": "node_not_found"} {
			c, recorder := newClusterForwardTestContext(http.MethodGet, "/api/cluster/backups/events", "", nil)
			ForwardToNode(c, service, node, nil)
			if !strings.Contains(recorder.Body.String(), want) {
				t.Fatalf("%s: answer %d %s, want %s", node, recorder.Code, recorder.Body.String(), want)
			}
		}
	})
}

func TestForwardToLeaderWaitsForTheLeadersAppliedIndex(t *testing.T) {
	service := newTestClusterService(newClusterHandlerTestDB(t, &clusterModels.ClusterNote{}, &clusterModels.ClusterNode{}), "follower-node")
	service.AuthService = authForwardStub{}
	if err := service.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	withRaft(service, newSingleNodeReplicationHandlerRaft(t, service.FSM()))
	leaderIndex := service.AppliedIndex() + 2
	serveForwards(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(appliedIndexHeader, strconv.FormatUint(leaderIndex, 10))
		w.WriteHeader(http.StatusCreated)
	}))

	go func() {
		time.Sleep(200 * time.Millisecond)
		for range 2 {
			_ = service.ProposeNoteCreate(context.Background(), "late-note", "content")
		}
	}()
	c, recorder := newClusterForwardTestContext(http.MethodPost, "/api/cluster/backups/jobs", `{}`, nil)
	forwardToLeader(c, service, nil)
	c.Writer.WriteHeaderNow()
	if applied := service.AppliedIndex(); recorder.Code != http.StatusCreated || applied < leaderIndex {
		t.Fatalf("answered %d at index %d, want 201 once index %d is applied", recorder.Code, applied, leaderIndex)
	}

	leaderIndex = service.AppliedIndex() + 1
	c, recorder = newClusterForwardTestContext(http.MethodGet, "/api/cluster/backups/jobs", "", nil)
	start := time.Now()
	forwardToLeader(c, service, nil)
	c.Writer.WriteHeaderNow()
	if recorder.Code != http.StatusCreated || time.Since(start) > time.Second {
		t.Fatalf("read answered %d after %s", recorder.Code, time.Since(start))
	}
}

func TestStampAppliedIndexReportsTheIndexAfterTheWrite(t *testing.T) {
	memberCert := newJoinTestCert(t)
	pin := cluster.CertPin(memberCert.Leaf)
	database := newClusterHandlerTestDB(t, &clusterModels.ClusterNote{}, &clusterModels.ClusterSSHIdentity{})
	if err := database.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: "member-node", SSHHost: "10.0.0.2", PublicKey: "ssh-ed25519 AAAA", CertPin: hex.EncodeToString(pin[:]),
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := newTestClusterService(database, "leader-node")
	if err := service.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	withRaft(service, newSingleNodeReplicationHandlerRaft(t, service.FSM()))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(StampAppliedIndex(service))
	router.POST("/note", func(c *gin.Context) {
		if err := service.ProposeNoteCreate(c.Request.Context(), "stamped", "content"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"status": "success"})
	})
	handler := service.PeerHandler(router)

	before := service.AppliedIndex()
	request := httptest.NewRequest(http.MethodPost, "/note", nil)
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{memberCert.Leaf}}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	after := service.AppliedIndex()
	if got := recorder.Header().Get(appliedIndexHeader); recorder.Code != http.StatusCreated ||
		after <= before || got != strconv.FormatUint(after, 10) {
		t.Fatalf("status=%d stamp=%q, want 201 stamped %d (was %d)", recorder.Code, got, after, before)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/note", nil))
	if got := recorder.Header().Get(appliedIndexHeader); got != "" {
		t.Fatalf("a browser's request was stamped %q", got)
	}
}

func TestLeaderOnlyRunsTheHandlerOnlyOnTheLeader(t *testing.T) {
	cfg := raft.DefaultConfig()
	cfg.LocalID = "follower-node"
	cfg.Logger = hclog.NewNullLogger()
	_, transport := raft.NewInmemTransport("follower-node")
	followerRaft, err := raft.NewRaft(cfg, nil, raft.NewInmemStore(), raft.NewInmemStore(), raft.NewInmemSnapshotStore(), transport)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = followerRaft.Shutdown().Error() })
	leaderRaft := setupSingleRaftForTest(t, "leader-node")
	t.Cleanup(func() { _ = leaderRaft.Shutdown().Error() })

	for _, test := range []struct {
		name    string
		service *cluster.Service
		status  int
		ran     bool
	}{
		{"leader", withRaft(&cluster.Service{}, leaderRaft), http.StatusNoContent, true},
		{"follower", withRaft(&cluster.Service{}, followerRaft), http.StatusServiceUnavailable, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ran := false
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.POST("/write", LeaderOnly(test.service), func(c *gin.Context) {
				ran = true
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/write", nil))
			if recorder.Code != test.status || ran != test.ran {
				t.Fatalf("status=%d ran=%t, want %d ran=%t", recorder.Code, ran, test.status, test.ran)
			}
		})
	}
}
