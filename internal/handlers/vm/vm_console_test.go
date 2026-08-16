// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

type vmConsoleTestFrame struct {
	messageType int
	payload     []byte
}

func newVMConsoleTestObserver(username string) (*VMObserver, *[]vmConsoleTestFrame) {
	frames := make([]vmConsoleTestFrame, 0)
	observer := &VMObserver{
		Username: username,
		writeMessageOverride: func(messageType int, payload []byte) error {
			frames = append(frames, vmConsoleTestFrame{
				messageType: messageType,
				payload:     append([]byte(nil), payload...),
			})
			return nil
		},
		closeOverride: func() error { return nil },
	}
	return observer, &frames
}

func lastVMConsoleControlState(t *testing.T, frames []vmConsoleTestFrame) vmConsoleControlState {
	t.Helper()

	for index := len(frames) - 1; index >= 0; index-- {
		if frames[index].messageType != websocket.TextMessage {
			continue
		}

		var state vmConsoleControlState
		if err := json.Unmarshal(frames[index].payload, &state); err != nil {
			t.Fatalf("decode control state: %v", err)
		}
		if state.Type == "control-state" {
			return state
		}
	}

	t.Fatal("no control-state frame found")
	return vmConsoleControlState{}
}

func lastVMConsoleBinaryFrame(t *testing.T, frames []vmConsoleTestFrame) []byte {
	t.Helper()

	for index := len(frames) - 1; index >= 0; index-- {
		if frames[index].messageType == websocket.BinaryMessage {
			return frames[index].payload
		}
	}

	t.Fatal("no binary frame found")
	return nil
}

func newVMConsoleTestSession() (*VMSession, *VMSessionManager) {
	session := &VMSession{
		ID:           "vm-console-107",
		Observers:    make(map[*VMObserver]struct{}),
		History:      make([]byte, 0, 16384),
		HistoryLimit: 16384,
	}
	manager := &VMSessionManager{sessions: map[string]*VMSession{session.ID: session}}
	return session, manager
}

type vmConsoleHandlerStub struct {
	vm        vmModels.VM
	vmErr     error
	allowed   bool
	guardErr  error
	domain    *libvirtServiceInterfaces.LvDomain
	domainErr error
	vmRID     uint
	guardRID  uint
	domainRID uint
}

func (s *vmConsoleHandlerStub) GetVMByRID(rid uint) (vmModels.VM, error) {
	s.vmRID = rid
	return s.vm, s.vmErr
}

func (s *vmConsoleHandlerStub) CanMutateProtectedVM(rid uint) (bool, error) {
	s.guardRID = rid
	return s.allowed, s.guardErr
}

func (s *vmConsoleHandlerStub) GetLvDomain(
	rid uint,
) (*libvirtServiceInterfaces.LvDomain, error) {
	s.domainRID = rid
	return s.domain, s.domainErr
}

func performVMConsoleRequest(
	t *testing.T,
	path string,
	service *vmConsoleHandlerStub,
) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/vm/:rid/console", HandleLibvirtTerminalWebsocket(service))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestParseVMConsoleRequest(t *testing.T) {
	request, err := parseVMConsoleRequest("107", "")
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	if request.RID != 107 ||
		request.RIDText != "107" ||
		request.BaudRate != vmConsoleDefaultBaud ||
		request.DevicePath != "/dev/nmdm107B" {
		t.Fatalf("request = %+v", request)
	}

	request, err = parseVMConsoleRequest("000107", "9600")
	if err != nil {
		t.Fatalf("parse normalized request: %v", err)
	}
	if request.RIDText != "107" || request.BaudRate != "9600" {
		t.Fatalf("normalized request = %+v", request)
	}
}

