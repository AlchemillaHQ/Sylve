// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

func TestParseListPayloadToValuesAcceptsIPAndCIDRLines(t *testing.T) {
	payload := strings.Join([]string{
		"# comment",
		"1.1.1.1",
		"2001:db8::1",
		"10.0.0.0/24",
		"2001:db8::/64",
		"1.1.1.1",
	}, "\n")

	values, err := parseListPayloadToValues(payload)
	if err != nil {
		t.Fatalf("expected payload to parse, got error: %v", err)
	}

	if len(values) != 4 {
		t.Fatalf("expected 4 unique values, got %d (%v)", len(values), values)
	}

	expected := map[string]bool{
		"1.1.1.1":       true,
		"2001:db8::1":   true,
		"10.0.0.0/24":   true,
		"2001:db8::/64": true,
	}
	for _, value := range values {
		if !expected[value] {
			t.Fatalf("unexpected parsed value: %s", value)
		}
	}
}

func TestParseListPayloadToValuesRejectsUnsupportedLine(t *testing.T) {
	_, err := parseListPayloadToValues("not-a-supported-value")
	if err == nil {
		t.Fatal("expected unsupported list line to return an error")
	}
	if !strings.Contains(err.Error(), "unsupported_list_line") {
		t.Fatalf("expected unsupported_list_line error, got: %v", err)
	}
}

func TestRefreshDynamicObjectsSkipsUntilRefreshInterval(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	now := time.Now().UTC()
	obj := networkModels.Object{
		Name:                   "fqdn-skip",
		Type:                   "FQDN",
		AutoUpdate:             true,
		RefreshIntervalSeconds: 3600,
		LastRefreshAt:          &now,
		Entries: []networkModels.ObjectEntry{
			{Value: "does-not-exist.invalid"},
		},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	changed, err := svc.RefreshDynamicObjects()
	if err != nil {
		t.Fatalf("expected refresh to skip object without error, got: %v", err)
	}
	if changed {
		t.Fatal("expected no changes when object refresh interval has not elapsed")
	}
}

func TestRefreshObjectResolutionsFQDNFailureKeepsPreviousResolutions(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	obj := networkModels.Object{
		Name:                   "fqdn-failure",
		Type:                   "FQDN",
		AutoUpdate:             true,
		RefreshIntervalSeconds: 300,
		Entries: []networkModels.ObjectEntry{
			{Value: "does-not-exist.invalid"},
		},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	oldResolution := networkModels.ObjectResolution{
		ObjectID:      obj.ID,
		ResolvedIP:    "203.0.113.20",
		ResolvedValue: "203.0.113.20",
	}
	if err := db.Create(&oldResolution).Error; err != nil {
		t.Fatalf("failed to seed old resolution: %v", err)
	}

	var loaded networkModels.Object
	if err := db.Preload("Entries").Preload("Resolutions").First(&loaded, obj.ID).Error; err != nil {
		t.Fatalf("failed to load object: %v", err)
	}

	changed, err := svc.refreshObjectResolutions(&loaded)
	if err == nil {
		t.Fatal("expected fqdn lookup failure to return an error")
	}
	if changed {
		t.Fatal("expected changed=false when refresh fails")
	}

	var resolutions []networkModels.ObjectResolution
	if err := db.Where("object_id = ?", obj.ID).Find(&resolutions).Error; err != nil {
		t.Fatalf("failed to load object resolutions: %v", err)
	}

	if len(resolutions) != 1 {
		t.Fatalf("expected previous resolution to remain, got %d", len(resolutions))
	}
	if resolutions[0].ResolvedValue != "203.0.113.20" {
		t.Fatalf("expected previous resolution to be retained, got %q", resolutions[0].ResolvedValue)
	}
}

func TestRefreshObjectResolutionsListLargePayloadUsesBatchedInsert(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	const totalValues = 1500
	lines := make([]string, 0, totalValues)
	for i := 0; i < totalValues; i++ {
		lines = append(lines, "10.200."+strconv.Itoa(i/256)+"."+strconv.Itoa(i%256))
	}

	payload := strings.Join(lines, "\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, payload)
	}))
	defer server.Close()

	obj := networkModels.Object{
		Name:                   "list-large",
		Type:                   "List",
		AutoUpdate:             true,
		RefreshIntervalSeconds: 300,
		Entries: []networkModels.ObjectEntry{
			{Value: server.URL},
		},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	var loaded networkModels.Object
	if err := db.Preload("Entries").Preload("Resolutions").First(&loaded, obj.ID).Error; err != nil {
		t.Fatalf("failed to load object: %v", err)
	}

	changed, err := svc.refreshObjectResolutions(&loaded)
	if err != nil {
		t.Fatalf("expected large list refresh to succeed, got: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true for first refresh")
	}

	var resolutions []networkModels.ObjectResolution
	if err := db.Where("object_id = ?", obj.ID).Find(&resolutions).Error; err != nil {
		t.Fatalf("failed to load object resolutions: %v", err)
	}
	if len(resolutions) != 0 {
		t.Fatalf("expected list resolutions table to stay empty, got %d rows", len(resolutions))
	}

	snapshotValues, err := svc.loadListSnapshotValues(obj.ID)
	if err != nil {
		t.Fatalf("failed to load list snapshot values: %v", err)
	}
	if len(snapshotValues) != totalValues {
		t.Fatalf("expected %d snapshot values, got %d", totalValues, len(snapshotValues))
	}
}

func TestRefreshObjectResolutionsSkipsRewriteWhenChecksumMatches(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.44\n"))
	}))
	defer server.Close()

	checksum := objectValuesChecksum([]string{"203.0.113.44"})
	obj := networkModels.Object{
		Name:                   "list-checksum-stable",
		Type:                   "List",
		AutoUpdate:             true,
		RefreshIntervalSeconds: 300,
		ResolutionChecksum:     checksum,
		Entries: []networkModels.ObjectEntry{
			{Value: server.URL},
		},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	row := networkModels.ObjectResolution{
		ObjectID:      obj.ID,
		ResolvedIP:    "203.0.113.44",
		ResolvedValue: "203.0.113.44",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("failed to seed object resolution: %v", err)
	}
	originalRowID := row.ID

	var loaded networkModels.Object
	if err := db.Preload("Entries").First(&loaded, obj.ID).Error; err != nil {
		t.Fatalf("failed to load object: %v", err)
	}

	changed, err := svc.refreshObjectResolutions(&loaded)
	if err != nil {
		t.Fatalf("expected refresh to succeed, got: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false when checksum matches")
	}

	var rows []networkModels.ObjectResolution
	if err := db.Where("object_id = ?", obj.ID).Order("id asc").Find(&rows).Error; err != nil {
		t.Fatalf("failed to load object resolutions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one resolution row to remain, got %d", len(rows))
	}
	if rows[0].ID != originalRowID {
		t.Fatalf("expected resolution row to remain unchanged, old_id=%d new_id=%d", originalRowID, rows[0].ID)
	}
}

func TestRefreshObjectResolutionsBackfillsLegacyChecksumWithoutRewrite(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("198.51.100.77\n"))
	}))
	defer server.Close()

	obj := networkModels.Object{
		Name:                   "list-checksum-legacy",
		Type:                   "List",
		AutoUpdate:             true,
		RefreshIntervalSeconds: 300,
		ResolutionChecksum:     "",
		Entries: []networkModels.ObjectEntry{
			{Value: server.URL},
		},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	row := networkModels.ObjectResolution{
		ObjectID:      obj.ID,
		ResolvedIP:    "198.51.100.77",
		ResolvedValue: "198.51.100.77",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("failed to seed object resolution: %v", err)
	}
	originalRowID := row.ID

	var loaded networkModels.Object
	if err := db.Preload("Entries").First(&loaded, obj.ID).Error; err != nil {
		t.Fatalf("failed to load object: %v", err)
	}

	changed, err := svc.refreshObjectResolutions(&loaded)
	if err != nil {
		t.Fatalf("expected refresh to succeed, got: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false for legacy checksum backfill path")
	}

	var rows []networkModels.ObjectResolution
	if err := db.Where("object_id = ?", obj.ID).Order("id asc").Find(&rows).Error; err != nil {
		t.Fatalf("failed to load object resolutions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one resolution row to remain, got %d", len(rows))
	}
	if rows[0].ID != originalRowID {
		t.Fatalf("expected resolution row to remain unchanged, old_id=%d new_id=%d", originalRowID, rows[0].ID)
	}

	var updated networkModels.Object
	if err := db.First(&updated, obj.ID).Error; err != nil {
		t.Fatalf("failed to load updated object: %v", err)
	}
	expected := objectValuesChecksum([]string{"198.51.100.77"})
	if updated.ResolutionChecksum != expected {
		t.Fatalf("expected legacy checksum backfill to %q, got %q", expected, updated.ResolutionChecksum)
	}
}

