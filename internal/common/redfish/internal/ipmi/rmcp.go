package ipmi

import (
	"encoding/binary"
	"fmt"
)

const (
	rmcpVersion     = 0x06
	rmcpSeqNoACK    = 0xFF
	rmcpClassIPMI   = 0x07
	authNone        = 0x00
	authRMCPPlus    = 0x06
	payloadIPMI     = 0x00
	payloadSOL      = 0x01
	payloadOpenReq  = 0x10
	payloadOpenResp = 0x11
	payloadRAKP1    = 0x12
	payloadRAKP2    = 0x13
	payloadRAKP3    = 0x14
	payloadRAKP4    = 0x15
	payloadEnc      = 0x80
	payloadAuth     = 0x40
	nextHeaderIPMI  = 0x07
)

func packRMCP(class byte, rest []byte) []byte {
	out := make([]byte, 0, 4+len(rest))
	out = append(out, rmcpVersion, 0x00, rmcpSeqNoACK, class)
	out = append(out, rest...)
	return out
}

func packSessionless(ipmiMsg []byte) []byte {
	rest := make([]byte, 0, 10+len(ipmiMsg))
	rest = append(rest, authNone)
	rest = append(rest, 0, 0, 0, 0) // seq
	rest = append(rest, 0, 0, 0, 0) // sid
	rest = append(rest, byte(len(ipmiMsg)))
	rest = append(rest, ipmiMsg...)
	return packRMCP(rmcpClassIPMI, rest)
}

func parseSessionless(pkt []byte) ([]byte, error) {
	if len(pkt) < 4+10 {
		return nil, fmt.Errorf("ipmi: short session-less packet")
	}
	if pkt[0] != rmcpVersion || pkt[3]&0x0F != rmcpClassIPMI {
		return nil, fmt.Errorf("ipmi: not an IPMI RMCP packet")
	}
	body := pkt[4:]
	if body[0] != authNone {
		return nil, fmt.Errorf("ipmi: unexpected auth type 0x%02x", body[0])
	}
	n := int(body[9])
	if len(body) < 10+n {
		return nil, fmt.Errorf("ipmi: truncated session-less payload")
	}
	return body[10 : 10+n], nil
}

func packRMCPPlus(payloadType byte, sessionID, seq uint32, payload []byte, keys *sessionKeys) ([]byte, error) {
	encrypted := payloadType&payloadEnc != 0
	authenticated := payloadType&payloadAuth != 0
	pt := payloadType & 0x3F
	bodyPayload := payload
	if encrypted {
		if keys == nil {
			return nil, fmt.Errorf("ipmi: encrypted payload without keys")
		}
		enc, err := encryptPayload(keys.aesKey(), payload)
		if err != nil {
			return nil, err
		}
		bodyPayload = enc
	}

	rest := make([]byte, 0, 12+len(bodyPayload)+20)
	rest = append(rest, authRMCPPlus, payloadType)
	rest = append(rest, le32(sessionID)...)
	rest = append(rest, le32(seq)...)
	ln := make([]byte, 2)
	binary.LittleEndian.PutUint16(ln, uint16(len(bodyPayload)))
	rest = append(rest, ln...)
	rest = append(rest, bodyPayload...)

	if authenticated {
		if keys == nil {
			return nil, fmt.Errorf("ipmi: authenticated payload without keys")
		}
		// Integrity PAD so AuthType through Next Header is DWORD-aligned.
		n := len(rest)
		padLen := (4 - ((n + 2) % 4)) % 4
		for i := 0; i < padLen; i++ {
			rest = append(rest, 0xFF)
		}
		rest = append(rest, byte(padLen), nextHeaderIPMI)
		mac := keys.suite.hmacSum(keys.k1, rest)
		rest = append(rest, mac[:keys.suite.authCodeSize()]...)
	} else if pt >= payloadOpenReq && pt <= payloadRAKP4 {
		// Table 13-8 notes [8]/[9]: Open Session and RAKP have no trailer.
	}
	return packRMCP(rmcpClassIPMI, rest), nil
}

type plusPkt struct {
	payloadType byte // including enc/auth bits
	sessionID   uint32
	seq         uint32
	payload     []byte // decrypted / plain
}

func parseRMCPPlus(pkt []byte, keys *sessionKeys) (plusPkt, error) {
	var z plusPkt
	if len(pkt) < 4+12 {
		return z, fmt.Errorf("ipmi: short RMCP+ packet")
	}
	if pkt[0] != rmcpVersion || pkt[3]&0x0F != rmcpClassIPMI {
		return z, fmt.Errorf("ipmi: not an IPMI RMCP packet")
	}
	rest := pkt[4:]
	if rest[0] != authRMCPPlus {
		return z, fmt.Errorf("ipmi: not RMCP+ (auth=0x%02x)", rest[0])
	}
	pt := rest[1]
	z.payloadType = pt
	z.sessionID = binary.LittleEndian.Uint32(rest[2:6])
	z.seq = binary.LittleEndian.Uint32(rest[6:10])
	plen := int(binary.LittleEndian.Uint16(rest[10:12]))
	if len(rest) < 12+plen {
		return z, fmt.Errorf("ipmi: truncated RMCP+ payload")
	}
	raw := rest[12 : 12+plen]
	authenticated := pt&payloadAuth != 0
	encrypted := pt&payloadEnc != 0
	if authenticated {
		if keys == nil {
			return z, fmt.Errorf("ipmi: authenticated packet without keys")
		}
		authn := keys.suite.authCodeSize()
		// trailer after payload: pad, padLen, nextHeader, authcode
		trail := rest[12+plen:]
		if len(trail) < 2+authn {
			return z, fmt.Errorf("ipmi: missing integrity trailer")
		}
		padLen := int(trail[len(trail)-2-authn])
		// covered = AuthType .. Next Header
		coveredLen := 12 + plen + padLen + 2
		if len(rest) < coveredLen+authn {
			return z, fmt.Errorf("ipmi: short integrity covered range")
		}
		got := trail[len(trail)-authn:]
		sum := keys.suite.hmacSum(keys.k1, rest[:coveredLen])
		if !macEqual(got, sum[:authn]) {
			return z, fmt.Errorf("ipmi: integrity check failed")
		}
	}
	if encrypted {
		if keys == nil {
			return z, fmt.Errorf("ipmi: encrypted packet without keys")
		}
		plain, err := decryptPayload(keys.aesKey(), raw)
		if err != nil {
			return z, err
		}
		z.payload = plain
	} else {
		z.payload = raw
	}
	return z, nil
}
