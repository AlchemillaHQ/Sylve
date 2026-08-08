// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type selectedNodeUploadRoute struct {
	name string
	path string
}

var selectedNodeUploadRoutes = []selectedNodeUploadRoute{
	{
		name: "file explorer",
		path: "/api/system/file-explorer/upload?path=%2Fvar%2Ftmp",
	},
	{
		name: "downloader",
		path: "/api/utilities/downloader-uploads",
	},
}

type selectedNodeProxyResult struct {
	statusCode int
	body       string
	err        error
}

type selectedNodeCancellationResult struct {
	readErr    error
	contextErr error
}

func performSelectedNodeProxyRequest(
	client *http.Client,
	request *http.Request,
) <-chan selectedNodeProxyResult {
	result := make(chan selectedNodeProxyResult, 1)
	go func() {
		response, err := client.Do(request)
		if err != nil {
			result <- selectedNodeProxyResult{err: err}
			return
		}
		defer response.Body.Close()

		body, readErr := io.ReadAll(response.Body)
		result <- selectedNodeProxyResult{
			statusCode: response.StatusCode,
			body:       string(body),
			err:        readErr,
		}
	}()
	return result
}

func newSelectedNodeUploadProxyRouter(t *testing.T, remoteURL string) *gin.Engine {
	t.Helper()

	database := testutil.NewSQLiteTestDB(t, &clusterModels.ClusterNode{})
	if err := database.Create(&clusterModels.ClusterNode{
		NodeUUID: "remote-node-id",
		Status:   "online",
		Hostname: "remote-node",
		API:      strings.TrimPrefix(remoteURL, "https://"),
	}).Error; err != nil {
		t.Fatalf("seed remote node: %v", err)
	}

	previousHostname := hostname
	hostname = "origin-node"
	t.Cleanup(func() {
		hostname = previousHostname
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(EnsureCorrectHost(database, nil))
	unexpectedLocalHandler := func(c *gin.Context) {
		c.String(http.StatusTeapot, "upload reached origin handler")
	}
	router.POST("/api/system/file-explorer/upload", unexpectedLocalHandler)
	router.DELETE("/api/system/file-explorer/upload", unexpectedLocalHandler)
	router.POST("/api/utilities/downloader-uploads", unexpectedLocalHandler)
	return router
}

func newSelectedNodeMultipartPayload(t *testing.T) ([]byte, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "selected-node.img")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("stream-to-remote-node-"), 4096)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	return append([]byte(nil), body.Bytes()...), writer.FormDataContentType()
}

