package ipmi

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// Config is the only IPMI surface OpenSOL needs. No chassis, power, or SEL.
// Never fmt %+v this struct (it contains Password).
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	Timeout  time.Duration
}

func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 2 * time.Second
}

func (c Config) port() int {
	if c.Port > 0 {
		return c.Port
	}
	return 623
}

// DialSOL opens a bidirectional SOL byte stream (payload type 1) over
// RMCP+ cipher suite 3, then suite 17 if Open Session rejects 3.
// ctx cancel / Close deactivates payload and closes the RMCP+ session.
func DialSOL(ctx context.Context, cfg Config) (io.ReadWriteCloser, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("ipmi: empty host")
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.port()))
	d := net.Dialer{}
	c, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, fmt.Errorf("ipmi: udp dial: %w", err)
	}
	udp, ok := c.(*net.UDPConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("ipmi: not a udp conn")
	}
	s := &sess{cfg: cfg, conn: udp}
	if err := s.handshake(ctx); err != nil {
		_ = udp.Close()
		return nil, err
	}
	return newSOL(ctx, s), nil
}

type sess struct {
	cfg          Config
	conn         *net.UDPConn
	keys         sessionKeys
	sidc, sidm   uint32
	seqOut       uint32
	rqSeq        byte
	tag          byte
	suite        cipherSuite
	solSeq       byte
	lastBMCSeq   byte
	lastAccepted byte
}

func (s *sess) nextTag() byte {
	s.tag++
	return s.tag
}

func (s *sess) nextRQ() byte {
	s.rqSeq = (s.rqSeq + 1) & 0x3F
	return s.rqSeq
}

func (s *sess) write(ctx context.Context, pkt []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.cfg.timeout()))
	_, err := s.conn.Write(pkt)
	return err
}

func (s *sess) readUDP(ctx context.Context) ([]byte, error) {
	buf := make([]byte, 4096)
	_ = s.conn.SetReadDeadline(time.Now().Add(s.cfg.timeout()))
	n, err := s.conn.Read(buf)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return buf[:n], nil
}

func (s *sess) handshake(ctx context.Context) error {
	if err := s.getChannelAuth(ctx); err != nil {
		return err
	}
	suite := suite3
	if err := s.openSession(ctx, suite); err != nil {
		if isOpenReject(err) {
			suite = suite17
			if err2 := s.openSession(ctx, suite); err2 != nil {
				return err2
			}
		} else {
			return err
		}
	}
	s.suite = suite
	return s.rakpAndActivate(ctx)
}

type openReject struct{ status byte }

func (e openReject) Error() string {
	return fmt.Sprintf("ipmi: cipher suite rejected status=0x%02x", e.status)
}

func isOpenReject(err error) bool {
	_, ok := err.(openReject)
	return ok
}

