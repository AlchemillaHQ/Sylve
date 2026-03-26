// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"reflect"
	"testing"
)

func TestUpsertLoaderConfSetting(t *testing.T) {
	tests := []struct {
		name        string
		lines       []string
		key         string
		value       string
		wantLines   []string
		wantChanged bool
	}{
		{
			name:        "appends_when_missing",
			lines:       []string{`vmm_load="YES"`},
			key:         jailRacctLoaderKey,
			value:       jailRacctLoaderValue,
			wantLines:   []string{`vmm_load="YES"`, `kern.racct.enable=1`},
			wantChanged: true,
		},
		{
			name:        "keeps_existing_matching_unquoted",
			lines:       []string{`kern.racct.enable=1`, `vmm_load="YES"`},
			key:         jailRacctLoaderKey,
			value:       jailRacctLoaderValue,
			wantLines:   []string{`kern.racct.enable=1`, `vmm_load="YES"`},
			wantChanged: false,
		},
		{
			name:        "keeps_existing_matching_quoted",
			lines:       []string{`kern.racct.enable="1"`},
			key:         jailRacctLoaderKey,
			value:       jailRacctLoaderValue,
			wantLines:   []string{`kern.racct.enable="1"`},
			wantChanged: false,
		},
		{
			name:        "replaces_mismatched_value",
			lines:       []string{`kern.racct.enable=0`, `vmm_load="YES"`},
			key:         jailRacctLoaderKey,
			value:       jailRacctLoaderValue,
			wantLines:   []string{`kern.racct.enable=1`, `vmm_load="YES"`},
			wantChanged: true,
		},
		{
			name:        "deduplicates_active_assignments",
			lines:       []string{`kern.racct.enable=1`, `kern.racct.enable=0`, `ppt_load="YES"`},
			key:         jailRacctLoaderKey,
			value:       jailRacctLoaderValue,
			wantLines:   []string{`kern.racct.enable=1`, `ppt_load="YES"`},
			wantChanged: true,
		},
		{
			name:        "ignores_commented_assignment_and_appends",
			lines:       []string{`# kern.racct.enable=0`, `vmm_load="YES"`},
			key:         jailRacctLoaderKey,
			value:       jailRacctLoaderValue,
			wantLines:   []string{`# kern.racct.enable=0`, `vmm_load="YES"`, `kern.racct.enable=1`},
			wantChanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLines, gotChanged := upsertLoaderConfSetting(tt.lines, tt.key, tt.value)
			if gotChanged != tt.wantChanged {
				t.Fatalf("expected changed=%t, got %t", tt.wantChanged, gotChanged)
			}

			if !reflect.DeepEqual(gotLines, tt.wantLines) {
				t.Fatalf("unexpected lines: want=%v got=%v", tt.wantLines, gotLines)
			}
		})
	}
}
