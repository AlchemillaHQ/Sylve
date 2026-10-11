// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
)

func TestADAllowsAdditiveTrustedMappings(t *testing.T) {
	for _, backend := range []string{"rid", "ad"} {
		for _, state := range []struct {
			name            string
			joined, enabled bool
		}{
			{"joined enabled", true, true},
			{"joined disabled", true, false},
			{"saved permissions", false, false},
		} {
			for _, existing := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/existing=%t", backend, state.name, existing), func(t *testing.T) {
					s := newTestService(t)
					ctx := context.Background()
					input := testADInput()
					input.IDMapBackend = backend
					if existing {
						input.AllowTrustedDomains = true
						input.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2049999999, Schema: "rfc2307"}}
					}
					before, err := s.SaveAD(ctx, input)
					if err != nil {
						t.Fatal(err)
					}
					before.Joined, before.Enabled = state.joined, state.enabled
					if err := s.DB.Save(&before).Error; err != nil {
						t.Fatal(err)
					}
					s.SharePrincipalsInUse = func() (bool, error) { return !state.joined, nil }
					input.AllowTrustedDomains = true
					input.TrustedDomains = append(slices.Clone(input.TrustedDomains), directoryModels.IDMap{Domain: "newdomain", Backend: backend, RangeLow: 2050000000, RangeHigh: 2099999999, Schema: "rfc2307"})
					after, err := s.SaveAD(ctx, input)
					if err != nil {
						t.Fatal(err)
					}
					if after.Enabled != state.enabled || after.Joined != state.joined || !after.AllowTrustedDomains || len(after.TrustedDomains) != len(before.TrustedDomains)+1 {
						t.Fatal("adding a mapping changed membership or the enabled state")
					}
					for _, mapping := range before.TrustedDomains {
						if !slices.Contains(after.TrustedDomains, mapping) {
							t.Fatal("adding a mapping changed an existing trusted mapping")
						}
					}
					afterPrimary := after
					afterPrimary.TrustedDomains = before.TrustedDomains
					if !sameMappingIdentity(before, afterPrimary) {
						t.Fatal("adding a mapping changed the primary domain mapping")
					}
					if state.joined {
						if !state.enabled {
							if err := s.SetEnabled(ctx, true); err != nil {
								t.Fatal(err)
							}
						}
						config := string(readTestFile(t, s.smbPath))
						if !strings.Contains(config, "allow trusted domains = yes") || !strings.Contains(config, "winbind scan trusted domains = yes") || !strings.Contains(config, "idmap config NEWDOMAIN : backend = "+backend) || !strings.Contains(config, "idmap config NEWDOMAIN : range = 2050000000-2099999999") || !strings.Contains(config, "idmap config EXAMPLE : range = 1000000-1999999999") {
							t.Fatal("enabled AD did not apply the added mapping with stable primary IDs")
						}
						if backend == "ad" && !strings.Contains(config, "idmap config NEWDOMAIN : schema_mode = rfc2307") {
							t.Fatal("added AD mapping did not apply its schema")
						}
					}
				})
			}
		}
	}
}