func TestParseVMConsoleRequestRejectsInvalidIdentityAndBaudRate(t *testing.T) {
	tests := []struct {
		name     string
		rid      string
		baudRate string
		wantCode string
	}{
		{name: "zero RID", rid: "0", wantCode: "invalid_rid_format"},
		{name: "negative RID", rid: "-1", wantCode: "invalid_rid_format"},
		{name: "non-numeric RID", rid: "vm", wantCode: "invalid_rid_format"},
		{name: "RID overflow", rid: "4294967296", wantCode: "invalid_rid_format"},
		{name: "non-numeric baud", rid: "107", baudRate: "fast", wantCode: "invalid_baud_rate"},
		{name: "baud below minimum", rid: "107", baudRate: "49", wantCode: "invalid_baud_rate"},
		{name: "baud above maximum", rid: "107", baudRate: "4000001", wantCode: "invalid_baud_rate"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseVMConsoleRequest(test.rid, test.baudRate)
			var validationErr *vmConsoleValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %v, want validation error", err)
			}
			if validationErr.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", validationErr.Code, test.wantCode)
			}
		})
	}
}

func TestVMDomainSupportsConsoleOnlyForRuntimeStates(t *testing.T) {
	for _, status := range []string{"running", "BLOCKED", " paused ", "shutdown", "pmsuspended"} {
		if !vmDomainSupportsConsole(status) {
			t.Fatalf("status %q should support console", status)
		}
	}
	for _, status := range []string{"", "nostate", "shutoff", "crashed", "orphan"} {
		if vmDomainSupportsConsole(status) {
			t.Fatalf("status %q should not support console", status)
		}
	}
}

func TestVMConsoleHandlerReturnsStandardPreUpgradeErrors(t *testing.T) {
	originalDeviceStat := vmConsoleDeviceStat
	vmConsoleDeviceStat = func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() {
		vmConsoleDeviceStat = originalDeviceStat
	})

	tests := []struct {
		name       string
		path       string
		configure  func(*vmConsoleHandlerStub)
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid RID",
			path:       "/vm/0/console",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_rid_format",
		},
		{
			name: "missing VM",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.vmErr = gorm.ErrRecordNotFound
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "vm_not_found",
		},
		{
			name: "VM lookup failure",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.vmErr = errors.New("database unavailable")
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "failed_to_get_vm",
		},
		{
			name: "serial disabled",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.vm.Serial = false
			},
			wantStatus: http.StatusConflict,
			wantCode:   "vm_serial_console_disabled",
		},
		{
			name: "ownership guard unavailable",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.guardErr = errors.New("replication database unavailable")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "vm_console_guard_unavailable",
		},
		{
			name: "replication lease not owned",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.allowed = false
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "replication_lease_not_owned",
		},
		{
			name: "domain not defined",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.domainErr = errors.New("domain not found")
			},
			wantStatus: http.StatusConflict,
			wantCode:   "vm_domain_not_defined",
		},
		{
			name: "libvirt unavailable",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.domainErr = errors.New("connection refused")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "libvirt_connection_unavailable",
		},
		{
			name: "nil domain",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.domain = nil
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "libvirt_connection_unavailable",
		},
		{
			name: "VM is powered off",
			path: "/vm/107/console",
			configure: func(service *vmConsoleHandlerStub) {
				service.domain.Status = "shutoff"
			},
			wantStatus: http.StatusConflict,
			wantCode:   "vm_console_requires_running_vm",
		},
		{
			name:       "serial device unavailable",
			path:       "/vm/107/console",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "vm_serial_device_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &vmConsoleHandlerStub{
				vm:      vmModels.VM{RID: 107, Serial: true},
				allowed: true,
				domain:  &libvirtServiceInterfaces.LvDomain{Name: "107", Status: "running"},
			}
			if test.configure != nil {
				test.configure(service)
			}

			recorder := performVMConsoleRequest(t, test.path, service)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			response := decodeVMReadEnvelope(t, recorder)
			if response.Status != "error" || response.Message != test.wantCode {
				t.Fatalf("response = %+v, want code %q", response, test.wantCode)
			}
			if len(response.Data) == 0 || string(response.Data) != "null" {
				t.Fatalf("error data = %s, want null", response.Data)
			}
		})
	}
}

