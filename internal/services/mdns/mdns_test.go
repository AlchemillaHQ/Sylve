// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdns

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGatherManagedRecordsForAppleSambaShares(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&models.BasicSettings{},
		&mdnsModels.MdnsSettings{},
		&mdnsModels.MdnsRecord{},
		&sambaModels.SambaSettings{},
		&sambaModels.SambaShare{},
	)

	if err := db.Create(&sambaModels.SambaSettings{AppleExtensions: true}).Error; err != nil {
		t.Fatalf("failed to create samba settings: %v", err)
	}
	if err := db.Create(&sambaModels.SambaShare{Name: "documents", Dataset: "dataset-1"}).Error; err != nil {
		t.Fatalf("failed to create samba share: %v", err)
	}
	if err := db.Create(&sambaModels.SambaShare{Name: "backups", Dataset: "dataset-2", TimeMachine: true}).Error; err != nil {
		t.Fatalf("failed to create time machine share: %v", err)
	}

	service := &Service{DB: db}
	records, err := service.gatherManagedRecords()
	if err != nil {
		t.Fatalf("gathering managed records failed: %v", err)
	}

	byType := make(map[string]mdnsModels.MdnsRecord, len(records))
	for _, record := range records {
		byType[record.Type] = record.MdnsRecord
	}

	smb, ok := byType["_smb._tcp"]
	if !ok || smb.Port != 445 {
		t.Fatalf("expected SMB record on port 445, got %+v", smb)
	}
	if smb.Txt == nil || len(smb.Txt) != 0 {
		t.Fatalf("expected SMB TXT to be an empty object, got %#v", smb.Txt)
	}
	payload, err := json.Marshal(smb)
	if err != nil {
		t.Fatalf("failed to marshal SMB record: %v", err)
	}
	if !bytes.Contains(payload, []byte(`"txt":{}`)) {
		t.Fatalf("expected SMB TXT to serialize as an object, got %s", payload)
	}

	device, ok := byType["_device-info._tcp"]
	if !ok || device.Txt["model"] != "RackMac" {
		t.Fatalf("expected Apple device-info record, got %+v", device)
	}

	adisk, ok := byType["_adisk._tcp"]
	if !ok || adisk.Txt["dk0"] != "adVN=backups,adVF=0x82" {
		t.Fatalf("expected Time Machine adisk record, got %+v", adisk)
	}
}