func TestADTrustedMappingEditsRemainLocked(t *testing.T) {
	changes := []struct {
		name   string
		modify func(*ADInput)
	}{
		{"remove trusted mapping", func(i *ADInput) { i.TrustedDomains = i.TrustedDomains[1:] }},
		{"rename trusted domain", func(i *ADInput) { i.TrustedDomains[0].Domain = "RENAMED" }},
		{"trusted backend", func(i *ADInput) { i.TrustedDomains[0].Backend = "ad" }},
		{"trusted schema", func(i *ADInput) { i.TrustedDomains[0].Schema = "sfu" }},
		{"trusted low", func(i *ADInput) { i.TrustedDomains[0].RangeLow++ }},
		{"trusted high", func(i *ADInput) { i.TrustedDomains[0].RangeHigh-- }},
		{"primary domain", func(i *ADInput) { i.Domain = "other.example.com" }},
		{"primary workgroup", func(i *ADInput) { i.Workgroup = "OTHER" }},
		{"primary backend", func(i *ADInput) { i.IDMapBackend = "ad" }},
		{"primary schema", func(i *ADInput) { i.IDMapSchema = "sfu" }},
		{"primary low", func(i *ADInput) { i.RangeLow++ }},
		{"primary high", func(i *ADInput) { i.RangeHigh-- }},
	}
	for _, state := range []struct {
		name            string
		joined, enabled bool
	}{
		{"joined enabled", true, true},
		{"joined disabled", true, false},
		{"saved permissions", false, false},
	} {
		for _, change := range changes {
			t.Run(state.name+"/"+change.name, func(t *testing.T) {
				s := newTestService(t)
				input := testADInput()
				input.AllowTrustedDomains = true
				input.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2049999999, Schema: "rfc2307"}}
				before, err := s.SaveAD(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				before.Joined, before.Enabled = state.joined, state.enabled
				if err := s.DB.Save(&before).Error; err != nil {
					t.Fatal(err)
				}
				s.SharePrincipalsInUse = func() (bool, error) { return !state.joined, nil }
				input.TrustedDomains = append(slices.Clone(input.TrustedDomains), directoryModels.IDMap{Domain: "NEWDOMAIN", Backend: "rid", RangeLow: 2050000000, RangeHigh: 2099999999, Schema: "rfc2307"})
				change.modify(&input)
				if _, err := s.SaveAD(context.Background(), input); !errors.Is(err, ErrConflict) {
					t.Fatalf("adding a mapping bypassed an existing mapping lock: %v", err)
				}
				after, err := s.GetAD()
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("rejected mapping change modified saved configuration: %v", err)
				}
			})
		}
	}
}

func TestTrustedMappingAdditionsRejectPendingMembership(t *testing.T) {
	for _, pending := range []string{"join", "leave", "leave-cleanup"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%t", pending, enabled), func(t *testing.T) {
				s := newTestService(t)
				before := saveTestAD(t, s, true)
				before.Enabled, before.MembershipPending = enabled, pending
				if err := s.DB.Save(&before).Error; err != nil {
					t.Fatal(err)
				}
				input := testADInput()
				input.AllowTrustedDomains = true
				input.TrustedDomains = []directoryModels.IDMap{{Domain: "NEWDOMAIN", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2099999999}}
				if _, err := s.SaveAD(context.Background(), input); !errors.Is(err, ErrConflict) {
					t.Fatalf("accepted mapping addition during pending membership: %v", err)
				}
				after, err := s.GetAD()
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("rejected mapping addition modified saved configuration: %v", err)
				}
			})
		}
	}
}

func TestEnabledADTrustedMappingAdditionsStillRequireValidRanges(t *testing.T) {
	for _, change := range []struct {
		name   string
		modify func(*ADInput)
	}{
		{"primary overlap", func(i *ADInput) { i.TrustedDomains[1].RangeLow, i.TrustedDomains[1].RangeHigh = 1000000, 1000010 }},
		{"trusted overlap", func(i *ADInput) { i.TrustedDomains[1].RangeLow = 2049999999 }},
		{"duplicate trusted domain", func(i *ADInput) { i.TrustedDomains[1].Domain = "TRUSTED" }},
		{"primary domain shadow", func(i *ADInput) { i.TrustedDomains[1].Domain = "EXAMPLE" }},
		{"missing trust enable", func(i *ADInput) { i.AllowTrustedDomains = false }},
		{"unsupported trusted backend", func(i *ADInput) { i.TrustedDomains[1].Backend = "autorid" }},
		{"AUTORID primary", func(i *ADInput) { i.IDMapBackend = "autorid" }},
		{"mapping limit", func(i *ADInput) {
			i.TrustedDomains = i.TrustedDomains[:1]
			for n := uint32(0); n < 32; n++ {
				low := uint32(2050000000) + n*1000000
				i.TrustedDomains = append(i.TrustedDomains, directoryModels.IDMap{Domain: fmt.Sprintf("T%d", n), Backend: "rid", RangeLow: low, RangeHigh: low + 999999})
			}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			s := newTestService(t)
			input := testADInput()
			input.AllowTrustedDomains = true
			input.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2049999999}}
			before, err := s.SaveAD(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			before.Joined, before.Enabled = true, true
			if err := s.DB.Save(&before).Error; err != nil {
				t.Fatal(err)
			}
			input.TrustedDomains = append(slices.Clone(input.TrustedDomains), directoryModels.IDMap{Domain: "NEWDOMAIN", Backend: "rid", RangeLow: 2050000000, RangeHigh: 2099999999})
			change.modify(&input)
			if _, err := s.SaveAD(context.Background(), input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid trusted mapping addition: %v", err)
			}
			after, err := s.GetAD()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("invalid mapping addition modified saved configuration: %v", err)
			}
		})
	}
}

