package ipmi

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

// cipherSuite is IPMI Table 22-19 ID 3 or 17 only.
type cipherSuite int

const (
	suite3  cipherSuite = 3
	suite17 cipherSuite = 17
)

const (
	roleAdminNameOnly byte = 0x14 // Table 13-11: Admin + name-only lookup
	kuidLen           int  = 20
)

func padKUID(password string) []byte {
	k := make([]byte, kuidLen)
	copy(k, []byte(password))
	return k
}

func (s cipherSuite) hmacSize() int {
	if s == suite17 {
		return sha256.Size
	}
	return sha1.Size
}

func (s cipherSuite) authCodeSize() int {
	if s == suite17 {
		return 16 // HMAC-SHA256-128
	}
	return 12 // HMAC-SHA1-96
}

func (s cipherSuite) newHMAC(key []byte) hash.Hash {
	if s == suite17 {
		return hmac.New(sha256.New, key)
	}
	return hmac.New(sha1.New, key)
}

func (s cipherSuite) hmacSum(key, data []byte) []byte {
	h := s.newHMAC(key)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func (s cipherSuite) constBlock(v byte) []byte {
	n := s.hmacSize()
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}

func (s cipherSuite) authAlg() byte {
	if s == suite17 {
		return 0x03 // RAKP-HMAC-SHA256
	}
	return 0x01 // RAKP-HMAC-SHA1
}

func (s cipherSuite) integAlg() byte {
	if s == suite17 {
		return 0x04 // HMAC-SHA256-128
	}
	return 0x01 // HMAC-SHA1-96
}

func le32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func kecc2Input(sidc, sidm uint32, rc, rm, guid []byte, role, ulen byte, user []byte) []byte {
	out := make([]byte, 0, 4+4+16+16+16+1+1+len(user))
	out = append(out, le32(sidc)...)
	out = append(out, le32(sidm)...)
	out = append(out, rc...)
	out = append(out, rm...)
	out = append(out, guid...)
	out = append(out, role, ulen)
	out = append(out, user...)
	return out
}

func sikInput(rc, rm []byte, role, ulen byte, user []byte) []byte {
	out := make([]byte, 0, 16+16+1+1+len(user))
	out = append(out, rc...)
	out = append(out, rm...)
	out = append(out, role, ulen)
	out = append(out, user...)
	return out
}

func kecc3Input(rm []byte, sidc uint32, role, ulen byte, user []byte) []byte {
	out := make([]byte, 0, 16+4+1+1+len(user))
	out = append(out, rm...)
	out = append(out, le32(sidc)...)
	out = append(out, role, ulen)
	out = append(out, user...)
	return out
}

func icvInput(rm []byte, sidc uint32, guid []byte) []byte {
	out := make([]byte, 0, 16+4+16)
	out = append(out, rm...)
	out = append(out, le32(sidc)...)
	out = append(out, guid...)
	return out
}

type sessionKeys struct {
	suite cipherSuite
	kuid  []byte
	sik   []byte
	k1    []byte
	k2    []byte
}

func deriveKeys(suite cipherSuite, password string, rc, rm []byte, role, ulen byte, user []byte) sessionKeys {
	kuid := padKUID(password)
	sik := suite.hmacSum(kuid, sikInput(rc, rm, role, ulen, user))
	k1 := suite.hmacSum(sik, suite.constBlock(0x01))
	k2 := suite.hmacSum(sik, suite.constBlock(0x02))
	return sessionKeys{suite: suite, kuid: kuid, sik: sik, k1: k1, k2: k2}
}

func (k sessionKeys) aesKey() []byte { return k.k2[:16] }

func (k sessionKeys) kecc2(sidc, sidm uint32, rc, rm, guid []byte, role, ulen byte, user []byte) []byte {
	return k.suite.hmacSum(k.kuid, kecc2Input(sidc, sidm, rc, rm, guid, role, ulen, user))
}

func (k sessionKeys) kecc3(rm []byte, sidc uint32, role, ulen byte, user []byte) []byte {
	return k.suite.hmacSum(k.kuid, kecc3Input(rm, sidc, role, ulen, user))
}

func (k sessionKeys) icv(rm []byte, sidc uint32, guid []byte) []byte {
	sum := k.suite.hmacSum(k.sik, icvInput(rm, sidc, guid))
	return sum[:k.suite.authCodeSize()]
}

// packRAKP1 is Table 13-11 (SID=0 session). Role 0x14, two reserved bytes, ULen at offset 27.
func packRAKP1(tag byte, sidm uint32, rc []byte, role, ulen byte, user []byte) []byte {
	out := make([]byte, 0, 28+len(user))
	out = append(out, tag, 0, 0, 0)
	out = append(out, le32(sidm)...)
	out = append(out, rc...)
	out = append(out, role, 0, 0, ulen)
	out = append(out, user...)
	return out
}
