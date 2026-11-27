// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package samba

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/pkg/utils"
)

const sampleShareACL = `         user:1001:r-x---a-R-c---:fd-----:allow
        group:2001:rwxpDdaARWc--s:fd-----:allow
         user:1002:r-----a-R-c---:fd-----:allow
         user:1001:rwxpDdaARWc--s:fd-----:allow
         user:3001:rwxpDdaARWc--s:fd----I:allow
            owner@:rwxp--aARWcCos:-------:allow
            group@:r-x---a-R-c--s:-------:allow
         everyone@:r-x---a-R-c--s:-------:allow
         everyone@:rwxpDdaARWc--s:fd-----:allow
`

func TestParseSambaACLFindsOnlySylveEntries(t *testing.T) {
	entries, ok := parseSambaACL(sampleShareACL)
	if !ok || len(entries) != 9 {
		t.Fatalf("parsed %d entries ok=%t", len(entries), ok)
	}
	managed := []int{}
	for _, entry := range entries {
		if entry.managed {
			managed = append(managed, entry.position)
		}
	}
	if !slices.Equal(managed, []int{0, 1, 2, 3, 8}) {
		t.Fatalf("managed positions = %v; inherited, owner, group and plain everyone@ entries must not count", managed)
	}
	if _, ok := parseSambaACL("user:broken"); ok {
		t.Fatal("an unreadable ACL parsed")
	}
}

func TestPlanSambaACLChangesOnlyWhatDiffers(t *testing.T) {
	entries, _ := parseSambaACL(sampleShareACL)
	ace := func(key, permission string) sambaACE {
		return sambaACE{key: key, permission: permission, entry: key + ":" + permission}
	}

	removals, additions := planSambaACL(entries, nil, []sambaACE{ace("user:1001", readACLPerm), ace("group:2001", writeACLPerm)})
	if !slices.Equal(removals, []int{3}) || len(additions) != 0 {
		t.Fatalf("matching entries: removals %v additions %v; want only the duplicate removed", removals, additions)
	}

	removals, additions = planSambaACL(entries, []string{"user:1002"}, []sambaACE{ace("user:1001", writeACLPerm), ace("group:2001", writeACLPerm)})
	if !slices.Equal(removals, []int{0, 2}) || len(additions) != 0 {
		t.Fatalf("promotion and revocation: removals %v additions %v", removals, additions)
	}

	removals, additions = planSambaACL(entries, []string{"user:1001"}, []sambaACE{ace("user:1002", writeACLPerm), ace("user:4001", readACLPerm)})
	if !slices.Equal(removals, []int{0, 3}) || len(additions) != 2 || additions[0].key != "user:1002" {
		t.Fatalf("wrong permissions and new principals: removals %v additions %v", removals, additions)
	}

	removals, additions = planSambaACL(entries, []string{guestACEName}, []sambaACE{ace(guestACEName, readACLPerm)})
	if !slices.Equal(removals, []int{8}) || len(additions) != 1 || additions[0].key != guestACEName {
		t.Fatalf("guest permissions changed: removals %v additions %v; want the old entry removed and a new one inserted", removals, additions)
	}

	removals, additions = planSambaACL(entries, []string{guestACEName}, nil)
	if !slices.Equal(removals, []int{8}) || len(additions) != 0 {
		t.Fatalf("turning guests off: removals %v additions %v; the plain everyone@ entry must stay", removals, additions)
	}
}

func TestReconcileSambaACLLeavesACorrectACLAlone(t *testing.T) {
	original := sambaRunCommand
	t.Cleanup(func() { sambaRunCommand = original })
	var calls []string
	sambaRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "/bin/getfacl" {
			return sampleShareACL, nil
		}
		return "", nil
	}
	s := &Service{}
	if err := s.syncSambaShareACL("/mnt/share", sambaPrincipalNames{}, sambaPrincipalNames{}, true, true, true); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("an unchanged guest ACL ran %v", calls)
	}

	calls = nil
	if err := s.reconcileSambaACL("/mnt/share", sambaPrincipalKeys(sambaPrincipalNames{ReadUsers: []string{"1002"}}), sambaPrincipalACEs(sambaPrincipalNames{ReadUsers: []string{"1001"}, WriteGroups: []string{"2001"}}), true); err != nil {
		t.Fatal(err)
	}
	want := []string{"/bin/getfacl -nq /mnt/share", "/bin/setfacl -x 3 /mnt/share", "/bin/setfacl -x 2 /mnt/share"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v; want %v (highest position first)", calls, want)
	}

	calls = nil
	sambaRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "/bin/getfacl" {
			return "", errors.New("no such file")
		}
		return "", nil
	}
	if err := s.syncSambaShareACL("/mnt/share", sambaPrincipalNames{}, sambaPrincipalNames{ReadUsers: []string{"1001"}}, false, false, false); err != nil {
		t.Fatalf("a best-effort sync of an unreadable ACL failed: %v", err)
	}
	if err := s.syncSambaShareACL("/mnt/share", sambaPrincipalNames{}, sambaPrincipalNames{ReadUsers: []string{"1001"}}, false, false, true); err == nil {
		t.Fatal("a strict sync of an unreadable ACL succeeded")
	}
	if want := []string{"/bin/getfacl -nq /mnt/share", "/bin/getfacl -nq /mnt/share"}; !slices.Equal(calls, want) {
		t.Fatalf("an unreadable ACL ran %v; want only the reads", calls)
	}
}

