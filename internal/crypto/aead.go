package crypto

import (
	"encoding/binary"
	"errors"
	"strings"
)

const keyIDSize = 4

var (
	ErrMalformed  = errors.New("crypto: malformed ciphertext")
	ErrUnknownKey = errors.New("crypto: unknown key id")
	ErrDecrypt    = errors.New("crypto: decryption failed")
)

func AAD(parts ...string) []byte {
	return []byte(strings.Join(parts, "\x00"))
}

func KeyID(ciphertext []byte) (uint32, error) {
	if len(ciphertext) < keyIDSize {
		return 0, ErrMalformed
	}
	return binary.BigEndian.Uint32(ciphertext), nil
}

func (k *KeyRing) Seal(plaintext, aad []byte) []byte {
	aead := k.aeads[k.active]
	nonceSize := aead.NonceSize()
	out := make([]byte, keyIDSize+nonceSize, keyIDSize+nonceSize+len(plaintext)+aead.Overhead())
	binary.BigEndian.PutUint32(out, k.active)
	nonce := out[keyIDSize:]
	fill(nonce)
	return aead.Seal(out, nonce, plaintext, aad)
}

func (k *KeyRing) Open(ciphertext, aad []byte) ([]byte, error) {
	id, err := KeyID(ciphertext)
	if err != nil {
		return nil, err
	}
	aead, ok := k.aeads[id]
	if !ok {
		return nil, ErrUnknownKey
	}
	nonceSize := aead.NonceSize()
	if len(ciphertext) < keyIDSize+nonceSize+aead.Overhead() {
		return nil, ErrMalformed
	}
	nonce := ciphertext[keyIDSize : keyIDSize+nonceSize]
	plaintext, err := aead.Open(nil, nonce, ciphertext[keyIDSize+nonceSize:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

func (k *KeyRing) SealString(s string, aad []byte) []byte {
	return k.Seal([]byte(s), aad)
}

func (k *KeyRing) OpenString(ciphertext, aad []byte) (string, error) {
	b, err := k.Open(ciphertext, aad)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (k *KeyRing) NeedsRotation(ciphertext []byte) bool {
	id, err := KeyID(ciphertext)
	return err == nil && id != k.active
}

func (k *KeyRing) Reseal(ciphertext, aad []byte) ([]byte, error) {
	plaintext, err := k.Open(ciphertext, aad)
	if err != nil {
		return nil, err
	}
	return k.Seal(plaintext, aad), nil
}
