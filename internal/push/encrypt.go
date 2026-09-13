package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

const recordSize = 4096

func encrypt(plaintext, uaPublic, authSecret []byte) ([]byte, error) {
	if len(uaPublic) != 65 || uaPublic[0] != 4 {
		return nil, errors.New("push: subscription key is not a P-256 point")
	}
	if len(authSecret) < 16 {
		return nil, errors.New("push: auth secret is too short")
	}
	remote, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, err
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(remote)
	if err != nil {
		return nil, err
	}
	asPublic := eph.PublicKey().Bytes()
	salt := crypto.RandomBytes(16)

	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(uaPublic)+string(asPublic), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	record := append(append([]byte{}, plaintext...), 0x02)
	if len(record)+gcm.Overhead() > recordSize {
		return nil, errors.New("push: payload is too large")
	}
	ciphertext := gcm.Seal(nil, nonce, record, nil)

	out := make([]byte, 0, 16+4+1+len(asPublic)+len(ciphertext))
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asPublic)))
	out = append(out, asPublic...)
	return append(out, ciphertext...), nil
}
