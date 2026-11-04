// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestReadEventsReportsChangedContainers(t *testing.T) {
	name := filepath.Join(t.TempDir(), "events.log")
	write := func(content string, flag int) {
		file, err := os.OpenFile(name, flag|os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if _, err := file.WriteString(content); err != nil {
			t.Fatal(err)
		}
	}

	complete := `{"Name":"sylve-7","Status":"start","Type":"container","Attributes":{"io.sylve.ocid":"7"}}
{"Name":"sylve-7","Status":"start","Type":"container","Attributes":{"io.sylve.ocid":"7"}}
{"Name":"sylve-8","Status":"exec","Type":"container","Attributes":{"io.sylve.ocid":"8"}}
{"Name":"busybox","Status":"pull","Type":"image"}
`
	write(complete+`{"Name":"sylve-9","Status":"died","Type":"container","Attributes":{"io.sylve.ocid":"9"}}`, os.O_TRUNC)
	offset, changed := readEvents(name, 0)
	if offset != int64(len(complete)) || !maps.Equal(changed, map[uint]bool{7: true}) {
		t.Fatalf("offset %d, changed %v", offset, changed)
	}

	write("\n", os.O_APPEND)
	if offset, changed = readEvents(name, offset); !maps.Equal(changed, map[uint]bool{9: true}) {
		t.Fatalf("partial line not read once complete: %v", changed)
	}

	write(`{"Name":"sylve-3","Status":"remove","Type":"container","Attributes":{"io.sylve.ocid":"3"}}`+"\n", os.O_TRUNC)
	if _, changed = readEvents(name, offset); !maps.Equal(changed, map[uint]bool{3: true}) {
		t.Fatalf("rotated log not reread: %v", changed)
	}
}
