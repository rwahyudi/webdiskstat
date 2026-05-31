package main

import (
	"crypto/sha1"
	"encoding/hex"
	"hash"
	"testing"
)

func testSHA1() hash.Hash {
	return sha1.New()
}

func TestChaCha20Poly1305RFCVector(t *testing.T) {
	key, _ := hex.DecodeString("808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f")
	nonce, _ := hex.DecodeString("070000004041424344454647")
	aad, _ := hex.DecodeString("50515253c0c1c2c3c4c5c6c7")
	plaintext, _ := hex.DecodeString(
		"4c616469657320616e642047656e746c656d656e206f662074686520636c617373206f66202739393a204966204920636f756c64206f6666657220796f75206f6e6c79206f6e652074697020666f7220746865206675747572652c2073756e73637265656e20776f756c642062652069742e",
	)
	wantCiphertext, _ := hex.DecodeString(
		"d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4def08e4b7a9de576d26586cec64b6116",
	)
	wantTag, _ := hex.DecodeString("1ae10b594f09e26a7e902ecbd0600691")

	ciphertext, tag, err := chacha20Poly1305Encrypt(key, nonce, plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(ciphertext) != hex.EncodeToString(wantCiphertext) {
		t.Fatalf("ciphertext mismatch:\ngot  %x\nwant %x", ciphertext, wantCiphertext)
	}
	if hex.EncodeToString(tag) != hex.EncodeToString(wantTag) {
		t.Fatalf("tag mismatch: got %x want %x", tag, wantTag)
	}
}
