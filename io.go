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

	reader := bufio.NewReader(input)
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

	decoder := json.NewDecoder(decodeReader)
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errNoInput
		}
		return nil, err
	}
	return raw, nil
}
