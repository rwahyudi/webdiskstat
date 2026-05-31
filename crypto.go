package main

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"math/bits"
)

func chacha20Poly1305Encrypt(key, nonce, plaintext, aad []byte) ([]byte, []byte, error) {
	if len(key) != 32 {
		return nil, nil, fmt.Errorf("encryption key must be 32 bytes")
	}
	if len(nonce) != 12 {
		return nil, nil, fmt.Errorf("encryption nonce must be 12 bytes")
	}
	polyKey := chacha20Block(key, nonce, 0)[:32]
	ciphertext, err := chacha20XOR(key, nonce, 1, plaintext)
	if err != nil {
		return nil, nil, err
	}
	tag := poly1305MACAEAD(aad, ciphertext, polyKey)
	return ciphertext, tag, nil
}

func chacha20XOR(key, nonce []byte, counter uint32, data []byte) ([]byte, error) {
	output := make([]byte, len(data))
	for offset := 0; offset < len(data); offset += 64 {
		block := chacha20Block(key, nonce, counter)
		chunkEnd := offset + 64
		if chunkEnd > len(data) {
			chunkEnd = len(data)
		}
		for i, value := range data[offset:chunkEnd] {
			output[offset+i] = value ^ block[i]
		}
		counter++
		if counter == 0 && offset+64 < len(data) {
			return nil, fmt.Errorf("report data is too large to encrypt with one nonce")
		}
	}
	return output, nil
}

func chacha20Block(key, nonce []byte, counter uint32) []byte {
	state := [16]uint32{
		0x61707865,
		0x3320646e,
		0x79622d32,
		0x6b206574,
	}
	for i := 0; i < 8; i++ {
		state[4+i] = binary.LittleEndian.Uint32(key[i*4 : i*4+4])
	}
	state[12] = counter
	state[13] = binary.LittleEndian.Uint32(nonce[0:4])
	state[14] = binary.LittleEndian.Uint32(nonce[4:8])
	state[15] = binary.LittleEndian.Uint32(nonce[8:12])

	working := state
	for i := 0; i < 10; i++ {
		quarterRound(&working, 0, 4, 8, 12)
		quarterRound(&working, 1, 5, 9, 13)
		quarterRound(&working, 2, 6, 10, 14)
		quarterRound(&working, 3, 7, 11, 15)
		quarterRound(&working, 0, 5, 10, 15)
		quarterRound(&working, 1, 6, 11, 12)
		quarterRound(&working, 2, 7, 8, 13)
		quarterRound(&working, 3, 4, 9, 14)
	}

	out := make([]byte, 64)
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], working[i]+state[i])
	}
	return out
}

func quarterRound(state *[16]uint32, a, b, c, d int) {
	state[a] += state[b]
	state[d] = bits.RotateLeft32(state[d]^state[a], 16)
	state[c] += state[d]
	state[b] = bits.RotateLeft32(state[b]^state[c], 12)
	state[a] += state[b]
	state[d] = bits.RotateLeft32(state[d]^state[a], 8)
	state[c] += state[d]
	state[b] = bits.RotateLeft32(state[b]^state[c], 7)
}

func poly1305MACAEAD(aad, ciphertext, key []byte) []byte {
	r := append([]byte(nil), key[:16]...)
	r[3] &= 15
	r[7] &= 15
	r[11] &= 15
	r[15] &= 15
	r[4] &= 252
	r[8] &= 252
	r[12] &= 252

	rValue := littleInt(r)
	sValue := littleInt(key[16:])
	modulus := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))
	accumulator := big.NewInt(0)
	number := new(big.Int)

	update := func(block []byte) {
		littleIntPaddedAEADBlock(number, block)
		accumulator.Add(accumulator, number)
		accumulator.Mul(accumulator, rValue)
		accumulator.Mod(accumulator, modulus)
	}

	for offset := 0; offset < len(aad); offset += 16 {
		update(aad[offset:min(offset+16, len(aad))])
	}
	for offset := 0; offset < len(ciphertext); offset += 16 {
		update(ciphertext[offset:min(offset+16, len(ciphertext))])
	}
	var lengths [16]byte
	binary.LittleEndian.PutUint64(lengths[0:8], uint64(len(aad)))
	binary.LittleEndian.PutUint64(lengths[8:16], uint64(len(ciphertext)))
	update(lengths[:])

	tagValue := new(big.Int).Add(accumulator, sValue)
	tagValue.Mod(tagValue, new(big.Int).Lsh(big.NewInt(1), 128))
	return intToLittleBytes(tagValue, 16)
}

func littleIntPaddedAEADBlock(dst *big.Int, data []byte) {
	var block [17]byte
	copy(block[:], data)
	block[16] = 1
	for i := 0; i < len(block)/2; i++ {
		block[i], block[len(block)-1-i] = block[len(block)-1-i], block[i]
	}
	dst.SetBytes(block[:])
}

func littleInt(data []byte) *big.Int {
	reversed := make([]byte, len(data))
	for i := range data {
		reversed[len(data)-1-i] = data[i]
	}
	return new(big.Int).SetBytes(reversed)
}

func intToLittleBytes(value *big.Int, length int) []byte {
	bigEndian := value.Bytes()
	out := make([]byte, length)
	for i := 0; i < len(bigEndian) && i < length; i++ {
		out[i] = bigEndian[len(bigEndian)-1-i]
	}
	return out
}
