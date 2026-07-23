package jail

import (
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
)

func TestSimpleJailListItemRetainsResourceLimits(t *testing.T) {
	resourceLimits := true
	jail := jailModels.Jail{
		ID:             1,
		CTID:           101,
		Name:           "limited",
		ResourceLimits: &resourceLimits,
		Cores:          2,
		Memory:         128 * 1024 * 1024,
	}

	got := simpleJailListItem(jail, "ACTIVE")
	if got.ID != jail.ID || got.CTID != jail.CTID || got.Name != jail.Name || got.State != "ACTIVE" {
		t.Fatalf("simple jail identity = %#v", got)
	}
	if got.ResourceLimits != jail.ResourceLimits || got.Cores != jail.Cores || got.Memory != jail.Memory {
		t.Fatalf("simple jail limits = %#v", got)
	}
}
