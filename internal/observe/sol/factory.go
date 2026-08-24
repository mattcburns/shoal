package sol

import (
	"fmt"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
)

// RedfishSOLConfig configures the native Redfish SOL transport shared across
// all watch sessions (per-job data — BMC URL, system id, credential ref —
// comes from the WatchSession itself).
type RedfishSOLConfig struct {
	NewBMC   redfish.Factory // e.g. redfish.NewBMC
	Secrets  secrets.Backend // resolves WatchSession.CredentialRef
	AuthMode string          // mirrors Config.RedfishAuthMode
	TLSMode  string          // mirrors Config.RedfishTLSMode
	CAFile   string          // mirrors Config.RedfishCAFile
}

// NewCombinedTransportFactory dispatches on session.Transport:
//   - "redfish_sol": BMC.OpenSOL (line-oriented WS, then SSH attach). IPMI SOL
//     last-resort is not implemented yet; OpenSOL returns unsupported for
//     IPMI-only BMCs. Never a watch transport named ipmi_sol.
//   - "libvirt", "": existing SSH/local libvirt tailing (delegates to
//     NewTransportFactory(sshCfg)).
//   - anything else (including the legacy "ipmi_sol"): a transport whose
//     Open() always errors — no silent fallback to libvirt, and raw IPMI is
//     never attempted.
func NewCombinedTransportFactory(rfCfg RedfishSOLConfig, sshCfg SSHSerialConfig) func(models.WatchSession) Transport {
	sshFactory := NewTransportFactory(sshCfg)
	return func(session models.WatchSession) Transport {
		switch session.Transport {
		case "redfish_sol":
			return &RedfishTransport{
				NewBMC:        rfCfg.NewBMC,
				AuthMode:      rfCfg.AuthMode,
				TLSMode:       rfCfg.TLSMode,
				CAFile:        rfCfg.CAFile,
				SystemID:      session.RedfishSystemID,
				Secrets:       rfCfg.Secrets,
				CredentialRef: session.CredentialRef,
			}
		case "libvirt", "":
			return sshFactory(session)
		default:
			return &errorTransport{err: fmt.Errorf("sol: unsupported serial transport %q", session.Transport)}
		}
	}
}
