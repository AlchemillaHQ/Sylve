package console

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

func TestExecuteOperationSendsTypedPayload(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "console.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	received := make(chan Request, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		var request Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			serverErr <- err
			return
		}
		received <- request
		serverErr <- json.NewEncoder(conn).Encode(Response{Output: "created\n"})
	}()

	ctid := uint(101)
	request := jailServiceInterfaces.CreateJailRequest{
		Name:          "HAOS-W",
		CTID:          &ctid,
		Pool:          "zroot",
		BootstrapName: "14.2-RELEASE",
		SwitchName:    "none",
		Type:          jailModels.JailTypeFreeBSD,
		Description:   "contains spaces safely",
	}

	output, err := executeOperation(socketPath, OperationJailCreate, JailCreatePayload{
		Request: request,
		JSON:    true,
	})
	if err != nil {
		t.Fatalf("execute operation: %v", err)
	}
	if output != "created\n" {
		t.Fatalf("output = %q, want created response", output)
	}

	envelope := <-received
	if envelope.Operation != OperationJailCreate || envelope.Command != "" {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}

	var payload JailCreatePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Request.CTID == nil || *payload.Request.CTID != ctid {
		t.Fatalf("CTID = %v, want %d", payload.Request.CTID, ctid)
	}
	if payload.Request.Description != request.Description || !payload.JSON {
		t.Fatalf("decoded payload = %#v", payload)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("serve response: %v", err)
	}
}
