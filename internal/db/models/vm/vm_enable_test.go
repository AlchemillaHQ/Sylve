// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package vmModels

import (
	"encoding/json"
	"testing"
)

func TestStorageUnmarshalEnableDefaultsToTrue(t *testing.T) {
	var storage Storage
	if err := json.Unmarshal([]byte(`{"id":1,"type":"raw"}`), &storage); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if !storage.Enable {
		t.Fatalf("expected enable=true when field is missing")
	}
}

func TestStorageUnmarshalEnableRespectsExplicitValue(t *testing.T) {
	var storage Storage
	if err := json.Unmarshal([]byte(`{"id":1,"type":"raw","enable":false}`), &storage); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if storage.Enable {
		t.Fatalf("expected enable=false when field is explicitly false")
	}
}

func TestVMTemplateStorageUnmarshalEnableDefaultsToTrue(t *testing.T) {
	var storage VMTemplateStorage
	if err := json.Unmarshal([]byte(`{"sourceStorageId":1,"type":"raw"}`), &storage); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if !storage.Enable {
		t.Fatalf("expected enable=true when field is missing")
	}
}

func TestVMTemplateStorageUnmarshalEnableRespectsExplicitValue(t *testing.T) {
	var storage VMTemplateStorage
	if err := json.Unmarshal([]byte(`{"sourceStorageId":1,"type":"raw","enable":false}`), &storage); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if storage.Enable {
		t.Fatalf("expected enable=false when field is explicitly false")
	}
}
