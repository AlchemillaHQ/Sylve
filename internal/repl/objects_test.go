package repl

import "testing"

func TestBuildConsoleObjectRequestNormalizesNetworkType(t *testing.T) {
	request, err := buildConsoleObjectRequest([]string{
		"lan4", "networks", " 192.0.2.0/24 ", "198.51.100.0/24",
	})
	if err != nil {
		t.Fatalf("build object request: %v", err)
	}
	if request.Name != "lan4" || request.Type != "Network" {
		t.Fatalf("unexpected object request: %#v", request)
	}
	if len(request.Values) != 2 || request.Values[0] != "192.0.2.0/24" || request.Values[1] != "198.51.100.0/24" {
		t.Fatalf("unexpected object values: %#v", request.Values)
	}
}

func TestNormalizeObjectTypeRejectsUnknownType(t *testing.T) {
	if _, err := normalizeObjectType("unknown"); err == nil || err.Error() != "invalid_object_type" {
		t.Fatalf("expected invalid object type, got %v", err)
	}
}

func TestBuildConsoleObjectEditRequestPatchesValuesOnly(t *testing.T) {
	id, request, err := buildConsoleObjectEditRequest([]string{
		"1", "--value", "16:8C:61:52:FF:60",
	})
	if err != nil {
		t.Fatalf("build object edit request: %v", err)
	}
	if id != 1 || request.Values == nil || len(*request.Values) != 1 || (*request.Values)[0] != "16:8C:61:52:FF:60" {
		t.Fatalf("unexpected object edit request: %#v", request)
	}
	if request.Name != nil || request.Type != nil {
		t.Fatalf("expected name and type to remain absent: %#v", request)
	}
}