func TestRefreshObjectResolutionsListSkipsParseWhenSourceChecksumMatches(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	payload := "not-a-supported-value\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	sourceChecksum := listSourceChecksum([]string{listSourceToken(server.URL, payload)})
	obj := networkModels.Object{
		Name:                   "list-source-checksum-stable",
		Type:                   "List",
		AutoUpdate:             true,
		RefreshIntervalSeconds: 300,
		SourceChecksum:         sourceChecksum,
		ResolutionChecksum:     "existing-resolution-checksum",
		Entries: []networkModels.ObjectEntry{
			{Value: server.URL},
		},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	row := networkModels.ObjectResolution{
		ObjectID:      obj.ID,
		ResolvedIP:    "203.0.113.88",
		ResolvedValue: "203.0.113.88",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("failed to seed object resolution: %v", err)
	}
	originalRowID := row.ID

	var loaded networkModels.Object
	if err := db.Preload("Entries").First(&loaded, obj.ID).Error; err != nil {
		t.Fatalf("failed to load object: %v", err)
	}

	changed, err := svc.refreshObjectResolutions(&loaded)
	if err != nil {
		t.Fatalf("expected refresh to short-circuit on source checksum match, got: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false when source checksum matches")
	}

	var rows []networkModels.ObjectResolution
	if err := db.Where("object_id = ?", obj.ID).Order("id asc").Find(&rows).Error; err != nil {
		t.Fatalf("failed to load object resolutions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one resolution row to remain, got %d", len(rows))
	}
	if rows[0].ID != originalRowID {
		t.Fatalf("expected resolution row to remain unchanged, old_id=%d new_id=%d", originalRowID, rows[0].ID)
	}
}

func TestRenderTrafficRulesSplitsMixedFamilies(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallTrafficRule{
		{
			ID:        11,
			Enabled:   true,
			Log:       true,
			Priority:  1000,
			Action:    "pass",
			Direction: "in",
			Protocol:  "any",
			Family:    "any",
			SourceObj: &networkModels.Object{
				ID:   42,
				Type: "List",
				Resolutions: []networkModels.ObjectResolution{
					{ResolvedValue: "10.0.0.0/24"},
					{ResolvedValue: "2001:db8::/64"},
				},
			},
		},
	}

	tables := buildFirewallObjectTables(rules, nil)
	rendered, err := svc.renderTrafficRules(rules, tables)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if !strings.Contains(rendered, "pass in log inet from <sylve_obj_42_inet> to any") {
		t.Fatalf("expected inet line in rendered rules, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "pass in log inet6 from <sylve_obj_42_inet6> to any") {
		t.Fatalf("expected inet6 line in rendered rules, got:\n%s", rendered)
	}
}

func TestRenderTrafficRulesIncludesQuickKeyword(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallTrafficRule{
		{
			Enabled:   true,
			Log:       true,
			Quick:     true,
			Priority:  1000,
			Action:    "block",
			Direction: "in",
			Protocol:  "any",
			Family:    "inet6",
			SourceRaw: "2001:db8::1",
			DestRaw:   "any",
		},
	}

	rendered, err := svc.renderTrafficRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if !strings.Contains(rendered, "block in log quick inet6 from 2001:db8::1 to any") {
		t.Fatalf("expected quick keyword in rendered rule, got:\n%s", rendered)
	}
}

func TestRenderTrafficRulesOmitsLogKeywordWhenLogDisabled(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallTrafficRule{
		{
			Enabled:   true,
			Log:       false,
			Quick:     true,
			Priority:  1000,
			Action:    "pass",
			Direction: "in",
			Protocol:  "any",
			Family:    "inet",
			SourceRaw: "198.51.100.11",
			DestRaw:   "any",
		},
	}

	rendered, err := svc.renderTrafficRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if strings.Contains(rendered, " pass in log ") {
		t.Fatalf("did not expect log keyword for log-disabled traffic rule, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "pass in quick inet from 198.51.100.11 to any") {
		t.Fatalf("expected traffic rule rendering without log keyword, got:\n%s", rendered)
	}
}

func TestRenderTrafficRulesUsesIngressAndEgressInterfaces(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallTrafficRule{
		{
			Enabled:           true,
			Log:               true,
			Quick:             true,
			Priority:          1000,
			Action:            "pass",
			Direction:         "in",
			Protocol:          "tcp",
			Family:            "inet",
			IngressInterfaces: []string{"em0"},
			SourceRaw:         "192.0.2.10",
			DestRaw:           "any",
			DstPortsRaw:       "443",
		},
		{
			Enabled:          true,
			Log:              true,
			Quick:            false,
			Priority:         1010,
			Action:           "pass",
			Direction:        "out",
			Protocol:         "tcp",
			Family:           "inet",
			EgressInterfaces: []string{"bridge0"},
			SourceRaw:        "192.0.2.10",
			DestRaw:          "any",
			DstPortsRaw:      "443",
		},
	}

	rendered, err := svc.renderTrafficRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	expectedInbound := "pass in log quick on em0 inet proto tcp from 192.0.2.10 to any port 443"
	if !strings.Contains(rendered, expectedInbound) {
		t.Fatalf("expected ingress interface rendering for inbound rule, got:\n%s", rendered)
	}
	expectedOutbound := "pass out log on bridge0 inet proto tcp from 192.0.2.10 to any port 443"
	if !strings.Contains(rendered, expectedOutbound) {
		t.Fatalf("expected egress interface rendering for outbound rule, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `label "sylve_trf_`) {
		t.Fatalf("expected rendered traffic rules to include deterministic labels, got:\n%s", rendered)
	}
}

func TestParseTrafficRuleCountersFromPFAggregatesByLabel(t *testing.T) {
	output := strings.Join([]string{
		`@101 block in quick inet from 10.0.0.2 to any label "sylve_trf_11"`,
		`  [ Evaluations: 88      Packets: 9         Bytes: 3000        States: 0     ]`,
		`@102 block in quick inet6 from 2001:db8::2 to any label "sylve_trf_11"`,
		`  [ Evaluations: 44      Packets: 3         Bytes: 900         States: 0     ]`,
		`@103 pass in inet from any to any label "other_rule"`,
		`  [ Evaluations: 1       Packets: 999       Bytes: 99999       States: 0     ]`,
		`@104 pass out inet from any to any label "sylve_trf_12"`,
		`  [ Evaluations: 12      Packets: 5         Bytes: 1024        States: 1     ]`,
	}, "\n")

	counters := parseTrafficRuleCountersFromPF(output)

	if len(counters) != 2 {
		t.Fatalf("expected 2 labeled counter entries, got %d (%v)", len(counters), counters)
	}

	if counters[11].Packets != 12 || counters[11].Bytes != 3900 {
		t.Fatalf("expected aggregated counters for rule 11 to be packets=12 bytes=3900, got packets=%d bytes=%d", counters[11].Packets, counters[11].Bytes)
	}
	if counters[12].Packets != 5 || counters[12].Bytes != 1024 {
		t.Fatalf("expected counters for rule 12 to be packets=5 bytes=1024, got packets=%d bytes=%d", counters[12].Packets, counters[12].Bytes)
	}
}

func TestParseLabeledRuleCountersMapsRuleNumberZero(t *testing.T) {
	output := strings.Join([]string{
		`@0 block in quick on bridge0 inet from any to any label "sylve_trf_12"`,
		`  [ Evaluations: 4       Packets: 2         Bytes: 236         States: 0     ]`,
	}, "\n")

	counters, ruleNumbers := parseLabeledRuleCounters(output, pfTrafficRuleLabelPattern)
	if counters[12].Packets != 2 || counters[12].Bytes != 236 {
		t.Fatalf("unexpected counters for rule 12 from @0 mapping: %+v", counters[12])
	}
	if ruleNumbers[0] != 12 {
		t.Fatalf("expected rule number 0 to map to rule id 12, got mapping: %v", ruleNumbers)
	}
}

func TestGetFirewallTrafficRuleCountersReturnsZerosWhenPFUnavailable(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})

	rule := networkModels.FirewallTrafficRule{
		ID:        501,
		Name:      "block-test",
		Enabled:   true,
		Priority:  1,
		Action:    "block",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
		SourceRaw: "any",
		DestRaw:   "any",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to create traffic rule: %v", err)
	}

	previousRunCommand := firewallRunCommand
	firewallRunCommand = func(command string, args ...string) (string, error) {
		if command == "/sbin/pfctl" && len(args) > 0 && args[0] == "-si" {
			return "", fmt.Errorf("pf disabled")
		}
		return "", nil
	}
	t.Cleanup(func() {
		firewallRunCommand = previousRunCommand
	})

	counters, err := svc.GetFirewallTrafficRuleCounters()
	if err != nil {
		t.Fatalf("expected counters to succeed when pf unavailable, got: %v", err)
	}
	if len(counters) != 1 {
		t.Fatalf("expected one counter row, got %d", len(counters))
	}
	if counters[0].ID != rule.ID {
		t.Fatalf("expected counter row for rule id %d, got %d", rule.ID, counters[0].ID)
	}
	if counters[0].Packets != 0 || counters[0].Bytes != 0 {
		t.Fatalf("expected zero counters when pf unavailable, got packets=%d bytes=%d", counters[0].Packets, counters[0].Bytes)
	}
}

func TestGetFirewallTrafficRuleCountersParsesPFOutput(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})

	ruleA := networkModels.FirewallTrafficRule{
		ID:        601,
		Name:      "allow-a",
		Enabled:   true,
		Priority:  1,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
		SourceRaw: "any",
		DestRaw:   "any",
	}
	ruleB := networkModels.FirewallTrafficRule{
		ID:        602,
		Name:      "allow-b",
		Enabled:   true,
		Priority:  2,
		Action:    "pass",
		Direction: "out",
		Protocol:  "any",
		Family:    "any",
		SourceRaw: "any",
		DestRaw:   "any",
	}
	if err := db.Create(&ruleA).Error; err != nil {
		t.Fatalf("failed to create traffic rule A: %v", err)
	}
	if err := db.Create(&ruleB).Error; err != nil {
		t.Fatalf("failed to create traffic rule B: %v", err)
	}

	sampleOutput := strings.Join([]string{
		`@10 pass in inet from any to any label "sylve_trf_601"`,
		`  [ Evaluations: 9       Packets: 7         Bytes: 4096        States: 2     ]`,
		`@11 pass out inet from any to any label "sylve_trf_602"`,
		`  [ Evaluations: 12      Packets: 3         Bytes: 512         States: 1     ]`,
	}, "\n")

	previousRunCommand := firewallRunCommand
	firewallRunCommand = func(command string, args ...string) (string, error) {
		if command == "/sbin/pfctl" && len(args) > 0 && args[0] == "-si" {
			return "Status: Enabled", nil
		}
		if command == "/sbin/pfctl" && len(args) == 3 && args[0] == "-a" && args[1] == "sylve/traffic-rules" && args[2] == "-vvsr" {
			return sampleOutput, nil
		}
		return "", nil
	}
	t.Cleanup(func() {
		firewallRunCommand = previousRunCommand
	})

	counters, err := svc.GetFirewallTrafficRuleCounters()
	if err != nil {
		t.Fatalf("expected counters read to succeed, got: %v", err)
	}
	if len(counters) != 2 {
		t.Fatalf("expected two counter rows, got %d", len(counters))
	}

	if counters[0].ID != 601 || counters[0].Packets != 7 || counters[0].Bytes != 4096 {
		t.Fatalf("unexpected counter for first rule: %+v", counters[0])
	}
	if counters[1].ID != 602 || counters[1].Packets != 3 || counters[1].Bytes != 512 {
		t.Fatalf("unexpected counter for second rule: %+v", counters[1])
	}
}

func TestParseNATRuleCountersFromPFAggregatesByLabel(t *testing.T) {
	output := strings.Join([]string{
		`@301 nat log on em0 inet from any to any tag sylve_nat_71 -> (em0)`,
		`  [ Evaluations: 30      Packets: 8         Bytes: 1024        States: 0     ]`,
		`@302 nat log on em0 inet6 from any to any tag sylve_nat_71 -> (em0)`,
		`  [ Evaluations: 12      Packets: 2         Bytes: 256         States: 0     ]`,
		`@303 rdr log on em0 inet proto tcp from any to any port 80 tag sylve_nat_72 -> 10.0.0.10 port 8080`,
		`  [ Evaluations: 11      Packets: 4         Bytes: 512         States: 0     ]`,
	}, "\n")

	counters, ruleNumbers := parseLabeledRuleCounters(output, pfNATRuleLabelPattern)
	if len(counters) != 2 {
		t.Fatalf("expected 2 nat counter entries, got %d (%v)", len(counters), counters)
	}

	if counters[71].Packets != 10 || counters[71].Bytes != 1280 {
		t.Fatalf("unexpected aggregated nat counters for rule 71: %+v", counters[71])
	}
	if counters[72].Packets != 4 || counters[72].Bytes != 512 {
		t.Fatalf("unexpected nat counters for rule 72: %+v", counters[72])
	}

	if ruleNumbers[301] != 71 || ruleNumbers[303] != 72 {
		t.Fatalf("unexpected rule number mapping: %v", ruleNumbers)
	}
}

func TestGetFirewallNATRuleCountersReturnsZerosWhenPFUnavailable(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})

	rule := networkModels.FirewallNATRule{
		ID:               701,
		Name:             "nat-test",
		Enabled:          true,
		Priority:         1,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		TranslateMode:    "interface",
		Family:           "any",
		Protocol:         "any",
		SourceRaw:        "10.0.0.0/24",
		DestRaw:          "any",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to create nat rule: %v", err)
	}

	previousRunCommand := firewallRunCommand
	firewallRunCommand = func(command string, args ...string) (string, error) {
		if command == "/sbin/pfctl" && len(args) > 0 && args[0] == "-si" {
			return "", fmt.Errorf("pf disabled")
		}
		return "", nil
	}
	t.Cleanup(func() {
		firewallRunCommand = previousRunCommand
	})

	counters, err := svc.GetFirewallNATRuleCounters()
	if err != nil {
		t.Fatalf("expected nat counters to succeed when pf unavailable, got: %v", err)
	}
	if len(counters) != 1 {
		t.Fatalf("expected one nat counter row, got %d", len(counters))
	}
	if counters[0].ID != rule.ID {
		t.Fatalf("expected nat counter row for rule id %d, got %d", rule.ID, counters[0].ID)
	}
	if counters[0].Packets != 0 || counters[0].Bytes != 0 {
		t.Fatalf("expected zero nat counters when pf unavailable, got packets=%d bytes=%d", counters[0].Packets, counters[0].Bytes)
	}
}

func TestGetFirewallNATRuleCountersParsesPFOutput(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})

	ruleA := networkModels.FirewallNATRule{
		ID:               801,
		Name:             "nat-a",
		Enabled:          true,
		Priority:         1,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		TranslateMode:    "interface",
		Family:           "any",
		Protocol:         "any",
		SourceRaw:        "10.1.0.0/24",
		DestRaw:          "any",
	}
	ruleB := networkModels.FirewallNATRule{
		ID:                802,
		Name:              "nat-b",
		Enabled:           true,
		Priority:          2,
		NATType:           "dnat",
		IngressInterfaces: []string{"em0"},
		Family:            "inet",
		Protocol:          "tcp",
		SourceRaw:         "any",
		DestRaw:           "198.51.100.20",
		DNATTargetRaw:     "10.1.0.20",
		DstPortsRaw:       "443",
	}
	if err := db.Create(&ruleA).Error; err != nil {
		t.Fatalf("failed to create nat rule A: %v", err)
	}
	if err := db.Create(&ruleB).Error; err != nil {
		t.Fatalf("failed to create nat rule B: %v", err)
	}

	sampleOutput := strings.Join([]string{
		`@21 nat log on em0 inet from any to any tag sylve_nat_801 -> (em0)`,
		`  [ Evaluations: 9       Packets: 7         Bytes: 4096        States: 2     ]`,
		`@22 rdr log on em0 inet proto tcp from any to any port 443 tag sylve_nat_802 -> 10.1.0.20 port 443`,
		`  [ Evaluations: 12      Packets: 3         Bytes: 512         States: 1     ]`,
	}, "\n")

	previousRunCommand := firewallRunCommand
	firewallRunCommand = func(command string, args ...string) (string, error) {
		if command == "/sbin/pfctl" && len(args) > 0 && args[0] == "-si" {
			return "Status: Enabled", nil
		}
		if command == "/sbin/pfctl" && len(args) == 3 && args[0] == "-a" && args[1] == "sylve/nat-rules" && args[2] == "-vvsn" {
			return sampleOutput, nil
		}
		return "", nil
	}
	t.Cleanup(func() {
		firewallRunCommand = previousRunCommand
	})

	counters, err := svc.GetFirewallNATRuleCounters()
	if err != nil {
		t.Fatalf("expected nat counters read to succeed, got: %v", err)
	}
	if len(counters) != 2 {
		t.Fatalf("expected two nat counter rows, got %d", len(counters))
	}

	if counters[0].ID != 801 || counters[0].Packets != 7 || counters[0].Bytes != 4096 {
		t.Fatalf("unexpected nat counter for first rule: %+v", counters[0])
	}
	if counters[1].ID != 802 || counters[1].Packets != 3 || counters[1].Bytes != 512 {
		t.Fatalf("unexpected nat counter for second rule: %+v", counters[1])
	}
}

func TestParseFirewallLogLineExtractsRuleMetadata(t *testing.T) {
	line := `1712400566.123456 rule 31/0(match): pass in on bridge0: 203.0.113.10.12345 > 198.51.100.20.443: Flags [S], length 64`
	parsed, ok := parseFirewallLogLine(line)
	if !ok {
		t.Fatal("expected firewall log line to parse")
	}
	if parsed.RuleNumber != 31 {
		t.Fatalf("expected rule number 31, got %d", parsed.RuleNumber)
	}
	if parsed.SubruleNumber != 0 {
		t.Fatalf("expected subrule number 0, got %d", parsed.SubruleNumber)
	}
	if parsed.Ruleset != "match" {
		t.Fatalf("expected ruleset match, got %q", parsed.Ruleset)
	}
	if parsed.Interface != "bridge0" {
		t.Fatalf("expected interface bridge0, got %q", parsed.Interface)
	}
	if parsed.Action != "pass" || parsed.Direction != "in" {
		t.Fatalf("unexpected action/direction: action=%q direction=%q", parsed.Action, parsed.Direction)
	}
	if parsed.Bytes != 64 {
		t.Fatalf("expected length 64 bytes, got %d", parsed.Bytes)
	}
}

func TestResolveFirewallRuleReferencePrefersSubruleForAnchorRuleset(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)
	runtime := svc.getFirewallTelemetryRuntime()

	runtime.mu.Lock()
	runtime.trafficRuleNumbers = map[int]uint{
		0: 12,
		1: 11,
	}
	runtime.ruleNames = map[firewallCounterKey]string{
		{RuleType: "traffic", RuleID: 11}: "Default allow all (outbound)",
		{RuleType: "traffic", RuleID: 12}: "Block FireHOL",
	}
	runtime.mu.Unlock()

	ruleType, ruleID, ruleName, ok := svc.resolveFirewallRuleReference(parsedFirewallLogLine{
		RuleNumber:    1,
		SubruleNumber: 0,
		Ruleset:       "traffic-rules",
	})
	if !ok {
		t.Fatal("expected resolveFirewallRuleReference to resolve anchor subrule mapping")
	}
	if ruleType != "traffic" || ruleID != 12 {
		t.Fatalf("expected traffic rule id 12, got type=%q id=%d", ruleType, ruleID)
	}
	if ruleName != "Block FireHOL" {
		t.Fatalf("expected rule name Block FireHOL, got %q", ruleName)
	}
}

func TestResolveFirewallRuleReferencePrefersNATForNATActionWithoutRuleset(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)
	runtime := svc.getFirewallTelemetryRuntime()

	runtime.mu.Lock()
	runtime.trafficRuleNumbers = map[int]uint{
		0: 13,
	}
	runtime.natRuleNumbers = map[int]uint{
		0: 3,
	}
	runtime.ruleNames = map[firewallCounterKey]string{
		{RuleType: "traffic", RuleID: 13}: "Block IPT Office",
		{RuleType: "nat", RuleID: 3}:      "To NC Porter",
	}
	runtime.mu.Unlock()

	ruleType, ruleID, ruleName, ok := svc.resolveFirewallRuleReference(parsedFirewallLogLine{
		RuleNumber:    0,
		SubruleNumber: 0,
		Ruleset:       "",
		Action:        "rdr",
		Direction:     "in",
	})
	if !ok {
		t.Fatal("expected resolveFirewallRuleReference to resolve nat mapping for rdr action")
	}
	if ruleType != "nat" || ruleID != 3 {
		t.Fatalf("expected nat rule id 3, got type=%q id=%d", ruleType, ruleID)
	}
	if ruleName != "To NC Porter" {
		t.Fatalf("expected nat rule name To NC Porter, got %q", ruleName)
	}
}

func TestGetFirewallLiveHitsCursorPagination(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)
	runtime := svc.getFirewallTelemetryRuntime()

	now := time.Now().UTC()
	runtime.mu.Lock()
	runtime.liveSourceStatus = "ok"
	runtime.liveCursor = 3
	runtime.liveUpdatedAt = now
	runtime.liveHits = []networkServiceInterfaces.FirewallLiveHitEvent{
		{Cursor: 1, Timestamp: now.Add(-3 * time.Second), RuleType: "traffic", RuleID: 11, RuleName: "t1"},
		{Cursor: 2, Timestamp: now.Add(-2 * time.Second), RuleType: "nat", RuleID: 21, RuleName: "n1"},
		{Cursor: 3, Timestamp: now.Add(-1 * time.Second), RuleType: "traffic", RuleID: 12, RuleName: "t2"},
	}
	runtime.mu.Unlock()

	resp, err := svc.GetFirewallLiveHits(1, 1, nil)
	if err != nil {
		t.Fatalf("expected live hits query to succeed, got: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected one item due to limit=1, got %d", len(resp.Items))
	}
	if resp.Items[0].Cursor != 2 {
		t.Fatalf("expected cursor 2 item, got %d", resp.Items[0].Cursor)
	}
	if resp.NextCursor != 2 {
		t.Fatalf("expected next cursor 2, got %d", resp.NextCursor)
	}
}

func TestGetFirewallLiveHitsInitialCursorBootstrapsWithoutHistory(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)
	runtime := svc.getFirewallTelemetryRuntime()

	now := time.Now().UTC()
	runtime.mu.Lock()
	runtime.liveSourceStatus = "ok"
	runtime.liveCursor = 5
	runtime.liveUpdatedAt = now
	runtime.liveHits = []networkServiceInterfaces.FirewallLiveHitEvent{
		{Cursor: 1, Timestamp: now.Add(-5 * time.Second), RuleType: "traffic", RuleID: 11, RuleName: "t1"},
		{Cursor: 2, Timestamp: now.Add(-4 * time.Second), RuleType: "traffic", RuleID: 12, RuleName: "t2"},
		{Cursor: 3, Timestamp: now.Add(-3 * time.Second), RuleType: "traffic", RuleID: 13, RuleName: "t3"},
		{Cursor: 4, Timestamp: now.Add(-2 * time.Second), RuleType: "nat", RuleID: 21, RuleName: "n1"},
		{Cursor: 5, Timestamp: now.Add(-1 * time.Second), RuleType: "nat", RuleID: 22, RuleName: "n2"},
	}
	runtime.mu.Unlock()

	resp, err := svc.GetFirewallLiveHits(0, 2, nil)
	if err != nil {
		t.Fatalf("expected live hits query to succeed, got: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected initial cursor bootstrap with no historical items, got %d", len(resp.Items))
	}
	if resp.NextCursor != 5 {
		t.Fatalf("expected bootstrap next cursor 5, got %d", resp.NextCursor)
	}

	runtime.mu.Lock()
	runtime.liveCursor = 6
	runtime.liveUpdatedAt = now.Add(1 * time.Second)
	runtime.liveHits = append(runtime.liveHits, networkServiceInterfaces.FirewallLiveHitEvent{
		Cursor: 6, Timestamp: now, RuleType: "traffic", RuleID: 14, RuleName: "t4",
	})
	runtime.mu.Unlock()

	respAfter, err := svc.GetFirewallLiveHits(resp.NextCursor, 10, nil)
	if err != nil {
		t.Fatalf("expected post-bootstrap live hits query to succeed, got: %v", err)
	}
	if len(respAfter.Items) != 1 {
		t.Fatalf("expected one new item after bootstrap, got %d", len(respAfter.Items))
	}
	if respAfter.Items[0].Cursor != 6 || respAfter.NextCursor != 6 {
		t.Fatalf("expected cursor 6 advancement after bootstrap, got item=%d next=%d", respAfter.Items[0].Cursor, respAfter.NextCursor)
	}
}

func TestGetFirewallLiveHitsAppliesFilters(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t)
	runtime := svc.getFirewallTelemetryRuntime()

	now := time.Now().UTC()
	runtime.mu.Lock()
	runtime.liveSourceStatus = "ok"
	runtime.liveCursor = 4
	runtime.liveUpdatedAt = now
	runtime.liveHits = []networkServiceInterfaces.FirewallLiveHitEvent{
		{
			Cursor: 1, Timestamp: now.Add(-4 * time.Second), RuleType: "traffic", RuleID: 11, RuleName: "web-allow",
			Action: "pass", Direction: "in", Interface: "bridge0", RawLine: "allow https from 203.0.113.10",
		},
		{
			Cursor: 2, Timestamp: now.Add(-3 * time.Second), RuleType: "nat", RuleID: 22, RuleName: "dnat-443",
			Action: "rdr", Direction: "in", Interface: "vtnet0", RawLine: "rdr tcp port 443",
		},
		{
			Cursor: 3, Timestamp: now.Add(-2 * time.Second), RuleType: "traffic", RuleID: 12, RuleName: "web-block",
			Action: "block", Direction: "in", Interface: "bridge0", RawLine: "block from 198.51.100.2",
		},
		{
			Cursor: 4, Timestamp: now.Add(-1 * time.Second), RuleType: "traffic", RuleID: 11, RuleName: "web-allow",
			Action: "pass", Direction: "in", Interface: "bridge0", RawLine: "allow https from 198.51.100.33",
		},
	}
	runtime.mu.Unlock()

	ruleID := uint(11)
	filter := &networkServiceInterfaces.FirewallLiveHitsFilter{
		RuleType:  "traffic",
		RuleID:    &ruleID,
		Action:    "pass",
		Direction: "in",
		Interface: "bridge0",
		Query:     "https",
	}

	resp, err := svc.GetFirewallLiveHits(0, 100, filter)
	if err != nil {
		t.Fatalf("expected live hits query to succeed, got: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected bootstrap cursor query to return no history even with filters, got %d", len(resp.Items))
	}
	if resp.NextCursor != 4 {
		t.Fatalf("expected bootstrap next cursor 4, got %d", resp.NextCursor)
	}

	respAfter, err := svc.GetFirewallLiveHits(resp.NextCursor-1, 100, filter)
	if err != nil {
		t.Fatalf("expected filtered live hits query to succeed, got: %v", err)
	}
	if len(respAfter.Items) != 1 {
		t.Fatalf("expected exactly one filtered item, got %d", len(respAfter.Items))
	}
	if respAfter.Items[0].Cursor != 4 || respAfter.Items[0].RuleID != 11 {
		t.Fatalf("unexpected filtered item: %+v", respAfter.Items[0])
	}
}

func TestBuildPFMainConfigIncludesObjectTablesAnchor(t *testing.T) {
	rendered := buildPFMainConfig("", "", "/tmp/object-tables.conf", "/tmp/nat.conf", "/tmp/traffic.conf")

	if !strings.Contains(rendered, `anchor "sylve/object-tables"`) {
		t.Fatalf("expected object-tables anchor declaration, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `load anchor "sylve/object-tables" from "/tmp/object-tables.conf"`) {
		t.Fatalf("expected object-tables anchor load path, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `nat-anchor "sylve/nat-rules" all`) {
		t.Fatalf("expected nat-anchor hook declaration, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `rdr-anchor "sylve/nat-rules" all`) {
		t.Fatalf("expected rdr-anchor hook declaration, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `binat-anchor "sylve/nat-rules" all`) {
		t.Fatalf("expected binat-anchor hook declaration, got:\n%s", rendered)
	}
	if strings.Index(rendered, `nat-anchor "sylve/nat-rules" all`) > strings.Index(rendered, `anchor "sylve/object-tables"`) {
		t.Fatalf("expected translation anchor hooks before filtering anchors, got:\n%s", rendered)
	}
}

func TestBuildPFMainConfigPlacesPreRulesAfterTranslationHooks(t *testing.T) {
	rendered := buildPFMainConfig("pass in all keep state", "", "/tmp/object-tables.conf", "/tmp/nat.conf", "/tmp/traffic.conf")

	natHook := strings.Index(rendered, `nat-anchor "sylve/nat-rules" all`)
	preRule := strings.Index(rendered, "pass in all keep state")
	trafficAnchor := strings.Index(rendered, `anchor "sylve/traffic-rules"`)

	if natHook == -1 || preRule == -1 || trafficAnchor == -1 {
		t.Fatalf("expected nat hook, pre rule, and traffic anchor in rendered output, got:\n%s", rendered)
	}
	if preRule < natHook {
		t.Fatalf("expected pre rules after translation hooks to preserve PF ordering, got:\n%s", rendered)
	}
	if preRule > trafficAnchor {
		t.Fatalf("expected pre rules before traffic anchor, got:\n%s", rendered)
	}
}

func TestFormatPFValidationErrorIncludesRuleContext(t *testing.T) {
	err := errors.New(
		"command execution failed: exit status 1, output: /tmp/sylve-pf-1/pf.sylve/traffic-rules.conf:2: syntax error\npfctl: load anchors\n",
	)
	formatted := formatPFValidationError(err, map[string]string{
		"traffic-rules.conf": strings.Join([]string{
			"# managed by Sylve: traffic rules",
			"pass in quick received-on bridge0 from any to any",
		}, "\n"),
	})

	message := formatted.Error()
	if !strings.Contains(message, "pf_validation_failed:") {
		t.Fatalf("expected pf_validation_failed prefix, got: %s", message)
	}
	if !strings.Contains(message, "traffic-rules.conf:2: syntax error") {
		t.Fatalf("expected parsed file/line details, got: %s", message)
	}
	if !strings.Contains(message, "rule: pass in quick received-on bridge0 from any to any") {
		t.Fatalf("expected offending rule line context, got: %s", message)
	}
	if !strings.Contains(message, "received-on is not valid in this generated filter rule") {
		t.Fatalf("expected received-on hint in formatted error, got: %s", message)
	}
}

func TestFormatPFValidationErrorIncludesOrderingHint(t *testing.T) {
	err := errors.New(
		"command execution failed: exit status 1, output: /tmp/sylve-pf-2/pf.conf:6: Rules must be in order: options, ethernet, normalization, queueing, translation, filtering",
	)
	formatted := formatPFValidationError(err, map[string]string{
		"pf.conf": strings.Join([]string{
			"# managed by Sylve",
			"anchor \"sylve/object-tables\"",
			"load anchor \"sylve/object-tables\" from \"/tmp/object-tables.conf\"",
			"anchor \"sylve/traffic-rules\"",
			"load anchor \"sylve/traffic-rules\" from \"/tmp/traffic-rules.conf\"",
			"nat-anchor \"sylve/nat-rules\" all",
		}, "\n"),
	})

	message := formatted.Error()
	if !strings.Contains(message, "pf.conf:6: Rules must be in order") {
		t.Fatalf("expected ordering error details, got: %s", message)
	}
	if !strings.Contains(message, "check pf.conf statement order") {
		t.Fatalf("expected ordering hint, got: %s", message)
	}
}

func TestBuildFirewallObjectTablesDeterministicAndFiltered(t *testing.T) {
	rules := []networkModels.FirewallTrafficRule{
		{
			Enabled: true,
			SourceObj: &networkModels.Object{
				ID:   8,
				Name: "Portal Blocklist",
				Type: "List",
				Resolutions: []networkModels.ObjectResolution{
					{ResolvedValue: "2001:db8::1"},
					{ResolvedValue: "10.0.0.0/24"},
					{ResolvedValue: "10.0.0.0/24"},
					{ResolvedValue: "2001:db8::1"},
				},
			},
		},
	}

	tables := buildFirewallObjectTables(rules, nil)
	table, ok := tables[8]
	if !ok {
		t.Fatal("expected object table for object ID 8")
	}
	if table.ObjectName != "Portal Blocklist" {
		t.Fatalf("unexpected object name on table metadata: %q", table.ObjectName)
	}

	if table.InetName != "sylve_obj_8_inet" || table.Inet6Name != "sylve_obj_8_inet6" {
		t.Fatalf("unexpected table names: inet=%q inet6=%q", table.InetName, table.Inet6Name)
	}
	if len(table.InetValues) != 1 || table.InetValues[0] != "10.0.0.0/24" {
		t.Fatalf("unexpected inet values: %v", table.InetValues)
	}
	if len(table.Inet6Values) != 1 || table.Inet6Values[0] != "2001:db8::1" {
		t.Fatalf("unexpected inet6 values: %v", table.Inet6Values)
	}
}

func TestRenderFirewallObjectTablesSortedOutput(t *testing.T) {
	rendered := renderFirewallObjectTables(map[uint]firewallObjectTable{
		10: {
			ObjectID:    10,
			ObjectName:  "Portal IPv4/IPv6",
			InetName:    "sylve_obj_10_inet",
			InetValues:  []string{"10.0.0.2", "10.0.0.1"},
			Inet6Name:   "sylve_obj_10_inet6",
			Inet6Values: []string{"2001:db8::2"},
		},
		2: {
			ObjectID:    2,
			ObjectName:  "LAN Allow",
			InetName:    "sylve_obj_2_inet",
			InetValues:  []string{"192.168.1.1"},
			Inet6Name:   "",
			Inet6Values: nil,
		},
	})

	firstObjectIdx := strings.Index(rendered, "table <sylve_obj_2_inet>")
	secondObjectIdx := strings.Index(rendered, "table <sylve_obj_10_inet>")
	if firstObjectIdx == -1 || secondObjectIdx == -1 || firstObjectIdx > secondObjectIdx {
		t.Fatalf("expected object table output sorted by object id, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `# object id=10 name="`) {
		t.Fatalf("expected object comment above table definitions, got:\n%s", rendered)
	}
}

func TestRenderTrafficRulesSkipsRuleWhenObjectTableEmpty(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallTrafficRule{
		{
			ID:          77,
			Name:        "empty-object",
			Enabled:     true,
			Priority:    1000,
			Action:      "block",
			Direction:   "in",
			Protocol:    "any",
			Family:      "inet",
			SourceObjID: func() *uint { id := uint(5); return &id }(),
			SourceObj: &networkModels.Object{
				ID:   5,
				Type: "List",
				Entries: []networkModels.ObjectEntry{
					{Value: "https://example.com/feed.txt"},
				},
			},
		},
	}

	tables := buildFirewallObjectTables(rules, nil)
	rendered, err := svc.renderTrafficRules(rules, tables)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if strings.Contains(rendered, "block in") {
		t.Fatalf("expected traffic rule line to be skipped for empty object table, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "skipped traffic rule id=77") {
		t.Fatalf("expected skip warning comment in rendered rules, got:\n%s", rendered)
	}
}

func TestRenderNATRulesUsesObjectTables(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallNATRule{
		{
			ID:               31,
			Name:             "nat-v4",
			Enabled:          true,
			Log:              true,
			Priority:         1000,
			NATType:          "snat",
			EgressInterfaces: []string{"em0"},
			TranslateMode:    "interface",
			Family:           "any",
			Protocol:         "any",
			SourceObj: &networkModels.Object{
				ID:   99,
				Type: "List",
				Resolutions: []networkModels.ObjectResolution{
					{ResolvedValue: "10.10.0.0/16"},
				},
			},
			DestRaw: "any",
		},
	}

	tables := buildFirewallObjectTables(nil, rules)
	rendered, err := svc.renderNATRules(rules, tables)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if !strings.Contains(rendered, "nat log on em0 inet from <sylve_obj_99_inet> to any tag sylve_nat_31 -> (em0)") {
		t.Fatalf("expected nat rule to reference object table, got:\n%s", rendered)
	}
	if strings.Contains(rendered, `label "sylve_nat_31"`) {
		t.Fatalf("did not expect nat label syntax in rendered output, got:\n%s", rendered)
	}
}

func TestRenderNATRulesRendersDNATWithPortRewrite(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallNATRule{
		{
			ID:                41,
			Name:              "rdr-https",
			Enabled:           true,
			Log:               true,
			Priority:          1000,
			NATType:           "dnat",
			IngressInterfaces: []string{"em0"},
			Family:            "inet",
			Protocol:          "tcp",
			SourceRaw:         "any",
			DestRaw:           "198.51.100.10",
			DNATTargetRaw:     "10.0.0.10",
			DstPortsRaw:       "443",
			RedirectPortsRaw:  "8443",
		},
	}

	rendered, err := svc.renderNATRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if !strings.Contains(rendered, "rdr log on em0 inet proto tcp from any to 198.51.100.10 port 443 tag sylve_nat_41 -> 10.0.0.10 port 8443") {
		t.Fatalf("expected dnat/rdr line with port rewrite, got:\n%s", rendered)
	}
	if strings.Contains(rendered, `label "sylve_nat_41"`) {
		t.Fatalf("did not expect dnat label syntax in rendered output, got:\n%s", rendered)
	}
}

func TestRenderNATRulesOmitsLogKeywordWhenLogDisabled(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallNATRule{
		{
			ID:               42,
			Name:             "snat-no-log",
			Enabled:          true,
			Log:              false,
			Priority:         1000,
			NATType:          "snat",
			EgressInterfaces: []string{"em0"},
			TranslateMode:    "interface",
			Family:           "inet",
			Protocol:         "any",
			SourceRaw:        "10.42.0.0/24",
			DestRaw:          "any",
		},
	}

	rendered, err := svc.renderNATRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if strings.Contains(rendered, "nat log on") {
		t.Fatalf("did not expect nat log keyword for log-disabled nat rule, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "nat on em0 inet from 10.42.0.0/24 to any tag sylve_nat_42 -> (em0)") {
		t.Fatalf("expected nat rendering without log keyword, got:\n%s", rendered)
	}
}

func TestValidateFirewallNATRuleRequestRejectsInvalidTypeSpecificFields(t *testing.T) {
	svc := &Service{}
	req := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:             "invalid",
		NATType:          "snat",
		Protocol:         "any",
		Family:           "any",
		EgressInterfaces: []string{"em0"},
		DNATTargetRaw:    "10.0.0.10",
	}

	err := svc.validateFirewallNATRuleRequest(req)
	if err == nil {
		t.Fatal("expected strict nat field validation error")
	}
	if !strings.Contains(err.Error(), "snat_rejects_dnat_only_fields") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestBuildFirewallObjectTablesIgnoresDisabledRules(t *testing.T) {
	rules := []networkModels.FirewallTrafficRule{
		{
			Enabled: false,
			SourceObj: &networkModels.Object{
				ID:   9,
				Type: "List",
				Resolutions: []networkModels.ObjectResolution{
					{ResolvedValue: "10.9.0.0/24"},
				},
			},
		},
		{
			Enabled: true,
			SourceObj: &networkModels.Object{
				ID:   10,
				Type: "List",
				Resolutions: []networkModels.ObjectResolution{
					{ResolvedValue: "10.10.0.0/24"},
				},
			},
		},
	}

	tables := buildFirewallObjectTables(rules, nil)
	if _, ok := tables[9]; ok {
		t.Fatal("expected disabled rule object table to be omitted")
	}
	if _, ok := tables[10]; !ok {
		t.Fatal("expected enabled rule object table to be present")
	}
}

func TestValidateFirewallTrafficRuleRequestRejectsPortsForICMP(t *testing.T) {
	svc := &Service{}
	req := &networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name:        "icmp-with-port",
		Action:      "pass",
		Direction:   "in",
		Protocol:    "icmp",
		Family:      "any",
		SrcPortsRaw: "80",
	}

	err := svc.validateFirewallTrafficRuleRequest(req)
	if err == nil {
		t.Fatal("expected traffic validation to reject ports for icmp")
	}
	if !strings.Contains(err.Error(), "ports_require_tcp_or_udp_protocol") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateFirewallTrafficRuleRequestRejectsMalformedRawPorts(t *testing.T) {
	svc := &Service{}
	req := &networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name:        "tcp-bad-port",
		Action:      "pass",
		Direction:   "in",
		Protocol:    "tcp",
		Family:      "any",
		SrcPortsRaw: "abc",
	}

	err := svc.validateFirewallTrafficRuleRequest(req)
	if err == nil {
		t.Fatal("expected traffic validation to reject malformed raw port selector")
	}
	if !strings.Contains(err.Error(), "invalid_src_ports_raw_port_selector") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateFirewallTrafficRuleRequestRejectsIrrelevantDirectionalInterfaces(t *testing.T) {
	svc := &Service{}
	inboundWithEgress := &networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name:             "inbound-with-egress",
		Action:           "pass",
		Direction:        "in",
		Protocol:         "any",
		Family:           "any",
		EgressInterfaces: []string{"bridge0"},
	}

	err := svc.validateFirewallTrafficRuleRequest(inboundWithEgress)
	if err == nil {
		t.Fatal("expected inbound traffic rule validation to reject egress interfaces")
	}
	if !strings.Contains(err.Error(), "in_direction_rejects_egress_interfaces") {
		t.Fatalf("unexpected validation error: %v", err)
	}

	outboundWithIngress := &networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name:              "outbound-with-ingress",
		Action:            "pass",
		Direction:         "out",
		Protocol:          "any",
		Family:            "any",
		IngressInterfaces: []string{"bridge0"},
	}

	err = svc.validateFirewallTrafficRuleRequest(outboundWithIngress)
	if err == nil {
		t.Fatal("expected outbound traffic rule validation to reject ingress interfaces")
	}
	if !strings.Contains(err.Error(), "out_direction_rejects_ingress_interfaces") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateFirewallNATRuleRequestSNATRequiresEgress(t *testing.T) {
	svc := &Service{}
	req := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:     "snat-no-egress",
		NATType:  "snat",
		Protocol: "any",
		Family:   "any",
	}

	err := svc.validateFirewallNATRuleRequest(req)
	if err == nil {
		t.Fatal("expected snat rule validation to fail without egress interface")
	}
	if !strings.Contains(err.Error(), "snat_requires_egress_interface") {
		t.Fatalf("unexpected validation error: %v", err)
	}

	rejectIngress := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:              "snat-reject-ingress",
		NATType:           "snat",
		Protocol:          "any",
		Family:            "any",
		EgressInterfaces:  []string{"em0"},
		IngressInterfaces: []string{"em1"},
	}
	err = svc.validateFirewallNATRuleRequest(rejectIngress)
	if err == nil {
		t.Fatal("expected snat rule validation to require policy routing for ingress interface selectors")
	}
	if !strings.Contains(err.Error(), "snat_ingress_interfaces_require_policy_routing") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateFirewallNATRuleRequestDNATRequiresIngressAndTarget(t *testing.T) {
	svc := &Service{}
	noIngress := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:          "dnat-no-ingress",
		NATType:       "dnat",
		Protocol:      "tcp",
		Family:        "inet",
		DNATTargetRaw: "10.0.0.10",
	}
	err := svc.validateFirewallNATRuleRequest(noIngress)
	if err == nil {
		t.Fatal("expected dnat rule validation to fail without ingress interface")
	}
	if !strings.Contains(err.Error(), "dnat_requires_ingress_interface") {
		t.Fatalf("unexpected validation error: %v", err)
	}

	noTarget := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:              "dnat-no-target",
		NATType:           "dnat",
		Protocol:          "tcp",
		Family:            "inet",
		IngressInterfaces: []string{"em0"},
	}
	err = svc.validateFirewallNATRuleRequest(noTarget)
	if err == nil {
		t.Fatal("expected dnat rule validation to fail without target")
	}
	if !strings.Contains(err.Error(), "dnat_requires_target_host") {
		t.Fatalf("unexpected validation error: %v", err)
	}

	rejectEgress := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:              "dnat-reject-egress",
		NATType:           "dnat",
		Protocol:          "tcp",
		Family:            "inet",
		IngressInterfaces: []string{"em0"},
		EgressInterfaces:  []string{"em1"},
		DNATTargetRaw:     "10.0.0.10",
	}
	err = svc.validateFirewallNATRuleRequest(rejectEgress)
	if err == nil {
		t.Fatal("expected dnat rule validation to reject egress interface selectors")
	}
	if !strings.Contains(err.Error(), "dnat_rejects_egress_interfaces") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateFirewallNATRuleRequestDNATRejectsTranslateFieldsAndInvalidPorts(t *testing.T) {
	svc := &Service{}
	rejectsTranslateMode := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:              "dnat-translate-mode",
		NATType:           "dnat",
		Protocol:          "tcp",
		Family:            "inet",
		IngressInterfaces: []string{"em0"},
		DNATTargetRaw:     "10.0.0.10",
		TranslateMode:     "interface",
	}
	err := svc.validateFirewallNATRuleRequest(rejectsTranslateMode)
	if err == nil {
		t.Fatal("expected dnat to reject translateMode")
	}
	if !strings.Contains(err.Error(), "dnat_rejects_translate_mode") {
		t.Fatalf("unexpected validation error: %v", err)
	}

	portsNeedTransport := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:              "dnat-port-any-proto",
		NATType:           "dnat",
		Protocol:          "any",
		Family:            "inet",
		IngressInterfaces: []string{"em0"},
		DNATTargetRaw:     "10.0.0.10",
		DstPortsRaw:       "443",
	}
	err = svc.validateFirewallNATRuleRequest(portsNeedTransport)
	if err == nil {
		t.Fatal("expected dnat to reject port match with non tcp/udp protocol")
	}
	if !strings.Contains(err.Error(), "dnat_ports_require_tcp_or_udp_protocol") {
		t.Fatalf("unexpected validation error: %v", err)
	}

	redirectNeedsMatch := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:              "dnat-redirect-no-match",
		NATType:           "dnat",
		Protocol:          "tcp",
		Family:            "inet",
		IngressInterfaces: []string{"em0"},
		DNATTargetRaw:     "10.0.0.10",
		RedirectPortsRaw:  "8443",
	}
	err = svc.validateFirewallNATRuleRequest(redirectNeedsMatch)
	if err == nil {
		t.Fatal("expected dnat to reject redirect port without destination match port")
	}
	if !strings.Contains(err.Error(), "redirect_port_requires_destination_port_match") {
		t.Fatalf("unexpected validation error: %v", err)
	}

	rejectMalformedPorts := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:              "dnat-bad-port",
		NATType:           "dnat",
		Protocol:          "tcp",
		Family:            "inet",
		IngressInterfaces: []string{"em0"},
		DNATTargetRaw:     "10.0.0.10",
		DstPortsRaw:       "https",
	}
	err = svc.validateFirewallNATRuleRequest(rejectMalformedPorts)
	if err == nil {
		t.Fatal("expected dnat to reject malformed raw port selector")
	}
	if !strings.Contains(err.Error(), "invalid_dst_ports_raw_port_selector") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateFirewallNATRuleRequestSNATTranslateModes(t *testing.T) {
	svc := &Service{}
	addressNeedsTarget := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:             "snat-address-missing-target",
		NATType:          "snat",
		Protocol:         "any",
		Family:           "any",
		EgressInterfaces: []string{"em0"},
		TranslateMode:    "address",
	}
	err := svc.validateFirewallNATRuleRequest(addressNeedsTarget)
	if err == nil {
		t.Fatal("expected snat translateMode=address to require translate target")
	}
	if !strings.Contains(err.Error(), "translate_mode_address_requires_translate_target") {
		t.Fatalf("unexpected validation error: %v", err)
	}

	interfaceRejectsTarget := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:             "snat-interface-with-target",
		NATType:          "snat",
		Protocol:         "any",
		Family:           "any",
		EgressInterfaces: []string{"em0"},
		TranslateMode:    "interface",
		TranslateToRaw:   "203.0.113.50",
	}
	err = svc.validateFirewallNATRuleRequest(interfaceRejectsTarget)
	if err == nil {
		t.Fatal("expected snat translateMode=interface to reject explicit translate target")
	}
	if !strings.Contains(err.Error(), "translate_to_fields_not_allowed_for_translate_mode_interface") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateFirewallNATRuleRequestPolicyRoutingAcceptsValidSNATAndBINAT(t *testing.T) {
	svc := &Service{}

	enablePolicyRouting := true
	snat := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "snat-policy",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "inet",
		IngressInterfaces:    []string{"bridge0"},
		EgressInterfaces:     []string{"wgc2"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: &enablePolicyRouting,
		PolicyRouteGateway:   "198.51.100.1",
	}
	if err := svc.validateFirewallNATRuleRequest(snat); err != nil {
		t.Fatalf("expected snat policy routing request to pass validation, got: %v", err)
	}

	binat := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "binat-policy-gateway",
		NATType:              "binat",
		Protocol:             "any",
		Family:               "inet",
		IngressInterfaces:    []string{"bridge0"},
		EgressInterfaces:     []string{"wgc2"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: &enablePolicyRouting,
		PolicyRouteGateway:   "198.51.100.1",
	}
	if err := svc.validateFirewallNATRuleRequest(binat); err != nil {
		t.Fatalf("expected binat policy routing request with gateway to pass validation, got: %v", err)
	}
}