func TestVMSessionSharesOutputWithOneConnectionController(t *testing.T) {
	session, manager := newVMConsoleTestSession()
	session.History = append(session.History, []byte("recent history")...)

	first, firstFrames := newVMConsoleTestObserver("alice")
	second, secondFrames := newVMConsoleTestObserver("bob")
	if err := session.AddObserverAndReplay(first, manager); err != nil {
		t.Fatalf("add first observer: %v", err)
	}
	if err := session.AddObserverAndReplay(second, manager); err != nil {
		t.Fatalf("add second observer: %v", err)
	}

	if session.Controller != first {
		t.Fatal("first observer did not retain initial control")
	}
	if first.JoinSequence != 1 || second.JoinSequence != 2 {
		t.Fatalf("join sequences = (%d, %d), want (1, 2)", first.JoinSequence, second.JoinSequence)
	}

	firstState := lastVMConsoleControlState(t, *firstFrames)
	secondState := lastVMConsoleControlState(t, *secondFrames)
	if !firstState.HasControl || secondState.HasControl {
		t.Fatalf("control states = first:%+v second:%+v", firstState, secondState)
	}
	if firstState.ControllerUsername != "alice" || secondState.ControllerUsername != "alice" {
		t.Fatalf("controller usernames = (%q, %q), want alice", firstState.ControllerUsername, secondState.ControllerUsername)
	}
	if firstState.ObserverCount != 2 || secondState.ObserverCount != 2 {
		t.Fatalf("observer counts = (%d, %d), want 2", firstState.ObserverCount, secondState.ObserverCount)
	}
	if got := string(lastVMConsoleBinaryFrame(t, *firstFrames)); got != "recent history" {
		t.Fatalf("first history = %q", got)
	}
	if got := string(lastVMConsoleBinaryFrame(t, *secondFrames)); got != "recent history" {
		t.Fatalf("second history = %q", got)
	}

	session.BroadcastBinary([]byte("live output"), manager)
	if got := string(lastVMConsoleBinaryFrame(t, *firstFrames)); got != "live output" {
		t.Fatalf("first live output = %q", got)
	}
	if got := string(lastVMConsoleBinaryFrame(t, *secondFrames)); got != "live output" {
		t.Fatalf("second live output = %q", got)
	}
}

func TestVMSessionTreatsSameUsernameConnectionsSeparately(t *testing.T) {
	session, manager := newVMConsoleTestSession()
	first, firstFrames := newVMConsoleTestObserver("alice")
	second, secondFrames := newVMConsoleTestObserver("alice")

	if err := session.AddObserverAndReplay(first, manager); err != nil {
		t.Fatalf("add first observer: %v", err)
	}
	if err := session.AddObserverAndReplay(second, manager); err != nil {
		t.Fatalf("add second observer: %v", err)
	}

	firstState := lastVMConsoleControlState(t, *firstFrames)
	secondState := lastVMConsoleControlState(t, *secondFrames)
	if !firstState.HasControl || secondState.HasControl {
		t.Fatalf("same-user control states = first:%+v second:%+v", firstState, secondState)
	}
	if firstState.ObserverCount != 2 || secondState.ObserverCount != 2 {
		t.Fatalf("same-user observer counts = (%d, %d), want 2", firstState.ObserverCount, secondState.ObserverCount)
	}
}

