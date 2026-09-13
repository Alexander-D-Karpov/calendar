package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

const (
	KeySize = 32
	MACSize = keyIDSize + sha256.Size
)

type KeyRing struct {
	aeads  map[uint32]cipher.AEAD
	raw    map[uint32][]byte
	active uint32
}

func NewKeyRing(keys map[uint32][]byte, active uint32) (*KeyRing, error) {
	if len(keys) == 0 {
		return nil, errors.New("crypto: no keys")
	}
	kr := &KeyRing{
		aeads: make(map[uint32]cipher.AEAD, len(keys)),
		raw:   make(map[uint32][]byte, len(keys)),
	}
	var highest uint32
	for id, key := range keys {
		if id == 0 {
			return nil, errors.New("crypto: key id 0 is reserved")
		}
		if len(key) != KeySize {
			return nil, fmt.Errorf("crypto: key %d must be %d bytes", id, KeySize)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("crypto: key %d: %w", id, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("crypto: key %d: %w", id, err)
		}
		kr.aeads[id] = aead
		kr.raw[id] = slices.Clone(key)
		highest = max(highest, id)
	}
	if active == 0 {
		active = highest
	}
	if _, ok := kr.aeads[active]; !ok {
		return nil, fmt.Errorf("crypto: active key %d not found", active)
	}
	kr.active = active
	return kr, nil
}

func (k *KeyRing) Active() uint32 {
	return k.active
}

func (k *KeyRing) IDs() []uint32 {
	ids := make([]uint32, 0, len(k.aeads))
	for id := range k.aeads {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (k *KeyRing) Derive(purpose string) []byte {
	key, err := k.DeriveFor(k.active, purpose)
	if err != nil {
		panic("crypto: active key missing: " + err.Error())
	}
	return key
}

func (k *KeyRing) DeriveFor(id uint32, purpose string) ([]byte, error) {
	raw, ok := k.raw[id]
	if !ok {
		return nil, ErrUnknownKey
	}
	key, err := hkdf.Key(sha256.New, raw, nil, "calendar:"+purpose, KeySize)
	if err != nil {
		panic("crypto: hkdf failed: " + err.Error())
	}
	return key, nil
}

func (k *KeyRing) Sign(purpose string, msg []byte) []byte {
	out := make([]byte, keyIDSize, MACSize)
	binary.BigEndian.PutUint32(out, k.active)
	return append(out, HMACSHA256(k.Derive("mac:"+purpose), msg)...)
}

func (k *KeyRing) Verify(purpose string, msg, sig []byte) bool {
	if len(sig) != MACSize {
		return false
	}
	key, err := k.DeriveFor(binary.BigEndian.Uint32(sig), "mac:"+purpose)
	if err != nil {
		return false
	}
	return Equal(sig[keyIDSize:], HMACSHA256(key, msg))
}