func TestValidateFirewallNATRuleRequestPolicyRoutingRejectsInvalidCombinations(t *testing.T) {
	svc := &Service{}
	enablePolicyRouting := true

	dnat := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "dnat-policy",
		NATType:              "dnat",
		Protocol:             "tcp",
		Family:               "inet",
		IngressInterfaces:    []string{"em0"},
		DNATTargetRaw:        "10.0.0.10",
		PolicyRoutingEnabled: &enablePolicyRouting,
	}
	err := svc.validateFirewallNATRuleRequest(dnat)
	if err == nil {
		t.Fatal("expected dnat with policy routing to fail validation")
	}
	if !strings.Contains(err.Error(), "dnat_rejects_policy_routing") {
		t.Fatalf("unexpected validation error for dnat policy routing: %v", err)
	}

	noEgress := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "snat-policy-no-egress",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "any",
		TranslateMode:        "interface",
		PolicyRoutingEnabled: &enablePolicyRouting,
	}
	err = svc.validateFirewallNATRuleRequest(noEgress)
	if err == nil {
		t.Fatal("expected policy routing to reject zero egress interfaces")
	}
	if !strings.Contains(err.Error(), "policy_routing_requires_exactly_one_egress_interface") {
		t.Fatalf("unexpected validation error for zero egress interfaces: %v", err)
	}

	noGateway := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "snat-policy-no-gateway",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "inet",
		EgressInterfaces:     []string{"em0"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: &enablePolicyRouting,
	}
	err = svc.validateFirewallNATRuleRequest(noGateway)
	if err == nil {
		t.Fatal("expected policy routing to require explicit gateway")
	}
	if !strings.Contains(err.Error(), "policy_route_gateway_required_when_policy_routing_enabled") {
		t.Fatalf("unexpected validation error for missing policy gateway: %v", err)
	}

	multiEgress := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "snat-policy-multi-egress",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "any",
		EgressInterfaces:     []string{"em0", "em1"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: &enablePolicyRouting,
	}
	err = svc.validateFirewallNATRuleRequest(multiEgress)
	if err == nil {
		t.Fatal("expected policy routing to reject multiple egress interfaces")
	}
	if !strings.Contains(err.Error(), "policy_routing_requires_exactly_one_egress_interface") {
		t.Fatalf("unexpected validation error for multiple egress interfaces: %v", err)
	}

	gatewayWithAnyFamily := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "snat-policy-gateway-any",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "any",
		EgressInterfaces:     []string{"em0"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: &enablePolicyRouting,
		PolicyRouteGateway:   "198.51.100.1",
	}
	err = svc.validateFirewallNATRuleRequest(gatewayWithAnyFamily)
	if err == nil {
		t.Fatal("expected policy routing gateway to reject family=any")
	}
	if !strings.Contains(err.Error(), "policy_route_gateway_requires_explicit_family") {
		t.Fatalf("unexpected validation error for gateway + family any: %v", err)
	}

	familyMismatch := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "snat-policy-gateway-mismatch",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "inet",
		EgressInterfaces:     []string{"em0"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: &enablePolicyRouting,
		PolicyRouteGateway:   "2001:db8::1",
	}
	err = svc.validateFirewallNATRuleRequest(familyMismatch)
	if err == nil {
		t.Fatal("expected policy route gateway family mismatch to fail validation")
	}
	if !strings.Contains(err.Error(), "incompatible_family_for_policy_route_gateway") {
		t.Fatalf("unexpected validation error for policy route gateway family mismatch: %v", err)
	}
}

