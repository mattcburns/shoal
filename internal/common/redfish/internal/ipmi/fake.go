package ipmi

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"sync"
)

// TestBMC is an in-process RMCP+/SOL responder for DialSOL unit tests.
// Production code never calls this. HMAC for RAKP 2 is computed here with
// concatenations written out locally (not DialSOL's kecc2Input).
type TestBMC struct {
	Addr *net.UDPAddr
	conn *net.UDPConn
	opts TestBMCOptions

	mu     sync.Mutex
	closed bool
}

type TestBMCOptions struct {
	Username, Password string
	RejectSuite3       bool
	Greet              []byte // optional unsolicited SOL bytes after Activate Payload
}

func StartTestBMC(opts TestBMCOptions) (*TestBMC, error) {
	if opts.Username == "" {
		opts.Username = "admin"
	}
	if opts.Password == "" {
		opts.Password = "password"
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return nil, err
	}
	f := &TestBMC{Addr: conn.LocalAddr().(*net.UDPAddr), conn: conn, opts: opts}
	go f.serve()
	return f, nil
}

func (f *TestBMC) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return f.conn.Close()
}

type fakePeer struct {
	sidc, sidm   uint32
	suite        cipherSuite
	keys         sessionKeys
	rc, rm, guid []byte
	seqOut       uint32
	solSeq       byte
	ready        bool
}

func (f *TestBMC) serve() {
	peer := &fakePeer{sidm: 0x01020304, seqOut: 1}
	peer.rm = bytesOf(0x11, 16)
	peer.guid = bytesOf(0xA1, 16)
	buf := make([]byte, 4096)
	for {
		n, addr, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		pkt := append([]byte{}, buf[:n]...)
		f.handle(addr, pkt, peer)
	}
}

