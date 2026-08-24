package ipmi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"fmt"
)

func encryptPayload(key, plain []byte) ([]byte, error) {
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("ipmi: iv: %w", err)
	}
	padded := confidentialityPad(plain)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("ipmi: aes: %w", err)
	}
	if len(padded)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ipmi: confidentiality pad not block-aligned")
	}
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	out := make([]byte, 0, len(iv)+len(ct))
	out = append(out, iv...)
	out = append(out, ct...)
	return out, nil
}

func decryptPayload(key, ivct []byte) ([]byte, error) {
	if len(ivct) < aes.BlockSize*2 || len(ivct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ipmi: short ciphertext")
	}
	iv, ct := ivct[:aes.BlockSize], ivct[aes.BlockSize:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("ipmi: aes: %w", err)
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	return stripConfidentialityPad(plain)
}

// Table 13-20: pad bytes 0x01…0xN, pad length, Next Header 0x07, so
// (len(payload)+1+1+N) % 16 == 0.
func confidentialityPad(payload []byte) []byte {
	n := (aes.BlockSize - ((len(payload) + 2) % aes.BlockSize)) % aes.BlockSize
	out := make([]byte, 0, len(payload)+n+2)
	out = append(out, payload...)
	for i := 1; i <= n; i++ {
		out = append(out, byte(i))
	}
	out = append(out, byte(n), nextHeaderIPMI)
	return out
}

func stripConfidentialityPad(plain []byte) ([]byte, error) {
	if len(plain) < 2 {
		return nil, fmt.Errorf("ipmi: confidentiality trailer truncated")
	}
	if plain[len(plain)-1] != nextHeaderIPMI {
		return nil, fmt.Errorf("ipmi: confidentiality next-header")
	}
	padLen := int(plain[len(plain)-2])
	if padLen < 0 || padLen > aes.BlockSize || len(plain) < padLen+2 {
		return nil, fmt.Errorf("ipmi: confidentiality pad length")
	}
	return plain[:len(plain)-2-padLen], nil
}

func macEqual(a, b []byte) bool { return hmac.Equal(a, b) }