func TestCreateAndEditFirewallNATRulePolicyRoutingPersistence(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})
	boolPtr := func(v bool) *bool { return &v }

	createReq := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "policy-persist",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "inet",
		EgressInterfaces:     []string{"wgc2"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: boolPtr(false),
		PolicyRouteGateway:   "198.51.100.1",
	}

	id, err := svc.CreateFirewallNATRule(createReq)
	if err != nil {
		t.Fatalf("expected nat rule creation to succeed, got: %v", err)
	}

	var created networkModels.FirewallNATRule
	if err := db.First(&created, id).Error; err != nil {
		t.Fatalf("failed to load created nat rule: %v", err)
	}
	if created.PolicyRoutingEnabled {
		t.Fatalf("expected policy routing to be disabled after create, got enabled=true")
	}
	if created.PolicyRouteGateway != "" {
		t.Fatalf("expected policy route gateway to be cleared when disabled, got %q", created.PolicyRouteGateway)
	}

	enableReq := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "policy-persist",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "inet",
		EgressInterfaces:     []string{"wgc2"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: boolPtr(true),
		PolicyRouteGateway:   "198.51.100.1",
	}
	if err := svc.EditFirewallNATRule(id, enableReq); err != nil {
		t.Fatalf("expected nat rule edit to enable policy routing, got: %v", err)
	}

	var enabled networkModels.FirewallNATRule
	if err := db.First(&enabled, id).Error; err != nil {
		t.Fatalf("failed to load edited nat rule: %v", err)
	}
	if !enabled.PolicyRoutingEnabled {
		t.Fatalf("expected policy routing to be enabled after edit")
	}
	if enabled.PolicyRouteGateway != "198.51.100.1" {
		t.Fatalf("expected persisted policy route gateway, got %q", enabled.PolicyRouteGateway)
	}

	disableReq := &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:                 "policy-persist",
		NATType:              "snat",
		Protocol:             "any",
		Family:               "inet",
		EgressInterfaces:     []string{"wgc2"},
		TranslateMode:        "interface",
		PolicyRoutingEnabled: boolPtr(false),
		PolicyRouteGateway:   "203.0.113.1",
	}
	if err := svc.EditFirewallNATRule(id, disableReq); err != nil {
		t.Fatalf("expected nat rule edit to disable policy routing, got: %v", err)
	}

	var disabled networkModels.FirewallNATRule
	if err := db.First(&disabled, id).Error; err != nil {
		t.Fatalf("failed to load final nat rule: %v", err)
	}
	if disabled.PolicyRoutingEnabled {
		t.Fatalf("expected policy routing to be disabled after edit")
	}
	if disabled.PolicyRouteGateway != "" {
		t.Fatalf("expected policy route gateway to be cleared on disable, got %q", disabled.PolicyRouteGateway)
	}
}

