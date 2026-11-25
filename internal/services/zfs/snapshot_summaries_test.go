// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"reflect"
	"testing"
	"time"

	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/vmihailenco/msgpack/v5"
)

func TestParseSnapshotSummaries(t *testing.T) {
	output := "zroot/data@manual-20261124-090507\t1234\t1795500307\t65536\t131072\t-\t0\t3100\n" +
		"tank@daily\t5678\t1795400000\t0\t98304\ttank/clone-a,tank/clone-b\t2\t3000\n" +
		"zroot/data@missing-creation\t91\t-\t1\t2\t\t0\t-\n" +
		"malformed line\n" +
		"zroot/data@old-format\t1\t2\t3\t4\n" +
		"zroot/data@v2-format\t1\t2\t3\t4\t-\t0\n" +
		"zroot/no-snapshot\t1\t2\t3\t4\t-\t0\t5\n"

	summaries := parseSnapshotSummaries(output)
	if len(summaries) != 3 {
		t.Fatalf("parsed %d summaries, want 3: %#v", len(summaries), summaries)
	}

	want := zfsServiceInterfaces.SnapshotSummary{
		Name:       "zroot/data@manual-20261124-090507",
		Dataset:    "zroot/data",
		Snapshot:   "manual-20261124-090507",
		Pool:       "zroot",
		GUID:       "1234",
		CreatedAt:  time.Unix(1795500307, 0).UTC(),
		CreateTXG:  3100,
		Used:       65536,
		Referenced: 131072,
		Clones:     []string{},
	}
	if !reflect.DeepEqual(summaries[0], want) {
		t.Fatalf("first summary = %#v, want %#v", summaries[0], want)
	}
	if summaries[1].Pool != "tank" || summaries[1].Dataset != "tank" || summaries[1].Holds != 2 ||
		!reflect.DeepEqual(summaries[1].Clones, []string{"tank/clone-a", "tank/clone-b"}) {
		t.Fatalf("pool root snapshot parsed as %#v", summaries[1])
	}
	if !summaries[2].CreatedAt.IsZero() || len(summaries[2].Clones) != 0 || summaries[2].CreateTXG != 0 {
		t.Fatalf("missing values parsed as %#v", summaries[2])
	}

	if parsed := parseSnapshotSummaries(""); len(parsed) != 0 {
		t.Fatalf("empty output parsed as %#v", parsed)
	}
}

func TestSnapshotSummariesSurviveCacheEncoding(t *testing.T) {
	summaries := parseSnapshotSummaries("zroot/data@a\t1\t1795500307\t2\t3\tzroot/clone\t1\t42\n")
	encoded, err := msgpack.Marshal(summaries)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []zfsServiceInterfaces.SnapshotSummary
	if err := msgpack.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || !decoded[0].CreatedAt.Equal(summaries[0].CreatedAt) ||
		decoded[0].Name != "zroot/data@a" || decoded[0].Holds != 1 || decoded[0].CreateTXG != 42 ||
		!reflect.DeepEqual(decoded[0].Clones, []string{"zroot/clone"}) {
		t.Fatalf("decoded %#v, want %#v", decoded, summaries)
	}
}
