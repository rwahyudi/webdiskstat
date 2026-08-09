package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash"
	"io"
	"time"
)

const (
	encryptionAAD       = "webdiskstat-report-data-v1"
	encryptionAlgorithm = "ChaCha20-Poly1305"
	pbkdf2Iterations    = 310000
)

type reportDataStrings struct {
	table   []string
	indexes map[string]int
}

func newReportDataStrings() *reportDataStrings {
	return &reportDataStrings{indexes: map[string]int{}}
}

func (strings *reportDataStrings) intern(value string) int {
	if value == "" {
		return -1
	}
	if index, ok := strings.indexes[value]; ok {
		return index
	}
	index := len(strings.table)
	strings.table = append(strings.table, value)
	strings.indexes[value] = index
	return index
}

func (strings *reportDataStrings) collect(root *Node) {
	type entry struct {
		node       *Node
		parentPath string
	}
	stack := []entry{{node: root}}
	for len(stack) > 0 {
		index := len(stack) - 1
		current := stack[index]
		stack = stack[:index]
		expectedPath := makePath(current.parentPath, current.node.Name)
		strings.intern(current.node.Name)
		if current.node.Path != expectedPath {
			strings.intern(current.node.Path)
		}
		strings.intern(current.node.Ext)
		strings.intern(current.node.MTime)
		strings.intern(current.node.MIME)
		strings.intern(current.node.Flag)
		for childIndex := len(current.node.Children) - 1; childIndex >= 0; childIndex-- {
			stack = append(stack, entry{node: current.node.Children[childIndex], parentPath: current.node.Path})
		}
	}
}

func scriptJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(bytes.ReplaceAll(data, []byte("</"), []byte("<\\/"))), nil
}

func reportDataPayload(root *Node, password *string) (string, error) {
	compressed, err := compressedReportData(root)
	if err != nil {
		return "", err
	}
	if password == nil {
		return scriptJSON(map[string]any{
			"encrypted": false,
			"encoding":  "base85",
			"format":    "gds-binary-v1",
			"length":    len(compressed),
			"payload":   base85Encode(compressed),
		})
	}
	payload, err := encryptReportData(compressed, *password)
	if err != nil {
		return "", err
	}
	return scriptJSON(payload)
}

func compressedReportData(root *Node) ([]byte, error) {
	var buf bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	writer.Header.ModTime = time.Unix(0, 0)
	buffered := bufio.NewWriterSize(writer, 64*1024)
	if err := writeBinaryReportData(buffered, root); err != nil {
		writer.Close()
		return nil, err
	}
	if err := buffered.Flush(); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeBinaryReportData(writer *bufio.Writer, root *Node) error {
	strings := newReportDataStrings()
	strings.collect(root)
	if _, err := writer.Write([]byte{'G', 'D', 'S', 1}); err != nil {
		return err
	}
	if err := writeUvarint(writer, uint64(len(strings.table))); err != nil {
		return err
	}
	for _, value := range strings.table {
		data := []byte(value)
		if err := writeUvarint(writer, uint64(len(data))); err != nil {
			return err
		}
		if _, err := writer.Write(data); err != nil {
			return err
		}
	}
	type entry struct {
		node       *Node
		parentPath string
	}
	stack := []entry{{node: root}}
	for len(stack) > 0 {
		index := len(stack) - 1
		current := stack[index]
		stack = stack[:index]
		if err := writeBinaryNode(writer, strings, current.node, current.parentPath); err != nil {
			return err
		}
		for childIndex := len(current.node.Children) - 1; childIndex >= 0; childIndex-- {
			stack = append(stack, entry{node: current.node.Children[childIndex], parentPath: current.node.Path})
		}
	}
	return nil
}

func writeBinaryNode(writer *bufio.Writer, strings *reportDataStrings, node *Node, parentPath string) error {
	expectedPath := makePath(parentPath, node.Name)
	pathIndex := -1
	if node.Path != expectedPath {
		pathIndex = strings.intern(node.Path)
	}
	nodeType := 0
	if node.Type == "dir" {
		nodeType = 1
	}
	fields := []uint64{
		encodeStringIndex(strings.intern(node.Name)),
		encodeStringIndex(pathIndex),
		uint64(node.Size),
		uint64(nodeType),
		encodeStringIndex(strings.intern(node.Ext)),
		encodeStringIndex(strings.intern(node.MTime)),
		encodeStringIndex(strings.intern(node.MIME)),
		encodeStringIndex(strings.intern(node.Flag)),
		uint64(len(node.Children)),
	}
	for _, value := range fields {
		if err := writeUvarint(writer, value); err != nil {
			return err
		}
	}
	return nil
}

func encodeStringIndex(index int) uint64 {
	return uint64(index + 1)
}

func writeUvarint(writer *bufio.Writer, value uint64) error {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], value)
	_, err := writer.Write(buf[:n])
	return err
}

func encryptReportData(plaintext []byte, password string) (map[string]any, error) {
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	key := pbkdf2Key(sha256.New, []byte(password), salt, pbkdf2Iterations, 32)
	ciphertext, tag, err := chacha20Poly1305Encrypt(key, nonce, plaintext, []byte(encryptionAAD))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"encrypted":  true,
		"algorithm":  encryptionAlgorithm,
		"encoding":   "base85",
		"format":     "gds-binary-v1",
		"kdf":        "PBKDF2-SHA256",
		"iterations": pbkdf2Iterations,
		"salt":       base64.StdEncoding.EncodeToString(salt),
		"nonce":      base64.StdEncoding.EncodeToString(nonce),
		"aad":        encryptionAAD,
		"length":     len(ciphertext),
		"payload":    base85Encode(ciphertext),
		"tag":        base64.StdEncoding.EncodeToString(tag),
	}, nil
}

func pbkdf2Key(hashFactory func() hash.Hash, password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(hashFactory, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	derived := make([]byte, 0, numBlocks*hashLen)
	var blockIndex [4]byte
	for block := 1; block <= numBlocks; block++ {
		blockIndex[0] = byte(block >> 24)
		blockIndex[1] = byte(block >> 16)
		blockIndex[2] = byte(block >> 8)
		blockIndex[3] = byte(block)
		prf.Reset()
		prf.Write(salt)
		prf.Write(blockIndex[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		derived = append(derived, t...)
	}
	return derived[:keyLen]
}