func TestSelectedNodeUploadsStreamBeforeIngressBodyCompletes(t *testing.T) {
	for _, route := range selectedNodeUploadRoutes {
		t.Run(route.name, func(t *testing.T) {
			payload, contentType := newSelectedNodeMultipartPayload(t)
			split := len(payload) / 2
			prefixRead := make(chan error, 1)
			remoteBody := make(chan []byte, 1)

			remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				prefix := make([]byte, split)
				_, err := io.ReadFull(request.Body, prefix)
				prefixRead <- err
				if err != nil {
					return
				}

				remainder, err := io.ReadAll(request.Body)
				if err != nil {
					remoteBody <- nil
					return
				}
				remoteBody <- append(prefix, remainder...)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"status":"success"}`))
			}))
			defer remote.Close()

			router := newSelectedNodeUploadProxyRouter(t, remote.URL)
			origin := httptest.NewServer(router)
			defer origin.Close()
			reader, writer := io.Pipe()
			request, err := http.NewRequest(http.MethodPost, origin.URL+route.path, reader)
			if err != nil {
				t.Fatalf("create ingress request: %v", err)
			}
			request.ContentLength = int64(len(payload))
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("X-Current-Hostname", "remote-node")

			proxyResult := performSelectedNodeProxyRequest(origin.Client(), request)

			prefixWritten := make(chan error, 1)
			go func() {
				_, err := writer.Write(payload[:split])
				prefixWritten <- err
			}()

			select {
			case err := <-prefixWritten:
				if err != nil {
					t.Fatalf("write first request segment: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("proxy did not consume the first request segment")
			}

			select {
			case err := <-prefixRead:
				if err != nil {
					t.Fatalf("remote read first request segment: %v", err)
				}
			case <-time.After(3 * time.Second):
				_ = writer.CloseWithError(context.DeadlineExceeded)
				t.Fatal("remote did not receive bytes before the ingress body completed")
			}

			if _, err := writer.Write(payload[split:]); err != nil {
				t.Fatalf("write remaining request segment: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close request body: %v", err)
			}

			var response selectedNodeProxyResult
			select {
			case response = <-proxyResult:
			case <-time.After(3 * time.Second):
				t.Fatal("selected-node proxy did not complete")
			}

			if response.err != nil {
				t.Fatalf("selected-node proxy request failed: %v", response.err)
			}
			if response.statusCode != http.StatusCreated ||
				response.body != `{"status":"success"}` {
				t.Fatalf("response=%d body=%q", response.statusCode, response.body)
			}

			select {
			case received := <-remoteBody:
				if !bytes.Equal(received, payload) {
					t.Fatal("remote node did not receive the exact multipart body")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("remote node did not finish reading the multipart body")
			}
		})
	}
}

func TestSelectedNodeUploadCancellationReachesRemoteHandler(t *testing.T) {
	for _, route := range selectedNodeUploadRoutes {
		t.Run(route.name, func(t *testing.T) {
			remoteStarted := make(chan error, 1)
			remoteCancelled := make(chan selectedNodeCancellationResult, 1)

			remote := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				firstByte := make([]byte, 1)
				_, err := io.ReadFull(request.Body, firstByte)
				remoteStarted <- err
				if err != nil {
					return
				}

				_, readErr := io.Copy(io.Discard, request.Body)
				select {
				case <-request.Context().Done():
					remoteCancelled <- selectedNodeCancellationResult{
						readErr:    readErr,
						contextErr: request.Context().Err(),
					}
				case <-time.After(time.Second):
					remoteCancelled <- selectedNodeCancellationResult{
						readErr:    readErr,
						contextErr: fmt.Errorf("remote request context was not cancelled"),
					}
				}
			}))
			defer remote.Close()

			router := newSelectedNodeUploadProxyRouter(t, remote.URL)
			originReturned := make(chan struct{})
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				router.ServeHTTP(w, request)
				close(originReturned)
			}))
			defer origin.Close()

			connection, err := net.Dial("tcp", origin.Listener.Addr().String())
			if err != nil {
				t.Fatalf("connect to ingress server: %v", err)
			}
			if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				_ = connection.Close()
				t.Fatalf("set ingress connection deadline: %v", err)
			}
			if _, err := fmt.Fprintf(
				connection,
				"POST %s HTTP/1.1\r\n"+
					"Host: %s\r\n"+
					"Content-Length: %d\r\n"+
					"Content-Type: multipart/form-data; boundary=selected-node-test\r\n"+
					"X-Current-Hostname: remote-node\r\n"+
					"Connection: close\r\n\r\n"+
					"x",
				route.path,
				origin.Listener.Addr().String(),
				1<<20,
			); err != nil {
				_ = connection.Close()
				t.Fatalf("write ingress request prefix: %v", err)
			}

			select {
			case err := <-remoteStarted:
				if err != nil {
					t.Fatalf("remote did not start reading upload: %v", err)
				}
			case <-time.After(3 * time.Second):
				_ = connection.Close()
				t.Fatal("remote upload handler did not start")
			}

			if err := connection.Close(); err != nil {
				t.Fatalf("close ingress connection: %v", err)
			}

			select {
			case result := <-remoteCancelled:
				if result.readErr == nil {
					t.Fatal("remote upload body ended cleanly after ingress cancellation")
				}
				if result.contextErr != context.Canceled {
					t.Fatalf(
						"remote cancellation error=%v want=%v (body read error: %v)",
						result.contextErr,
						context.Canceled,
						result.readErr,
					)
				}
			case <-time.After(3 * time.Second):
				remote.CloseClientConnections()
				t.Fatal("remote handler did not observe ingress cancellation")
			}

			select {
			case <-originReturned:
			case <-time.After(3 * time.Second):
				t.Fatal("selected-node proxy did not return after cancellation")
			}
		})
	}
}

func TestSelectedNodeUploadRevertProxiesRequestBody(t *testing.T) {
	const requestBody = `{"data":{"uploadId":"opaque-upload-id"}}`
	var remoteMethod string
	var remoteBody string
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		remoteMethod = request.Method
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read remote request body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		remoteBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","message":"file_deleted","error":"","data":null}`))
	}))
	defer remote.Close()

	router := newSelectedNodeUploadProxyRouter(t, remote.URL)
	origin := httptest.NewServer(router)
	defer origin.Close()
	request, err := http.NewRequest(
		http.MethodDelete,
		origin.URL+"/api/system/file-explorer/upload",
		strings.NewReader(requestBody),
	)
	if err != nil {
		t.Fatalf("create revert request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Current-Hostname", "remote-node")

	response, err := origin.Client().Do(request)
	if err != nil {
		t.Fatalf("proxy revert request: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read revert response: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, responseBody)
	}
	if remoteMethod != http.MethodDelete || remoteBody != requestBody {
		t.Fatalf("remote request method=%q body=%q", remoteMethod, remoteBody)
	}
}
