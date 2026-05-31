package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"
)

func TestPBKDF2Vector(t *testing.T) {
	got := pbkdf2Key(testSHA1, []byte("password"), []byte("salt"), 1, 20)
	want := "0c60c80f961f0e71f3a9b524af6012062fe037a6"
	if hex.EncodeToString(got) != want {
		t.Fatalf("PBKDF2 mismatch: got %x want %s", got, want)
	}
}

func TestUnencryptedPayloadIsCompressedReportData(t *testing.T) {
	root := &Node{Name: "root", Path: "root", Type: "dir", Children: []*Node{
		{Name: "file.txt", Path: "root/file.txt", Size: 42, Type: "file", Ext: ".txt"},
	}}
	addTotals(root)
	payloadJSON, err := reportDataPayload(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["encrypted"] != false {
		t.Fatalf("expected unencrypted payload: %#v", payload)
	}
	if payload["encoding"] != "base85" || payload["format"] != "gds-binary-v1" {
		t.Fatalf("unexpected payload encoding: %#v", payload)
	}
	compressed, err := base85DecodeBytes(payload["payload"].(string), int(payload["length"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("file.txt")) {
		t.Fatalf("decompressed payload does not contain file name")
	}
}

func TestCompressedReportDataIsBinaryPayload(t *testing.T) {
	root := &Node{Name: "root", Path: "root", Type: "dir", Children: []*Node{
		{Name: "dir", Path: "root/dir", Size: 5, Type: "dir", Children: []*Node{
			{Name: "a.txt", Path: "root/dir/a.txt", Size: 5, Type: "file", Ext: ".txt", MTime: "1"},
		}},
		{Name: "b.bin", Path: "/absolute/b.bin", Size: 9, Type: "file", Ext: ".bin", MIME: "application/octet-stream"},
	}}
	addTotals(root)
	compressed, err := compressedReportData(root)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:4], []byte{'G', 'D', 'S', 1}) {
		t.Fatalf("unexpected binary magic: %x", got[:4])
	}
	offset := 4
	stringCount, n := binary.Uvarint(got[offset:])
	if n <= 0 {
		t.Fatalf("invalid string table length")
	}
	offset += n
	var table []string
	for i := uint64(0); i < stringCount; i++ {
		size, n := binary.Uvarint(got[offset:])
		if n <= 0 {
			t.Fatalf("invalid string length at %d", i)
		}
		offset += n
		table = append(table, string(got[offset:offset+int(size)]))
		offset += int(size)
	}
	for _, want := range []string{"root", "dir", "a.txt", ".txt", "b.bin", "/absolute/b.bin", "application/octet-stream"} {
		if !stringTableContains(table, want) {
			t.Fatalf("string table missing %q: %#v", want, table)
		}
	}
}

func stringTableContains(table []string, want string) bool {
	for _, value := range table {
		if value == want {
			return true
		}
	}
	return false
}

func TestEncryptedPayloadShape(t *testing.T) {
	root := &Node{Name: "root", Path: "root", Type: "dir", Children: []*Node{}}
	addTotals(root)
	password := "secret"
	payloadJSON, err := reportDataPayload(root, &password)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["encrypted"] != true || payload["algorithm"] != encryptionAlgorithm || payload["kdf"] != "PBKDF2-SHA256" {
		t.Fatalf("unexpected encrypted payload: %#v", payload)
	}
	if payload["encoding"] != "base85" || payload["format"] != "gds-binary-v1" {
		t.Fatalf("unexpected encrypted payload encoding: %#v", payload)
	}
	for _, key := range []string{"salt", "nonce", "tag"} {
		if _, err := base64.StdEncoding.DecodeString(payload[key].(string)); err != nil {
			t.Fatalf("%s is not base64: %v", key, err)
		}
	}
	if _, err := base85DecodeBytes(payload["payload"].(string), int(payload["length"].(float64))); err != nil {
		t.Fatalf("encrypted payload is not base85: %v", err)
	}
}

func TestBase85RoundTrip(t *testing.T) {
	input := []byte{0, 1, 2, 3, 4, 5, 250, 255, 42}
	encoded := base85Encode(input)
	got, err := base85DecodeBytes(encoded, len(input))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, input) {
		t.Fatalf("base85 roundtrip = %x, want %x", got, input)
	}
}
