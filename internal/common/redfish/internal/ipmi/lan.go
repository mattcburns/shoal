package ipmi

import "fmt"

const (
	rsBMC      = 0x20
	rqSWID     = 0x81
	netFnApp   = 0x06
	cmdGetAuth = 0x38
	cmdSetPriv = 0x3B
	cmdClose   = 0x3C
	cmdAct     = 0x48
	cmdDeact   = 0x49
	ccOK       = 0x00
)

func checksum(b []byte) byte {
	var s byte
	for _, v := range b {
		s += v
	}
	return -s
}

func packLANRequest(netFn, cmd, rqSeq byte, data []byte) []byte {
	netfnlun := netFn << 2
	rqseqlun := rqSeq << 2
	msg := []byte{rsBMC, netfnlun, 0, rqSWID, rqseqlun, cmd}
	msg = append(msg, data...)
	msg[2] = checksum(msg[:2])
	msg = append(msg, checksum(msg[3:]))
	return msg
}

func parseLANResponse(msg []byte) (cmd, cc byte, data []byte, err error) {
	if len(msg) < 8 {
		return 0, 0, nil, fmt.Errorf("ipmi: short LAN response")
	}
	cmd = msg[5]
	cc = msg[6]
	data = msg[7 : len(msg)-1]
	return cmd, cc, data, nil
}