func TestRenderNATRulesRendersSNATAddressAndBINATInterface(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallNATRule{
		{
			ID:               51,
			Name:             "snat-address",
			Enabled:          true,
			Log:              true,
			Priority:         1000,
			NATType:          "snat",
			EgressInterfaces: []string{"em0"},
			TranslateMode:    "address",
			TranslateToRaw:   "203.0.113.50",
			Family:           "inet",
			Protocol:         "any",
			SourceRaw:        "10.0.0.0/24",
			DestRaw:          "any",
		},
		{
			ID:               52,
			Name:             "binat-interface",
			Enabled:          true,
			Log:              true,
			Priority:         1010,
			NATType:          "binat",
			EgressInterfaces: []string{"em0"},
			TranslateMode:    "interface",
			Family:           "inet",
			Protocol:         "any",
			SourceRaw:        "10.0.1.10",
			DestRaw:          "any",
		},
	}

	rendered, err := svc.renderNATRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if !strings.Contains(rendered, "nat log on em0 inet from 10.0.0.0/24 to any tag sylve_nat_51 -> 203.0.113.50") {
		t.Fatalf("expected snat address translation line, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "binat log on em0 inet from 10.0.1.10 to any tag sylve_nat_52 -> (em0)") {
		t.Fatalf("expected binat interface translation line, got:\n%s", rendered)
	}
}

func TestRenderNATPolicyRoutingRulesRendersCompanionRules(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallNATRule{
		{
			ID:                   91,
			Name:                 "policy-with-gateway",
			Enabled:              true,
			NATType:              "snat",
			PolicyRoutingEnabled: true,
			PolicyRouteGateway:   "203.0.113.1",
			EgressInterfaces:     []string{"wgc2"},
			Family:               "inet",
			Protocol:             "any",
			SourceRaw:            "192.168.180.102",
			DestRaw:              "any",
			TranslateMode:        "interface",
		},
		{
			ID:                   92,
			Name:                 "policy-with-gateway",
			Enabled:              true,
			NATType:              "binat",
			PolicyRoutingEnabled: true,
			PolicyRouteGateway:   "198.51.100.1",
			EgressInterfaces:     []string{"wgc2"},
			Family:               "inet",
			Protocol:             "tcp",
			SourceRaw:            "192.168.180.103",
			DestRaw:              "203.0.113.0/24",
			TranslateMode:        "interface",
		},
	}

	rendered, err := svc.renderNATPolicyRoutingRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if !strings.Contains(rendered, `pass in quick route-to (wgc2 203.0.113.1) inet from 192.168.180.102 to ! self tag "sylve_nat_91" label "sylve_nat_policy_91_in"`) {
		t.Fatalf("expected inbound policy companion rule with explicit gateway, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `pass out quick on wgc2 route-to (wgc2 203.0.113.1) inet from 192.168.180.102 to ! self tag "sylve_nat_91" label "sylve_nat_policy_91_out"`) {
		t.Fatalf("expected outbound policy companion rule with explicit gateway, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `pass in quick route-to (wgc2 198.51.100.1) inet proto tcp from 192.168.180.103 to 203.0.113.0/24 tag "sylve_nat_92" label "sylve_nat_policy_92_in"`) {
		t.Fatalf("expected inbound policy companion rule for tcp with explicit gateway, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `pass out quick on wgc2 route-to (wgc2 198.51.100.1) inet proto tcp from 192.168.180.103 to 203.0.113.0/24 tag "sylve_nat_92" label "sylve_nat_policy_92_out"`) {
		t.Fatalf("expected outbound policy companion rule for tcp with explicit gateway, got:\n%s", rendered)
	}
}

func TestRenderNATPolicyRoutingRulesHonorsIngressScope(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallNATRule{
		{
			ID:                   191,
			Name:                 "policy-ingress-scoped",
			Enabled:              true,
			NATType:              "snat",
			PolicyRoutingEnabled: true,
			PolicyRouteGateway:   "203.0.113.1",
			IngressInterfaces:    []string{"bridge0"},
			EgressInterfaces:     []string{"wgc2"},
			Family:               "inet",
			Protocol:             "any",
			SourceRaw:            "192.168.180.0/24",
			DestRaw:              "any",
			TranslateMode:        "interface",
		},
	}

	rendered, err := svc.renderNATPolicyRoutingRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if !strings.Contains(rendered, `pass in quick on bridge0 route-to (wgc2 203.0.113.1) inet from 192.168.180.0/24 to ! self tag "sylve_nat_191" label "sylve_nat_policy_191_in"`) {
		t.Fatalf("expected inbound policy companion rule to include ingress interface scope, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `pass out quick on wgc2 route-to (wgc2 203.0.113.1) inet from 192.168.180.0/24 to ! self tag "sylve_nat_191" label "sylve_nat_policy_191_out"`) {
		t.Fatalf("expected outbound policy companion rule with self exclusion, got:\n%s", rendered)
	}
}

func TestRenderNATPolicyRoutingRulesSkipsWhenPolicyRoutingDisabled(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallNATRule{
		{
			ID:                   93,
			Name:                 "no-policy",
			Enabled:              true,
			NATType:              "snat",
			PolicyRoutingEnabled: false,
			EgressInterfaces:     []string{"wgc2"},
			Family:               "inet",
			Protocol:             "any",
			SourceRaw:            "any",
			DestRaw:              "any",
			TranslateMode:        "interface",
		},
	}

	rendered, err := svc.renderNATPolicyRoutingRules(rules, map[uint]firewallObjectTable{})
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}
	if strings.TrimSpace(rendered) != "" {
		t.Fatalf("expected no policy routing companion output when disabled, got:\n%s", rendered)
	}
}

func TestNATPolicyRoutingRulesPrependTrafficRules(t *testing.T) {
	svc := &Service{}

	natRules := []networkModels.FirewallNATRule{
		{
			ID:                   94,
			Name:                 "policy-order",
			Enabled:              true,
			NATType:              "snat",
			PolicyRoutingEnabled: true,
			PolicyRouteGateway:   "198.51.100.1",
			EgressInterfaces:     []string{"wgc2"},
			Family:               "inet",
			Protocol:             "any",
			SourceRaw:            "192.168.180.102",
			DestRaw:              "any",
			TranslateMode:        "interface",
		},
	}
	trafficRules := []networkModels.FirewallTrafficRule{
		{
			ID:        201,
			Name:      "allow-out",
			Enabled:   true,
			Priority:  1,
			Action:    "pass",
			Direction: "out",
			Protocol:  "any",
			Family:    "any",
			SourceRaw: "192.168.180.102",
			DestRaw:   "any",
		},
	}

	objectTables := buildFirewallObjectTables(trafficRules, natRules)
	policyRendered, err := svc.renderNATPolicyRoutingRules(natRules, objectTables)
	if err != nil {
		t.Fatalf("unexpected policy render error: %v", err)
	}
	trafficRendered, err := svc.renderTrafficRules(trafficRules, objectTables)
	if err != nil {
		t.Fatalf("unexpected traffic render error: %v", err)
	}

	combined := trafficRendered
	if strings.TrimSpace(policyRendered) != "" {
		if strings.TrimSpace(combined) != "" {
			combined = strings.TrimRight(policyRendered, "\n") + "\n" + combined
		} else {
			combined = policyRendered
		}
	}

	policyIdx := strings.Index(combined, `label "sylve_nat_policy_94_in"`)
	trafficIdx := strings.Index(combined, `label "sylve_trf_201"`)
	if policyIdx == -1 || trafficIdx == -1 {
		t.Fatalf("expected both policy and traffic rule labels in rendered output, got:\n%s", combined)
	}
	if policyIdx > trafficIdx {
		t.Fatalf("expected policy routing companion rules before normal traffic rules, got:\n%s", combined)
	}
}

func TestRenderNATRulesSkipsIncompatibleFamilyForRawTranslateTarget(t *testing.T) {
	svc := &Service{}
	rules := []networkModels.FirewallNATRule{
		{
			ID:               61,
			Name:             "snat-mixed-family",
			Enabled:          true,
			Log:              true,
			Priority:         1000,
			NATType:          "snat",
			EgressInterfaces: []string{"em0"},
			TranslateMode:    "address",
			TranslateToRaw:   "203.0.113.60",
			Family:           "any",
			Protocol:         "any",
			SourceObj: &networkModels.Object{
				ID:   62,
				Type: "List",
				Resolutions: []networkModels.ObjectResolution{
					{ResolvedValue: "10.62.0.0/24"},
					{ResolvedValue: "2001:db8:62::/64"},
				},
			},
			DestRaw: "any",
		},
	}

	tables := buildFirewallObjectTables(nil, rules)
	rendered, err := svc.renderNATRules(rules, tables)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	if !strings.Contains(rendered, "nat log on em0 inet from <sylve_obj_62_inet> to any tag sylve_nat_61 -> 203.0.113.60") {
		t.Fatalf("expected inet line for compatible target family, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "nat log on em0 inet6") {
		t.Fatalf("did not expect inet6 line with ipv4 translation target, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "raw target \"203.0.113.60\" is not valid for family inet6") {
		t.Fatalf("expected skip warning for incompatible inet6 family line, got:\n%s", rendered)
	}
}

func TestEnsureDefaultAllowTrafficRulesSeedsWhenEmpty(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})

	if err := svc.ensureDefaultAllowTrafficRules(); err != nil {
		t.Fatalf("expected default rule seed to succeed, got: %v", err)
	}

	var rules []networkModels.FirewallTrafficRule
	if err := db.Order("priority asc").Find(&rules).Error; err != nil {
		t.Fatalf("failed loading seeded rules: %v", err)
	}

	if len(rules) != 2 {
		t.Fatalf("expected 2 baseline traffic rules, got %d", len(rules))
	}
	if rules[0].Direction != "in" || rules[1].Direction != "out" {
		t.Fatalf("expected pass in/pass out baseline ordering, got directions=%q,%q", rules[0].Direction, rules[1].Direction)
	}
	if rules[0].Action != "pass" || rules[1].Action != "pass" {
		t.Fatalf("expected pass actions for baseline rules, got actions=%q,%q", rules[0].Action, rules[1].Action)
	}
	if rules[0].Priority != 1 || rules[1].Priority != 2 {
		t.Fatalf("expected baseline priorities 1 and 2, got %d and %d", rules[0].Priority, rules[1].Priority)
	}
}

func TestNextFirewallPrioritiesIncrementByOne(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)

	trafficNext, err := svc.nextFirewallTrafficPriority()
	if err != nil {
		t.Fatalf("expected empty traffic priority lookup to succeed, got: %v", err)
	}
	if trafficNext != 1 {
		t.Fatalf("expected first traffic priority to be 1, got %d", trafficNext)
	}

	if err := db.Select("*").Create(&networkModels.FirewallTrafficRule{
		Name:      "t1",
		Enabled:   true,
		Priority:  1,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	}).Error; err != nil {
		t.Fatalf("failed to seed traffic rule: %v", err)
	}

	trafficNext, err = svc.nextFirewallTrafficPriority()
	if err != nil {
		t.Fatalf("expected traffic next priority lookup to succeed, got: %v", err)
	}
	if trafficNext != 2 {
		t.Fatalf("expected next traffic priority to be 2, got %d", trafficNext)
	}

	natNext, err := svc.nextFirewallNATPriority()
	if err != nil {
		t.Fatalf("expected empty nat priority lookup to succeed, got: %v", err)
	}
	if natNext != 1 {
		t.Fatalf("expected first nat priority to be 1, got %d", natNext)
	}

	if err := db.Select("*").Create(&networkModels.FirewallNATRule{
		Name:             "n1",
		Enabled:          true,
		Priority:         1,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		TranslateMode:    "interface",
		SourceRaw:        "any",
		DestRaw:          "any",
	}).Error; err != nil {
		t.Fatalf("failed to seed nat rule: %v", err)
	}

	natNext, err = svc.nextFirewallNATPriority()
	if err != nil {
		t.Fatalf("expected nat next priority lookup to succeed, got: %v", err)
	}
	if natNext != 2 {
		t.Fatalf("expected next nat priority to be 2, got %d", natNext)
	}
}

func TestEnsureDefaultAllowTrafficRulesNoopWhenRulesExist(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})

	custom := networkModels.FirewallTrafficRule{
		Name:      "custom",
		Enabled:   true,
		Priority:  900,
		Action:    "block",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
		SourceRaw: "any",
		DestRaw:   "any",
	}
	if err := db.Create(&custom).Error; err != nil {
		t.Fatalf("failed creating custom traffic rule fixture: %v", err)
	}

	if err := svc.ensureDefaultAllowTrafficRules(); err != nil {
		t.Fatalf("expected default seed to noop when rules exist, got: %v", err)
	}

	var count int64
	if err := db.Model(&networkModels.FirewallTrafficRule{}).Count(&count).Error; err != nil {
		t.Fatalf("failed counting traffic rules: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected existing rules to be preserved without seeding, got count=%d", count)
	}
}

func TestReorderFirewallTrafficRulesUpdatesPriorities(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.FirewallTrafficRule{},
	)

	ruleA := networkModels.FirewallTrafficRule{Name: "a", Enabled: true, Priority: 1000, Action: "pass", Direction: "in", Protocol: "any", Family: "any"}
	ruleB := networkModels.FirewallTrafficRule{Name: "b", Enabled: true, Priority: 1010, Action: "pass", Direction: "in", Protocol: "any", Family: "any"}
	if err := db.Create(&ruleA).Error; err != nil {
		t.Fatalf("failed to create rule A: %v", err)
	}
	if err := db.Create(&ruleB).Error; err != nil {
		t.Fatalf("failed to create rule B: %v", err)
	}

	err := svc.ReorderFirewallTrafficRules([]networkServiceInterfaces.FirewallReorderRequest{
		{ID: ruleA.ID, Priority: 1010},
		{ID: ruleB.ID, Priority: 1000},
	})
	if err != nil {
		t.Fatalf("expected reorder to succeed, got: %v", err)
	}

	var refreshedA networkModels.FirewallTrafficRule
	var refreshedB networkModels.FirewallTrafficRule
	if err := db.First(&refreshedA, ruleA.ID).Error; err != nil {
		t.Fatalf("failed to reload rule A: %v", err)
	}
	if err := db.First(&refreshedB, ruleB.ID).Error; err != nil {
		t.Fatalf("failed to reload rule B: %v", err)
	}

	if refreshedA.Priority != 1010 || refreshedB.Priority != 1000 {
		t.Fatalf("unexpected reordered priorities: A=%d B=%d", refreshedA.Priority, refreshedB.Priority)
	}
}