func (s *sess) getChannelAuth(ctx context.Context) error {
	req := packLANRequest(netFnApp, cmdGetAuth, s.nextRQ(), []byte{0x0E, 0x84})
	pkt := packSessionless(req)
	var last error
	for try := 0; try < 3; try++ {
		if err := s.write(ctx, pkt); err != nil {
			return fmt.Errorf("ipmi: get channel auth: %w", err)
		}
		raw, err := s.readUDP(ctx)
		if err != nil {
			last = err
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		msg, err := parseSessionless(raw)
		if err != nil {
			last = err
			continue
		}
		_, cc, data, err := parseLANResponse(msg)
		if err != nil {
			last = err
			continue
		}
		if cc != ccOK {
			return fmt.Errorf("ipmi: get channel auth cc=0x%02x", cc)
		}
		if len(data) < 4 {
			return fmt.Errorf("ipmi: get channel auth short data")
		}
		if data[1]&0x80 == 0 {
			return fmt.Errorf("ipmi: no RMCP+ (ext caps bit7=0)")
		}
		if data[3]&0x02 == 0 {
			return fmt.Errorf("ipmi: no RMCP+ (ext caps bit1=0)")
		}
		return nil
	}
	if last != nil {
		if ne, ok := last.(net.Error); ok && ne.Timeout() {
			return fmt.Errorf("ipmi: udp 623 timeout on Get Channel Auth Caps")
		}
	}
	return fmt.Errorf("ipmi: udp 623 timeout on Get Channel Auth Caps")
}

func (s *sess) openSession(ctx context.Context, suite cipherSuite) error {
	if s.sidc == 0 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		s.sidc = binary.LittleEndian.Uint32(b[:])
		if s.sidc == 0 {
			s.sidc = 1
		}
	} else {
		// new SIDc allowed on suite 17 retry
		var b [4]byte
		_, _ = rand.Read(b[:])
		s.sidc = binary.LittleEndian.Uint32(b[:])
		if s.sidc == 0 {
			s.sidc = 1
		}
	}
	tag := s.nextTag()
	payload := packOpenSession(tag, s.sidc, suite)
	pkt, err := packRMCPPlus(payloadOpenReq, 0, 0, payload, nil)
	if err != nil {
		return err
	}
	if err := s.write(ctx, pkt); err != nil {
		return fmt.Errorf("ipmi: open session: %w", err)
	}
	raw, err := s.readUDP(ctx)
	if err != nil {
		return fmt.Errorf("ipmi: open session: %w", err)
	}
	plus, err := parseRMCPPlus(raw, nil)
	if err != nil {
		return err
	}
	if plus.payloadType&0x3F != payloadOpenResp {
		return fmt.Errorf("ipmi: expected open session response")
	}
	p := plus.payload
	if len(p) < 12 {
		return fmt.Errorf("ipmi: short open session response")
	}
	status := p[1]
	if status != 0 {
		return openReject{status: status}
	}
	s.sidm = binary.LittleEndian.Uint32(p[8:12])
	s.suite = suite
	return nil
}

func packOpenSession(tag byte, sidc uint32, suite cipherSuite) []byte {
	b := make([]byte, 32)
	b[0] = tag
	b[1] = 0x04
	copy(b[4:8], le32(sidc))
	b[8] = 0x00
	b[10], b[11] = 0x00, 0x08
	b[12] = suite.authAlg()
	b[16] = 0x01
	b[18], b[19] = 0x00, 0x08
	b[20] = suite.integAlg()
	b[24] = 0x02
	b[26], b[27] = 0x00, 0x08
	b[28] = 0x01
	return b
}

