package libvirt

import (
	"testing"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/digitalocean/go-libvirt"
)

func TestApplyDomainStatesUpdatesSliceElements(t *testing.T) {
	vms := []vmModels.VM{
		{RID: 101, State: libvirt.DomainRunning},
		{RID: 102, State: libvirt.DomainRunning},
	}
	states := []libvirtServiceInterfaces.DomainState{{
		Domain: "101",
		State:  libvirt.DomainShutoff,
	}}

	applyDomainStates(vms, states)

	if vms[0].State != libvirt.DomainShutoff {
		t.Fatalf("VM 101 state = %d, want %d", vms[0].State, libvirt.DomainShutoff)
	}
	if vms[1].State != 0 {
		t.Fatalf("VM 102 state = %d, want unknown state 0", vms[1].State)
	}
}
