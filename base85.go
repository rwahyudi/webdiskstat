package main

import (
	"encoding/binary"
	"fmt"
	"strings"
)

const base85Alphabet = "!#$%'()*+,-./0123456789:;=?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]^_`abcdefghijklmnopqrstuvwxyz{|}~"

var base85Decode = func() [128]int {
	var table [128]int
	for i := range table {
		table[i] = -1
	}
	for index, char := range base85Alphabet {
		table[char] = index
	}
	return table
}()

func base85Encode(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.Grow((len(data) + 3) / 4 * 5)
	var block [4]byte
	for offset := 0; offset < len(data); offset += 4 {
		n := copy(block[:], data[offset:])
		for i := n; i < len(block); i++ {
			block[i] = 0
		}
		value := binary.BigEndian.Uint32(block[:])
		var encoded [5]byte
		for i := len(encoded) - 1; i >= 0; i-- {
			encoded[i] = base85Alphabet[value%85]
			value /= 85
		}
		builder.Write(encoded[:])
	}
	return builder.String()
}

func base85DecodeBytes(text string, length int) ([]byte, error) {
	if len(text)%5 != 0 {
		return nil, fmt.Errorf("base85 payload length must be a multiple of 5")
	}
	if length < 0 || length > len(text)/5*4 {
		return nil, fmt.Errorf("invalid base85 decoded length")
	}
	out := make([]byte, 0, len(text)/5*4)
	for offset := 0; offset < len(text); offset += 5 {
		var value uint32
		for _, char := range text[offset : offset+5] {
			if char >= 128 || base85Decode[char] < 0 {
				return nil, fmt.Errorf("invalid base85 character")
			}
			value = value*85 + uint32(base85Decode[char])
		}
		var block [4]byte
		binary.BigEndian.PutUint32(block[:], value)
		out = append(out, block[:]...)
	}
	return out[:length], nil
}
