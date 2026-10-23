package siteicon

import (
	"encoding/binary"
	"errors"
)

// icoSignature starts every ICO file: reserved 0, type 1 (icon).
var icoSignature = []byte{0, 0, 1, 0}

var errBadICO = errors.New("siteicon: malformed ICO")

// icoSize returns the size of the largest image in an ICO file's directory.
// A width or height byte of 0 means 256. It reads only the directory, not
// the images.
func icoSize(data []byte) (width, height int, err error) {
	const headerLen, entryLen = 6, 16
	if len(data) < headerLen || string(data[:4]) != string(icoSignature) {
		return 0, 0, errBadICO
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || len(data) < headerLen+count*entryLen {
		return 0, 0, errBadICO
	}
	for i := range count {
		entry := data[headerLen+i*entryLen:]
		w, h := int(entry[0]), int(entry[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		if w*h > width*height {
			width, height = w, h
		}
	}
	return width, height, nil
}