func TestReorderFirewallNATRulesRejectsDuplicateIDs(t *testing.T) {
	svc := &Service{}
	err := svc.ReorderFirewallNATRules([]networkServiceInterfaces.FirewallReorderRequest{
		{ID: 1, Priority: 1000},
		{ID: 1, Priority: 1010},
	})
	if err == nil {
		t.Fatal("expected duplicate id validation error")
	}
	if !strings.Contains(err.Error(), "duplicate_rule_id") {
		t.Fatalf("expected duplicate_rule_id error, got: %v", err)
	}
}

func TestReorderFirewallNATRulesUpdatesPriorities(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.FirewallNATRule{},
	)

	ruleA := networkModels.FirewallNATRule{
		Name:             "a",
		Enabled:          true,
		Priority:         1,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		TranslateMode:    "interface",
		SourceRaw:        "any",
		DestRaw:          "any",
	}
	ruleB := networkModels.FirewallNATRule{
		Name:             "b",
		Enabled:          true,
		Priority:         2,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		TranslateMode:    "interface",
		SourceRaw:        "any",
		DestRaw:          "any",
	}
	if err := db.Create(&ruleA).Error; err != nil {
		t.Fatalf("failed to create NAT rule A: %v", err)
	}
	if err := db.Create(&ruleB).Error; err != nil {
		t.Fatalf("failed to create NAT rule B: %v", err)
	}

	err := svc.ReorderFirewallNATRules([]networkServiceInterfaces.FirewallReorderRequest{
		{ID: ruleA.ID, Priority: 2},
		{ID: ruleB.ID, Priority: 1},
	})
	if err != nil {
		t.Fatalf("expected NAT reorder to succeed, got: %v", err)
	}

	var refreshedA networkModels.FirewallNATRule
	var refreshedB networkModels.FirewallNATRule
	if err := db.First(&refreshedA, ruleA.ID).Error; err != nil {
		t.Fatalf("failed to reload NAT rule A: %v", err)
	}
	if err := db.First(&refreshedB, ruleB.ID).Error; err != nil {
		t.Fatalf("failed to reload NAT rule B: %v", err)
	}

	if refreshedA.Priority != 2 || refreshedB.Priority != 1 {
		t.Fatalf("unexpected NAT reordered priorities: A=%d B=%d", refreshedA.Priority, refreshedB.Priority)
	}
}