func TestReconcileSambaACLWithRealTools(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("/bin/setfacl"); err != nil {
		t.Skip("setfacl is not available")
	}
	if _, err := utils.RunCommand("/bin/setfacl", "-m", "u:4242:read_set:fd:allow", dir); err != nil {
		t.Skipf("NFSv4 ACLs are not supported here: %v", err)
	}
	if _, err := utils.RunCommand("/bin/setfacl", "-x", "0", dir); err != nil {
		t.Fatal(err)
	}
	original := sambaRunCommand
	t.Cleanup(func() { sambaRunCommand = original })
	var writes []string
	sambaRunCommand = func(command string, args ...string) (string, error) {
		if command == "/bin/setfacl" {
			writes = append(writes, strings.Join(args, " "))
		}
		return utils.RunCommand(command, args...)
	}
	acl := func() string {
		t.Helper()
		output, err := utils.RunCommand("/bin/getfacl", "-nq", dir)
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	baseline := acl()
	s := &Service{}
	sync := func(previous, desired sambaPrincipalNames, guest, guestWriteable bool) {
		t.Helper()
		if err := s.syncSambaShareACL(dir, previous, desired, guest, guestWriteable, true); err != nil {
			t.Fatal(err)
		}
	}
	none := sambaPrincipalNames{}
	desired := sambaPrincipalNames{ReadUsers: []string{"4242"}, WriteGroups: []string{"4343"}}

	sync(none, desired, false, false)
	first := acl()
	if !strings.Contains(first, "user:4242:r-x---a-R-c---:fd-----:allow") || !strings.Contains(first, "group:4343:rwxpDdaARWc--s:fd-----:allow") {
		t.Fatalf("entries were not added:\n%s", first)
	}

	writes = nil
	sync(none, desired, false, false)
	if len(writes) != 0 || acl() != first {
		t.Fatalf("an unchanged sync wrote %v", writes)
	}

	sync(desired, sambaPrincipalNames{WriteUsers: []string{"4242"}}, false, false)
	moved := acl()
	if !strings.Contains(moved, "user:4242:rwxpDdaARWc--s:fd-----:allow") || strings.Contains(moved, "group:4343:") ||
		strings.Count(moved, "user:4242:") != 1 {
		t.Fatalf("promotion and revocation went wrong:\n%s", moved)
	}

	sync(sambaPrincipalNames{WriteUsers: []string{"4242"}}, none, false, false)
	if final := acl(); final != baseline {
		t.Fatalf("removing every principal did not restore the original ACL:\n%s\nwant\n%s", final, baseline)
	}

	sync(none, desired, false, false)
	sync(desired, none, true, false)
	guest := acl()
	if !strings.Contains(guest, "everyone@:r-x---a-R-c---:fd-----:allow") || strings.Contains(guest, "user:4242:") || strings.Contains(guest, "group:4343:") {
		t.Fatalf("switching to guest access went wrong:\n%s", guest)
	}
	writes = nil
	sync(desired, none, true, false)
	if len(writes) != 0 {
		t.Fatalf("an unchanged guest share wrote %v", writes)
	}
	sync(none, none, false, false)
	if final := acl(); final != baseline {
		t.Fatalf("turning guest access off did not restore the original ACL:\n%s", final)
	}
}

func TestSyncSambaShareACLReadsTheACLOnce(t *testing.T) {
	original := sambaRunCommand
	t.Cleanup(func() { sambaRunCommand = original })
	var calls []string
	sambaRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "/bin/getfacl" {
			return sampleShareACL, nil
		}
		return "", nil
	}
	s := &Service{}

	_ = s.syncSambaShareACL("/mnt/share", sambaPrincipalNames{ReadUsers: []string{"1002"}}, sambaPrincipalNames{}, true, true, true)
	if want := []string{"/bin/getfacl -nq /mnt/share", "/bin/setfacl -x 2 /mnt/share"}; !slices.Equal(calls, want) {
		t.Fatalf("guest share calls = %v; want %v", calls, want)
	}

	calls = nil
	_ = s.syncSambaShareACL("/mnt/share", sambaPrincipalNames{}, sambaPrincipalNames{ReadUsers: []string{"1001"}, WriteGroups: []string{"2001"}}, false, false, true)
	if want := []string{"/bin/getfacl -nq /mnt/share", "/bin/setfacl -x 8 /mnt/share", "/bin/setfacl -x 3 /mnt/share"}; !slices.Equal(calls, want) {
		t.Fatalf("signed-in share calls = %v; want %v", calls, want)
	}
}

func TestGuestEntriesAreInsertedSoTheDefaultEveryoneEntryStays(t *testing.T) {
	original := sambaRunCommand
	t.Cleanup(func() { sambaRunCommand = original })
	var calls []string
	sambaRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "/bin/getfacl" {
			return "            owner@:rwxp--aARWcCos:-------:allow\n         everyone@:r-x---a-R-c--s:-------:allow\n", nil
		}
		return "", nil
	}
	if err := (&Service{}).syncSambaShareACL("/mnt/share", sambaPrincipalNames{}, sambaPrincipalNames{}, true, false, true); err != nil {
		t.Fatal(err)
	}
	want := []string{"/bin/getfacl -nq /mnt/share", "/bin/setfacl -a 0 everyone@:read_set/execute:fd:allow /mnt/share"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v; want %v", calls, want)
	}
}