func bytesOf(start byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

func (f *TestBMC) handle(addr *net.UDPAddr, pkt []byte, peer *fakePeer) {
	if len(pkt) < 14 {
		return
	}
	if pkt[4] == authNone {
		f.handleSessionless(addr, pkt)
		return
	}
	var keys *sessionKeys
	if peer.ready {
		keys = &peer.keys
	}
	plus, err := parseRMCPPlus(pkt, keys)
	if err != nil {
		return
	}
	switch plus.payloadType & 0x3F {
	case payloadOpenReq:
		f.handleOpen(addr, plus.payload, peer)
	case payloadRAKP1:
		f.handleRAKP1(addr, plus.payload, peer)
	case payloadRAKP3:
		f.handleRAKP3(addr, plus.payload, peer)
	case payloadIPMI:
		f.handleIPMI(addr, plus.payload, peer)
	case payloadSOL:
		f.handleSOL(addr, plus.payload, peer)
	}
}

func (f *TestBMC) handleSessionless(addr *net.UDPAddr, pkt []byte) {
	msg, err := parseSessionless(pkt)
	if err != nil || len(msg) < 8 {
		return
	}
	cmd := msg[5]
	if cmd != cmdGetAuth {
		return
	}
	data := msg[6 : len(msg)-1]
	if len(data) < 2 || data[0] != 0x0E || data[1] != 0x84 {
		return
	}
	rqseq := msg[4]
	respData := []byte{0x01, 0x80, 0x00, 0x02} // channel, ext support, enable, RMCP+
	resp := packLANResponse(cmd, rqseq, ccOK, respData)
	_, _ = f.conn.WriteToUDP(packSessionless(resp), addr)
}

func packLANResponse(cmd, rqseqlun, cc byte, data []byte) []byte {
	netfnlun := byte((netFnApp + 1) << 2)
	msg := []byte{rqSWID, netfnlun, 0, rsBMC, rqseqlun, cmd, cc}
	msg = append(msg, data...)
	msg[2] = checksum(msg[:2])
	msg = append(msg, checksum(msg[3:]))
	return msg
}

func (f *TestBMC) replyPlus(addr *net.UDPAddr, pt byte, sid, seq uint32, payload []byte, keys *sessionKeys) {
	pkt, err := packRMCPPlus(pt, sid, seq, payload, keys)
	if err != nil {
		return
	}
	_, _ = f.conn.WriteToUDP(pkt, addr)
}

func (f *TestBMC) handleOpen(addr *net.UDPAddr, p []byte, peer *fakePeer) {
	if len(p) < 32 {
		return
	}
	tag := p[0]
	sidc := binary.LittleEndian.Uint32(p[4:8])
	authAlg := p[12]
	integAlg := p[20]
	status := byte(0)
	var suite cipherSuite
	switch {
	case authAlg == 0x01 && integAlg == 0x01:
		if f.opts.RejectSuite3 {
			status = 0x01
		} else {
			suite = suite3
		}
	case authAlg == 0x03 && integAlg == 0x04:
		suite = suite17
	default:
		status = 0x01
	}
	resp := make([]byte, 36)
	resp[0] = tag
	resp[1] = status
	resp[2] = 0x04
	copy(resp[4:8], le32(sidc))
	copy(resp[8:12], le32(peer.sidm))
	resp[12] = 0x00
	resp[14], resp[15] = 0x00, 0x08
	resp[16] = authAlg
	resp[20] = 0x01
	resp[22], resp[23] = 0x00, 0x08
	resp[24] = integAlg
	resp[28] = 0x02
	resp[30], resp[31] = 0x00, 0x08
	resp[32] = 0x01
	if status == 0 {
		peer.sidc = sidc
		peer.suite = suite
	}
	f.replyPlus(addr, payloadOpenResp, 0, 0, resp, nil)
}

func (f *TestBMC) handleRAKP1(addr *net.UDPAddr, p []byte, peer *fakePeer) {
	if len(p) < 29 {
		return
	}
	tag := p[0]
	peer.rc = append([]byte{}, p[8:24]...)
	role := p[24]
	ulen := p[27]
	if int(28+ulen) > len(p) {
		return
	}
	user := p[28 : 28+ulen]
	kecc := fakeKECC2(peer.suite, f.opts.Password, peer.sidc, peer.sidm, peer.rc, peer.rm, peer.guid, role, ulen, user)
	resp := make([]byte, 0, 40+len(kecc))
	resp = append(resp, tag, 0x00, 0x00, 0x00)
	resp = append(resp, le32(peer.sidc)...)
	resp = append(resp, peer.rm...)
	resp = append(resp, peer.guid...)
	resp = append(resp, kecc...)
	peer.keys = fakeDerive(peer.suite, f.opts.Password, peer.rc, peer.rm, role, ulen, user)
	f.replyPlus(addr, payloadRAKP2, 0, 0, resp, nil)
}

func (f *TestBMC) handleRAKP3(addr *net.UDPAddr, p []byte, peer *fakePeer) {
	if len(p) < 8 {
		return
	}
	tag := p[0]
	icv := fakeICV(peer.suite, peer.keys.sik, peer.rm, peer.sidc, peer.guid)
	resp := make([]byte, 0, 8+len(icv))
	resp = append(resp, tag, 0x00, 0x00, 0x00)
	resp = append(resp, le32(peer.sidc)...)
	resp = append(resp, icv...)
	peer.ready = true
	f.replyPlus(addr, payloadRAKP4, 0, 0, resp, nil)
}

func (f *TestBMC) handleIPMI(addr *net.UDPAddr, msg []byte, peer *fakePeer) {
	if len(msg) < 8 {
		return
	}
	cmd := msg[5]
	rqseq := msg[4]
	var data []byte
	switch cmd {
	case cmdSetPriv:
		data = nil
	case cmdAct:
		data = []byte{0, 0, 0, 0, 0xff, 0x00, 0xff, 0x00, 0, 0, 0xff, 0xff}
	case cmdDeact, cmdClose:
		data = nil
	default:
		resp := packLANResponse(cmd, rqseq, 0xC1, nil)
		f.replyPlus(addr, payloadIPMI|payloadEnc|payloadAuth, peer.sidc, peer.seqOut, resp, &peer.keys)
		peer.seqOut++
		return
	}
	resp := packLANResponse(cmd, rqseq, ccOK, data)
	f.replyPlus(addr, payloadIPMI|payloadEnc|payloadAuth, peer.sidc, peer.seqOut, resp, &peer.keys)
	peer.seqOut++
	if cmd == cmdAct && len(f.opts.Greet) > 0 {
		peer.solSeq = 1
		out := []byte{peer.solSeq, 0, byte(len(f.opts.Greet)), 0}
		out = append(out, f.opts.Greet...)
		f.replyPlus(addr, payloadSOL|payloadEnc|payloadAuth, peer.sidc, peer.seqOut, out, &peer.keys)
		peer.seqOut++
	}
}

func (f *TestBMC) handleSOL(addr *net.UDPAddr, body []byte, peer *fakePeer) {
	if len(body) < 4 {
		return
	}
	seq := body[0] & 0x0F
	chars := body[4:]
	if seq == 0 || len(chars) == 0 {
		return
	}
	peer.solSeq++
	if peer.solSeq == 0 || peer.solSeq > 15 {
		peer.solSeq = 1
	}
	out := []byte{peer.solSeq, seq, byte(len(chars)), 0}
	out = append(out, chars...)
	f.replyPlus(addr, payloadSOL|payloadEnc|payloadAuth, peer.sidc, peer.seqOut, out, &peer.keys)
	peer.seqOut++
}

// Independent concatenations (do not call kecc2Input / deriveKeys).
func fakeKECC2(suite cipherSuite, password string, sidc, sidm uint32, rc, rm, guid []byte, role, ulen byte, user []byte) []byte {
	kuid := make([]byte, 20)
	copy(kuid, password)
	var in []byte
	in = append(in, le32(sidc)...)
	in = append(in, le32(sidm)...)
	in = append(in, rc...)
	in = append(in, rm...)
	in = append(in, guid...)
	in = append(in, role, ulen)
	in = append(in, user...)
	return fakeHMAC(suite, kuid, in)
}

func fakeDerive(suite cipherSuite, password string, rc, rm []byte, role, ulen byte, user []byte) sessionKeys {
	kuid := make([]byte, 20)
	copy(kuid, password)
	var sikIn []byte
	sikIn = append(sikIn, rc...)
	sikIn = append(sikIn, rm...)
	sikIn = append(sikIn, role, ulen)
	sikIn = append(sikIn, user...)
	sik := fakeHMAC(suite, kuid, sikIn)
	n := sha1.Size
	if suite == suite17 {
		n = sha256.Size
	}
	c1 := bytesRepeat(0x01, n)
	c2 := bytesRepeat(0x02, n)
	return sessionKeys{
		suite: suite,
		kuid:  kuid,
		sik:   sik,
		k1:    fakeHMAC(suite, sik, c1),
		k2:    fakeHMAC(suite, sik, c2),
	}
}

func fakeICV(suite cipherSuite, sik, rm []byte, sidc uint32, guid []byte) []byte {
	var in []byte
	in = append(in, rm...)
	in = append(in, le32(sidc)...)
	in = append(in, guid...)
	sum := fakeHMAC(suite, sik, in)
	n := 12
	if suite == suite17 {
		n = 16
	}
	return sum[:n]
}

func fakeHMAC(suite cipherSuite, key, data []byte) []byte {
	var h interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
	if suite == suite17 {
		h = hmac.New(sha256.New, key)
	} else {
		h = hmac.New(sha1.New, key)
	}
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func bytesRepeat(v byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}