func TestGetFirewallRulesExcludeHiddenManagedRules(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{}, &networkModels.FirewallNATRule{})

	if err := db.Create(&networkModels.FirewallTrafficRule{
		Name:      "visible-traffic",
		Visible:   true,
		Enabled:   true,
		Priority:  2,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	}).Error; err != nil {
		t.Fatalf("failed to create visible traffic rule: %v", err)
	}
	hiddenTraffic := networkModels.FirewallTrafficRule{
		Name:      "hidden-traffic",
		Visible:   true,
		Enabled:   true,
		Priority:  1,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	}
	if err := db.Create(&hiddenTraffic).Error; err != nil {
		t.Fatalf("failed to create hidden traffic rule: %v", err)
	}
	if err := db.Model(&hiddenTraffic).Update("visible", false).Error; err != nil {
		t.Fatalf("failed to mark hidden traffic rule as hidden: %v", err)
	}

	if err := db.Create(&networkModels.FirewallNATRule{
		Name:             "visible-nat",
		Visible:          true,
		Enabled:          true,
		Priority:         2,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		TranslateMode:    "interface",
	}).Error; err != nil {
		t.Fatalf("failed to create visible nat rule: %v", err)
	}
	hiddenNAT := networkModels.FirewallNATRule{
		Name:             "hidden-nat",
		Visible:          true,
		Enabled:          true,
		Priority:         1,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		TranslateMode:    "interface",
	}
	if err := db.Create(&hiddenNAT).Error; err != nil {
		t.Fatalf("failed to create hidden nat rule: %v", err)
	}
	if err := db.Model(&hiddenNAT).Update("visible", false).Error; err != nil {
		t.Fatalf("failed to mark hidden nat rule as hidden: %v", err)
	}

	trafficRules, err := svc.GetFirewallTrafficRules()
	if err != nil {
		t.Fatalf("expected traffic list to succeed: %v", err)
	}
	if len(trafficRules) != 1 || trafficRules[0].Name != "visible-traffic" {
		t.Fatalf("expected only visible traffic rules, got: %+v", trafficRules)
	}

	natRules, err := svc.GetFirewallNATRules()
	if err != nil {
		t.Fatalf("expected nat list to succeed: %v", err)
	}
	if len(natRules) != 1 || natRules[0].Name != "visible-nat" {
		t.Fatalf("expected only visible nat rules, got: %+v", natRules)
	}
}