func (s *sess) rakpAndActivate(ctx context.Context) error {
	rc := make([]byte, 16)
	if _, err := rand.Read(rc); err != nil {
		return err
	}
	user := []byte(s.cfg.Username)
	if len(user) > 16 {
		user = user[:16]
	}
	ulen := byte(len(user))
	role := roleAdminNameOnly
	tag := s.nextTag()
	rakp1 := packRAKP1(tag, s.sidm, rc, role, ulen, user)
	pkt, err := packRMCPPlus(payloadRAKP1, 0, 0, rakp1, nil)
	if err != nil {
		return err
	}
	if err := s.write(ctx, pkt); err != nil {
		return fmt.Errorf("ipmi: rakp1: %w", err)
	}
	raw, err := s.readUDP(ctx)
	if err != nil {
		return fmt.Errorf("ipmi: rakp2: %w", err)
	}
	plus, err := parseRMCPPlus(raw, nil)
	if err != nil {
		return err
	}
	if plus.payloadType&0x3F != payloadRAKP2 {
		return fmt.Errorf("ipmi: expected rakp2")
	}
	p := plus.payload
	keccLen := s.suite.hmacSize()
	if len(p) < 40+keccLen {
		return fmt.Errorf("ipmi: short rakp2")
	}
	if p[1] != 0 {
		return fmt.Errorf("ipmi: rakp2 status=0x%02x", p[1])
	}
	rm := p[8:24]
	guid := append([]byte{}, p[24:40]...)
	gotKECC := p[40 : 40+keccLen]
	s.keys = deriveKeys(s.suite, s.cfg.Password, rc, rm, role, ulen, user)
	want := s.keys.kecc2(s.sidc, s.sidm, rc, rm, guid, role, ulen, user)
	if !macEqual(gotKECC, want) {
		return fmt.Errorf("ipmi: rakp2 kecc mismatch")
	}

	kecc3 := s.keys.kecc3(rm, s.sidc, role, ulen, user)
	rakp3 := make([]byte, 0, 8+len(kecc3))
	rakp3 = append(rakp3, s.nextTag(), 0x00, 0x00, 0x00)
	rakp3 = append(rakp3, le32(s.sidm)...)
	rakp3 = append(rakp3, kecc3...)
	pkt, err = packRMCPPlus(payloadRAKP3, 0, 0, rakp3, nil)
	if err != nil {
		return err
	}
	if err := s.write(ctx, pkt); err != nil {
		return fmt.Errorf("ipmi: rakp3: %w", err)
	}
	raw, err = s.readUDP(ctx)
	if err != nil {
		return fmt.Errorf("ipmi: rakp4: %w", err)
	}
	plus, err = parseRMCPPlus(raw, nil)
	if err != nil {
		return err
	}
	if plus.payloadType&0x3F != payloadRAKP4 {
		return fmt.Errorf("ipmi: expected rakp4")
	}
	p = plus.payload
	icvN := s.suite.authCodeSize()
	if len(p) < 8+icvN {
		return fmt.Errorf("ipmi: short rakp4")
	}
	if p[1] != 0 {
		return fmt.Errorf("ipmi: rakp4 status=0x%02x", p[1])
	}
	if !macEqual(p[8:8+icvN], s.keys.icv(rm, s.sidc, guid)) {
		return fmt.Errorf("ipmi: rakp4 icv mismatch")
	}

	s.seqOut = 1
	if _, err := s.ipmiCmd(ctx, cmdSetPriv, []byte{0x04}); err != nil {
		return fmt.Errorf("ipmi: set privilege: %w", err)
	}
	if _, err := s.ipmiCmd(ctx, cmdAct, []byte{0x01, 0x01, 0xC0, 0x00, 0x00, 0x00}); err != nil {
		return fmt.Errorf("ipmi: activate payload: %w", err)
	}
	return nil
}

func (s *sess) ipmiCmd(ctx context.Context, cmd byte, data []byte) ([]byte, error) {
	msg := packLANRequest(netFnApp, cmd, s.nextRQ(), data)
	pt := byte(payloadIPMI | payloadEnc | payloadAuth)
	pkt, err := packRMCPPlus(pt, s.sidm, s.seqOut, msg, &s.keys)
	if err != nil {
		return nil, err
	}
	s.seqOut++
	if err := s.write(ctx, pkt); err != nil {
		return nil, err
	}
	raw, err := s.readUDP(ctx)
	if err != nil {
		return nil, err
	}
	plus, err := parseRMCPPlus(raw, &s.keys)
	if err != nil {
		return nil, err
	}
	if plus.payloadType&0x3F != payloadIPMI {
		return nil, fmt.Errorf("ipmi: expected IPMI payload, got 0x%02x", plus.payloadType)
	}
	_, cc, resp, err := parseLANResponse(plus.payload)
	if err != nil {
		return nil, err
	}
	if cc != ccOK {
		return nil, fmt.Errorf("ipmi: cmd 0x%02x cc=0x%02x", cmd, cc)
	}
	return resp, nil
}

func (s *sess) sendSOL(ctx context.Context, seq, ack, accepted, op byte, chars []byte) error {
	body := []byte{seq & 0x0F, ack & 0x0F, accepted, op}
	body = append(body, chars...)
	pt := byte(payloadSOL | payloadEnc | payloadAuth)
	pkt, err := packRMCPPlus(pt, s.sidm, s.seqOut, body, &s.keys)
	if err != nil {
		return err
	}
	s.seqOut++
	return s.write(ctx, pkt)
}

func (s *sess) deactivate(ctx context.Context) {
	_, _ = s.ipmiCmd(ctx, cmdDeact, []byte{0x01, 0x01, 0x00, 0x00, 0x00, 0x00})
	_, _ = s.ipmiCmd(ctx, cmdClose, le32(s.sidm))
}
