package sol_test

import (
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func TestNewTransportFactorySSH(t *testing.T) {
	f := sol.NewTransportFactory(sol.SSHSerialConfig{
		Host:    "192.168.122.100",
		User:    "lab",
		UseSudo: true,
	})
	tr := f(models.WatchSession{Transport: "libvirt", Target: "shoal-node-1"})
	if _, ok := tr.(*sol.SSHLibvirtTransport); !ok {
		t.Fatalf("want SSHLibvirtTransport, got %T", tr)
	}
	tr = f(models.WatchSession{Target: "/dev/pts/0"})
	if _, ok := tr.(*sol.LibvirtTransport); !ok {
		t.Fatalf("want LibvirtTransport for path, got %T", tr)
	}
}

func TestNewTransportFactoryLocal(t *testing.T) {
	f := sol.NewTransportFactory(sol.SSHSerialConfig{})
	tr := f(models.WatchSession{Target: "shoal-node-1"})
	if _, ok := tr.(*sol.LibvirtTransport); !ok {
		t.Fatalf("want local LibvirtTransport, got %T", tr)
	}
}