func TestHiddenFirewallRulesCannotBeMutatedByGenericFlows(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{}, &networkModels.FirewallNATRule{})

	traffic := networkModels.FirewallTrafficRule{
		Name:      "hidden-traffic",
		Visible:   true,
		Enabled:   true,
		Priority:  1,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	}
	if err := db.Create(&traffic).Error; err != nil {
		t.Fatalf("failed to create hidden traffic rule: %v", err)
	}
	if err := db.Model(&traffic).Update("visible", false).Error; err != nil {
		t.Fatalf("failed to mark hidden traffic rule as hidden: %v", err)
	}
	nat := networkModels.FirewallNATRule{
		Name:             "hidden-nat",
		Visible:          true,
		Enabled:          true,
		Priority:         1,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		TranslateMode:    "interface",
	}
	if err := db.Create(&nat).Error; err != nil {
		t.Fatalf("failed to create hidden nat rule: %v", err)
	}
	if err := db.Model(&nat).Update("visible", false).Error; err != nil {
		t.Fatalf("failed to mark hidden nat rule as hidden: %v", err)
	}

	err := svc.EditFirewallTrafficRule(traffic.ID, &networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name:      "x",
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	})
	if err == nil || !strings.Contains(err.Error(), errHiddenFirewallRuleMutation) {
		t.Fatalf("expected hidden traffic edit guard error, got: %v", err)
	}

	err = svc.DeleteFirewallTrafficRule(traffic.ID)
	if err == nil || !strings.Contains(err.Error(), errHiddenFirewallRuleMutation) {
		t.Fatalf("expected hidden traffic delete guard error, got: %v", err)
	}

	err = svc.ReorderFirewallTrafficRules([]networkServiceInterfaces.FirewallReorderRequest{{ID: traffic.ID, Priority: 1}})
	if err == nil || !strings.Contains(err.Error(), errHiddenFirewallRuleMutation) {
		t.Fatalf("expected hidden traffic reorder guard error, got: %v", err)
	}

	err = svc.EditFirewallNATRule(nat.ID, &networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:             "x",
		NATType:          "snat",
		Protocol:         "any",
		Family:           "inet",
		EgressInterfaces: []string{"em0"},
		TranslateMode:    "interface",
	})
	if err == nil || !strings.Contains(err.Error(), errHiddenFirewallRuleMutation) {
		t.Fatalf("expected hidden nat edit guard error, got: %v", err)
	}

	err = svc.DeleteFirewallNATRule(nat.ID)
	if err == nil || !strings.Contains(err.Error(), errHiddenFirewallRuleMutation) {
		t.Fatalf("expected hidden nat delete guard error, got: %v", err)
	}

	err = svc.ReorderFirewallNATRules([]networkServiceInterfaces.FirewallReorderRequest{{ID: nat.ID, Priority: 1}})
	if err == nil || !strings.Contains(err.Error(), errHiddenFirewallRuleMutation) {
		t.Fatalf("expected hidden nat reorder guard error, got: %v", err)
	}
}

func TestCreateFirewallRulesInsertAtPriorityAndShiftConflicts(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{}, &networkModels.FirewallNATRule{})

	hiddenTraffic := networkModels.FirewallTrafficRule{
		Name:      "hidden-top",
		Visible:   true,
		Enabled:   true,
		Priority:  1,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	}
	visibleTraffic := networkModels.FirewallTrafficRule{
		Name:      "visible-a",
		Visible:   true,
		Enabled:   true,
		Priority:  2,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	}
	if err := db.Create(&hiddenTraffic).Error; err != nil {
		t.Fatalf("failed to create hidden traffic rule: %v", err)
	}
	if err := db.Model(&hiddenTraffic).Update("visible", false).Error; err != nil {
		t.Fatalf("failed to mark hidden traffic rule as hidden: %v", err)
	}
	if err := db.Create(&visibleTraffic).Error; err != nil {
		t.Fatalf("failed to create visible traffic rule: %v", err)
	}

	priority := 1
	if _, err := svc.CreateFirewallTrafficRule(&networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name:      "new-visible",
		Priority:  &priority,
		Action:    "pass",
		Direction: "in",
		Protocol:  "any",
		Family:    "any",
	}); err != nil {
		t.Fatalf("expected traffic create to succeed: %v", err)
	}

	var traffic []networkModels.FirewallTrafficRule
	if err := db.Order("priority asc, id asc").Find(&traffic).Error; err != nil {
		t.Fatalf("failed to reload traffic rules: %v", err)
	}
	if traffic[0].Name != "hidden-top" || traffic[0].Priority != 1 {
		t.Fatalf("expected hidden traffic rule to remain top: %+v", traffic[0])
	}
	if traffic[1].Name != "new-visible" || traffic[1].Priority != 2 {
		t.Fatalf("expected created visible traffic at priority 2, got: %+v", traffic[1])
	}
	if traffic[2].Name != "visible-a" || traffic[2].Priority != 3 {
		t.Fatalf("expected existing visible traffic shifted to priority 3, got: %+v", traffic[2])
	}

	hiddenNAT := networkModels.FirewallNATRule{
		Name:             "hidden-top-nat",
		Visible:          true,
		Enabled:          true,
		Priority:         1,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		TranslateMode:    "interface",
	}
	visibleNAT := networkModels.FirewallNATRule{
		Name:             "visible-nat-a",
		Visible:          true,
		Enabled:          true,
		Priority:         2,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		TranslateMode:    "interface",
	}
	if err := db.Create(&hiddenNAT).Error; err != nil {
		t.Fatalf("failed to create hidden nat rule: %v", err)
	}
	if err := db.Model(&hiddenNAT).Update("visible", false).Error; err != nil {
		t.Fatalf("failed to mark hidden nat rule as hidden: %v", err)
	}
	if err := db.Create(&visibleNAT).Error; err != nil {
		t.Fatalf("failed to create visible nat rule: %v", err)
	}

	priority = 1
	if _, err := svc.CreateFirewallNATRule(&networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:             "new-visible-nat",
		Priority:         &priority,
		NATType:          "snat",
		Protocol:         "any",
		Family:           "inet",
		EgressInterfaces: []string{"em0"},
		TranslateMode:    "interface",
	}); err != nil {
		t.Fatalf("expected nat create to succeed: %v", err)
	}

	var natRules []networkModels.FirewallNATRule
	if err := db.Order("priority asc, id asc").Find(&natRules).Error; err != nil {
		t.Fatalf("failed to reload nat rules: %v", err)
	}
	if natRules[0].Name != "hidden-top-nat" || natRules[0].Priority != 1 {
		t.Fatalf("expected hidden nat rule to remain top: %+v", natRules[0])
	}
	if natRules[1].Name != "new-visible-nat" || natRules[1].Priority != 2 {
		t.Fatalf("expected created visible nat at priority 2, got: %+v", natRules[1])
	}
	if natRules[2].Name != "visible-nat-a" || natRules[2].Priority != 3 {
		t.Fatalf("expected existing visible nat shifted to priority 3, got: %+v", natRules[2])
	}
}

func TestEnsurePFAutostartDisabledInRCConfUpsertsPFEnableNo(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "rc.conf")
	initial := strings.Join([]string{
		`hostname="node1"`,
		`pf_enable="YES"`,
		`pf_rules="/etc/pf.conf"`,
		`pf_enable="YES"`,
		"",
	}, "\n")
	if err := os.WriteFile(tmp, []byte(initial), 0644); err != nil {
		t.Fatalf("failed to seed temp rc.conf: %v", err)
	}

	previous := firewallRCConfPath
	firewallRCConfPath = tmp
	t.Cleanup(func() {
		firewallRCConfPath = previous
	})

	svc := &Service{}
	if err := svc.ensurePFAutostartDisabledInRCConf(); err != nil {
		t.Fatalf("expected rc.conf update to succeed, got: %v", err)
	}

	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("failed to read updated rc.conf: %v", err)
	}
	updated := string(data)

	if strings.Count(updated, "pf_enable=") != 1 {
		t.Fatalf("expected exactly one pf_enable assignment, got:\n%s", updated)
	}
	if !strings.Contains(updated, `pf_enable="NO"`) {
		t.Fatalf("expected pf_enable to be forced to NO, got:\n%s", updated)
	}
	if !strings.Contains(updated, `pf_rules="/etc/pf.conf"`) {
		t.Fatalf("expected unrelated pf_rules to be preserved, got:\n%s", updated)
	}
}

func TestEnsurePFKernelModuleLoadedSkipsLoadWhenPresent(t *testing.T) {
	original := firewallRunCommand
	t.Cleanup(func() {
		firewallRunCommand = original
	})

	calls := []string{}
	firewallRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "/sbin/kldstat" && len(args) == 2 && args[0] == "-m" && args[1] == "pf" {
			return "pf loaded", nil
		}
		t.Fatalf("unexpected command call: %s %v", command, args)
		return "", nil
	}

	svc := &Service{}
	if err := svc.ensurePFKernelModuleLoaded(); err != nil {
		t.Fatalf("expected module check to succeed when loaded, got: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected only kldstat call, got %d calls: %v", len(calls), calls)
	}
}

func TestEnsurePFKernelModuleLoadedLoadsWhenMissing(t *testing.T) {
	original := firewallRunCommand
	t.Cleanup(func() {
		firewallRunCommand = original
	})

	calls := []string{}
	firewallRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "/sbin/kldstat" && len(args) == 2 && args[0] == "-m" && args[1] == "pf" {
			return "", errors.New("not found")
		}
		if command == "/sbin/kldload" && len(args) == 2 && args[0] == "-n" && args[1] == "pf" {
			return "", nil
		}
		t.Fatalf("unexpected command call: %s %v", command, args)
		return "", nil
	}

	svc := &Service{}
	if err := svc.ensurePFKernelModuleLoaded(); err != nil {
		t.Fatalf("expected module load to succeed, got: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected kldstat + kldload calls, got %d calls: %v", len(calls), calls)
	}
}

func TestEnsurePFKernelModuleLoadedReturnsErrorOnLoadFailure(t *testing.T) {
	original := firewallRunCommand
	t.Cleanup(func() {
		firewallRunCommand = original
	})

	firewallRunCommand = func(command string, args ...string) (string, error) {
		if command == "/sbin/kldstat" {
			return "", errors.New("not found")
		}
		if command == "/sbin/kldload" {
			return "", errors.New("permission denied")
		}
		t.Fatalf("unexpected command call: %s %v", command, args)
		return "", nil
	}

	svc := &Service{}
	err := svc.ensurePFKernelModuleLoaded()
	if err == nil {
		t.Fatal("expected module load failure to return an error")
	}
	if !strings.Contains(err.Error(), "failed_to_load_pf_kernel_module") {
		t.Fatalf("expected wrapped load error, got: %v", err)
	}
}

func TestEnsurePFLogInterfaceReadySkipsCreateWhenPresent(t *testing.T) {
	original := firewallRunCommand
	t.Cleanup(func() {
		firewallRunCommand = original
	})

	calls := []string{}
	firewallRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "/sbin/ifconfig" && len(args) == 1 && args[0] == "pflog0" {
			return "pflog0: flags=...", nil
		}
		t.Fatalf("unexpected command call: %s %v", command, args)
		return "", nil
	}

	svc := &Service{}
	if err := svc.ensurePFLogInterfaceReady(); err != nil {
		t.Fatalf("expected pflog readiness check to succeed, got: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected one ifconfig check call, got %d calls: %v", len(calls), calls)
	}
}

func TestEnsurePFLogInterfaceReadyCreatesWhenMissing(t *testing.T) {
	original := firewallRunCommand
	t.Cleanup(func() {
		firewallRunCommand = original
	})

	exists := false
	calls := []string{}
	firewallRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "/sbin/ifconfig" && len(args) == 1 && args[0] == "pflog0" {
			if exists {
				return "pflog0: flags=...", nil
			}
			return "", errors.New("No Such Device")
		}
		if command == "/sbin/ifconfig" && len(args) == 2 && args[0] == "pflog0" && args[1] == "create" {
			exists = true
			return "pflog0", nil
		}
		t.Fatalf("unexpected command call: %s %v", command, args)
		return "", nil
	}

	svc := &Service{}
	if err := svc.ensurePFLogInterfaceReady(); err != nil {
		t.Fatalf("expected pflog create path to succeed, got: %v", err)
	}

	if len(calls) < 3 {
		t.Fatalf("expected ifconfig check + create + verify calls, got %d calls: %v", len(calls), calls)
	}
}