func TestADTrustedMappingApplyFailureRollsBack(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			s := newTestService(t)
			ctx := context.Background()
			input := testADInput()
			input.AllowTrustedDomains = true
			input.TrustedDomains = []directoryModels.IDMap{{Domain: "TRUSTED", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2049999999}}
			before, err := s.SaveAD(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			before.Joined = true
			if err := s.DB.Save(&before).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.SetEnabled(ctx, enabled); err != nil {
				t.Fatal(err)
			}
			before, err = s.GetAD()
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			for _, path := range []string{s.krbPath, s.keytabPath, s.nssPath, s.smbPath} {
				files[path] = readTestFile(t, path)
			}
			write := s.WriteSamba
			s.WriteSamba = func(ctx context.Context, reload bool) error {
				ad, err := s.GetAD()
				if err != nil {
					return err
				}
				if len(ad.TrustedDomains) > len(before.TrustedDomains) {
					return errors.New("test Samba apply failure")
				}
				return write(ctx, reload)
			}
			input.TrustedDomains = append(slices.Clone(input.TrustedDomains), directoryModels.IDMap{Domain: "NEWDOMAIN", Backend: "rid", RangeLow: 2050000000, RangeHigh: 2099999999})
			if _, err := s.SaveAD(ctx, input); err == nil {
				t.Fatal("expected trusted mapping apply failure")
			}
			after, err := s.GetAD()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed mapping apply did not restore saved membership and mappings: %v", err)
			}
			for path, data := range files {
				if !slices.Equal(readTestFile(t, path), data) {
					t.Fatalf("failed mapping apply changed runtime file: %s", path)
				}
			}
		})
	}
}

func TestEnabledADTrustedMappingValidationFailurePreservesLastGoodState(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	saveTestAD(t, s, true)
	if err := s.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetAD()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, path := range []string{s.krbPath, s.keytabPath, s.nssPath, s.smbPath} {
		files[path] = readTestFile(t, path)
	}
	validationError := errors.New("test Samba validation failure")
	validated := false
	s.ValidateSamba = func(_ context.Context, candidate directoryModels.ActiveDirectory) error {
		validated = true
		if !candidate.Enabled || !candidate.Joined || len(candidate.TrustedDomains) != 1 || candidate.TrustedDomains[0].Domain != "NEWDOMAIN" {
			t.Fatal("did not validate the proposed enabled member configuration")
		}
		return validationError
	}
	s.WriteSamba = func(context.Context, bool) error {
		t.Fatal("applied an invalid trusted mapping")
		return nil
	}
	input := testADInput()
	input.AllowTrustedDomains = true
	input.TrustedDomains = []directoryModels.IDMap{{Domain: "NEWDOMAIN", Backend: "rid", RangeLow: 2000000000, RangeHigh: 2099999999}}
	if _, err := s.SaveAD(ctx, input); !errors.Is(err, validationError) || !validated {
		t.Fatalf("enabled trusted mapping bypassed Samba validation: %v", err)
	}
	after, err := s.GetAD()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed mapping validation modified saved configuration: %v", err)
	}
	for path, data := range files {
		if !slices.Equal(readTestFile(t, path), data) {
			t.Fatalf("failed mapping validation changed runtime file: %s", path)
		}
	}
}
