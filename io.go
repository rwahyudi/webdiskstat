package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var errNoInput = errors.New("stdin is empty")

const (
	maxInputBytes        int64 = 512 << 20
	maxDecompressedBytes int64 = 1 << 30
)

var errInputTooLarge = errors.New("input exceeds the supported size limit")

type sizeLimitedReader struct {
	reader    io.Reader
	remaining int64
}

func newSizeLimitedReader(reader io.Reader, limit int64) *sizeLimitedReader {
	return &sizeLimitedReader{reader: reader, remaining: limit}
}

func (reader *sizeLimitedReader) Read(data []byte) (int, error) {
	if reader.remaining <= 0 {
		var probe [1]byte
		n, err := reader.reader.Read(probe[:])
		if n > 0 {
			return 0, errInputTooLarge
		}
		return 0, err
	}
	if int64(len(data)) > reader.remaining {
		data = data[:reader.remaining]
	}
	n, err := reader.reader.Read(data)
	reader.remaining -= int64(n)
	return n, err
}

func readJSON(source string, stdin io.Reader) (any, error) {
	var input io.Reader
	var closeInput func() error
	if source == "-" {
		input = stdin
		closeInput = func() error { return nil }
	} else {
		file, err := os.Open(source)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%q does not exist", source)
			}
			return nil, err
		}
		input = file
		closeInput = file.Close
	}
	defer closeInput()

	// Limit both the stored export and its expanded form so a compressed export
	// cannot consume unbounded memory while being decoded.
	reader := bufio.NewReader(newSizeLimitedReader(input, maxInputBytes))
	gzipped := filepath.Ext(source) == ".gz"
	if header, err := reader.Peek(2); err == nil && header[0] == 0x1f && header[1] == 0x8b {
		gzipped = true
	}

	var decodeReader io.Reader = reader
	if gzipped {
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		decodeReader = gzipReader
	}

	decoder := json.NewDecoder(newSizeLimitedReader(decodeReader, maxDecompressedBytes))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errNoInput
		}
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("input contains multiple JSON values")
		}
		return nil, err
	}
	return raw, nil
}