func TestVMSessionTakeoverGatesControllerActionsAndLastRequestWins(t *testing.T) {
	session, manager := newVMConsoleTestSession()
	first, firstFrames := newVMConsoleTestObserver("alice")
	second, secondFrames := newVMConsoleTestObserver("bob")
	third, thirdFrames := newVMConsoleTestObserver("carol")
	for _, observer := range []*VMObserver{first, second, third} {
		if err := session.AddObserverAndReplay(observer, manager); err != nil {
			t.Fatalf("add observer %q: %v", observer.Username, err)
		}
	}

	actionCount := 0
	allowed, err := session.RunControllerAction(second, func(*os.File) error {
		actionCount++
		return nil
	})
	if err != nil || allowed || actionCount != 0 {
		t.Fatalf("viewer action = allowed:%t count:%d err:%v", allowed, actionCount, err)
	}
	allowed, err = session.RunControllerAction(first, func(*os.File) error {
		actionCount++
		return nil
	})
	if err != nil || !allowed || actionCount != 1 {
		t.Fatalf("controller action = allowed:%t count:%d err:%v", allowed, actionCount, err)
	}

	if !session.TakeControl(second, manager) {
		t.Fatal("second observer takeover was rejected")
	}
	if !session.TakeControl(third, manager) {
		t.Fatal("third observer takeover was rejected")
	}
	if session.Controller != third {
		t.Fatal("last processed takeover did not win")
	}

	for name, frames := range map[string]*[]vmConsoleTestFrame{
		"alice": firstFrames,
		"bob":   secondFrames,
	} {
		state := lastVMConsoleControlState(t, *frames)
		if state.HasControl || state.ControllerUsername != "carol" {
			t.Fatalf("%s state after takeover = %+v", name, state)
		}
	}
	thirdState := lastVMConsoleControlState(t, *thirdFrames)
	if !thirdState.HasControl || thirdState.ControllerUsername != "carol" {
		t.Fatalf("carol state after takeover = %+v", thirdState)
	}

	allowed, err = session.RunControllerAction(first, func(*os.File) error {
		actionCount++
		return nil
	})
	if err != nil || allowed || actionCount != 1 {
		t.Fatalf("demoted action = allowed:%t count:%d err:%v", allowed, actionCount, err)
	}
	allowed, err = session.RunControllerAction(third, func(*os.File) error {
		actionCount++
		return nil
	})
	if err != nil || !allowed || actionCount != 2 {
		t.Fatalf("new controller action = allowed:%t count:%d err:%v", allowed, actionCount, err)
	}
}

func TestVMSessionPromotesOldestViewerWhenControllerLeaves(t *testing.T) {
	session, manager := newVMConsoleTestSession()
	first, _ := newVMConsoleTestObserver("alice")
	second, secondFrames := newVMConsoleTestObserver("bob")
	third, thirdFrames := newVMConsoleTestObserver("carol")
	for _, observer := range []*VMObserver{first, second, third} {
		if err := session.AddObserverAndReplay(observer, manager); err != nil {
			t.Fatalf("add observer %q: %v", observer.Username, err)
		}
	}

	session.RemoveObserver(first, manager)
	if session.Controller != second {
		t.Fatal("oldest remaining viewer was not promoted")
	}
	secondState := lastVMConsoleControlState(t, *secondFrames)
	thirdState := lastVMConsoleControlState(t, *thirdFrames)
	if !secondState.HasControl || thirdState.HasControl {
		t.Fatalf("promoted states = second:%+v third:%+v", secondState, thirdState)
	}
	if secondState.ControllerUsername != "bob" || secondState.ObserverCount != 2 {
		t.Fatalf("promoted controller state = %+v", secondState)
	}

	session.RemoveObserver(third, manager)
	if session.Controller != second {
		t.Fatal("viewer disconnect changed controller ownership")
	}
	secondState = lastVMConsoleControlState(t, *secondFrames)
	if !secondState.HasControl || secondState.ObserverCount != 1 {
		t.Fatalf("controller state after viewer disconnect = %+v", secondState)
	}
}

func TestVMSessionLastObserverCleanupRemovesManagerEntry(t *testing.T) {
	manager := &VMSessionManager{sessions: make(map[string]*VMSession)}
	observer := &VMObserver{}
	session := &VMSession{
		ID:        "vm-console-107",
		Observers: map[*VMObserver]struct{}{observer: {}},
	}
	manager.sessions[session.ID] = session

	session.RemoveObserver(observer, manager)

	if !session.IsClosed() {
		t.Fatal("session remained open after its last observer disconnected")
	}
	if _, exists := manager.sessions[session.ID]; exists {
		t.Fatal("closed session remained registered")
	}
}

func TestVMSessionHistoryIsBounded(t *testing.T) {
	session := &VMSession{
		ID:           "vm-console-107",
		Observers:    make(map[*VMObserver]struct{}),
		HistoryLimit: 4,
	}

	session.BroadcastBinary([]byte("abcdef"), &VMSessionManager{
		sessions: make(map[string]*VMSession),
	})

	if string(session.History) != "cdef" {
		t.Fatalf("history = %q, want cdef", session.History)
	}
}
