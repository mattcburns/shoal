package sol_test

import (
	"context"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func TestCombinedTransportFactoryDispatch(t *testing.T) {
	rfCfg := sol.RedfishSOLConfig{
		NewBMC:  func(redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Secrets: secrets.NewMemory(),
	}
	sshCfg := sol.SSHSerialConfig{} // empty Host => local libvirt path

	factory := sol.NewCombinedTransportFactory(rfCfg, sshCfg)

	t.Run("redfish_sol dispatches to RedfishTransport", func(t *testing.T) {
		tr := factory(models.WatchSession{Transport: "redfish_sol", Target: "http://bmc"})
		if _, ok := tr.(*sol.RedfishTransport); !ok {
			t.Fatalf("got %T, want *sol.RedfishTransport", tr)
		}
	})

	t.Run("libvirt dispatches to local LibvirtTransport (no ssh host configured)", func(t *testing.T) {
		tr := factory(models.WatchSession{Transport: "libvirt", Target: "domain-1"})
		if _, ok := tr.(*sol.LibvirtTransport); !ok {
			t.Fatalf("got %T, want *sol.LibvirtTransport", tr)
		}
	})

	t.Run("empty transport dispatches to local LibvirtTransport", func(t *testing.T) {
		tr := factory(models.WatchSession{Target: "domain-1"})
		if _, ok := tr.(*sol.LibvirtTransport); !ok {
			t.Fatalf("got %T, want *sol.LibvirtTransport", tr)
		}
	})

	t.Run("unknown transport errors, never falls back to libvirt", func(t *testing.T) {
		tr := factory(models.WatchSession{Transport: "bogus", Target: "x"})
		if _, ok := tr.(*sol.LibvirtTransport); ok {
			t.Fatal("unknown transport must not silently become LibvirtTransport")
		}
		_, err := tr.Open(context.Background(), "x")
		if err == nil {
			t.Fatal("expected Open to error for unknown transport")
		}
	})

	t.Run("legacy ipmi_sol errors, never attempts raw IPMI or libvirt", func(t *testing.T) {
		tr := factory(models.WatchSession{Transport: "ipmi_sol", Target: "x"})
		if _, ok := tr.(*sol.LibvirtTransport); ok {
			t.Fatal("ipmi_sol must not silently become LibvirtTransport")
		}
		if _, ok := tr.(*sol.RedfishTransport); ok {
			t.Fatal("ipmi_sol must not be routed to the Redfish transport")
		}
		_, err := tr.Open(context.Background(), "x")
		if err == nil {
			t.Fatal("expected Open to error for ipmi_sol")
		}
	})
}

// TestCombinedTransportFactorySSHHost proves the libvirt/SSH dispatch still
// honors an SSH delegate host, matching NewTransportFactory's own contract.
func TestCombinedTransportFactorySSHHost(t *testing.T) {
	rfCfg := sol.RedfishSOLConfig{
		NewBMC: func(redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
	}
	sshCfg := sol.SSHSerialConfig{Host: "lab-host"}
	factory := sol.NewCombinedTransportFactory(rfCfg, sshCfg)

	tr := factory(models.WatchSession{Transport: "libvirt", Target: "domain-1"})
	if _, ok := tr.(*sol.SSHLibvirtTransport); !ok {
		t.Fatalf("got %T, want *sol.SSHLibvirtTransport", tr)
	}

	// Absolute path targets always stay local regardless of configured SSH host.
	tr2 := factory(models.WatchSession{Transport: "libvirt", Target: "/dev/pts/3"})
	if _, ok := tr2.(*sol.LibvirtTransport); !ok {
		t.Fatalf("got %T, want *sol.LibvirtTransport for absolute path target", tr2)
	}
}
