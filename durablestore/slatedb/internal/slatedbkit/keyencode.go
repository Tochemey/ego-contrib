// MIT License
//
// Copyright (c) 2024-2026 Arsene Tochemey Gandote
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package slatedbkit

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
)

// KeyID deterministically encodes a persistenceID or projectionID into a
// bytes-safe, reversible identifier suitable for use in SlateDB keys. It uses
// unpadded base64url so the result is URL/object-store friendly.
func KeyID(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

// DecodeKeyID reverses KeyID, returning the original identifier.
func DecodeKeyID(encoded string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// EncodeSeq encodes a sequence number as an 8-byte big-endian value so that
// lexicographic ordering of keys matches numeric ordering. This lets a prefix
// scan return events in sequence order.
func EncodeSeq(seq uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, seq)
	return b
}

// DecodeSeq reverses EncodeSeq.
func DecodeSeq(b []byte) (uint64, bool) {
	if len(b) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(b), true
}

// EncodeOffset encodes an offset value as an 8-byte big-endian value.
func EncodeOffset(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// DecodeOffset reverses EncodeOffset.
func DecodeOffset(b []byte) (uint64, bool) {
	if len(b) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(b), true
}

// HasPrefix reports whether b starts with prefix.
func HasPrefix(b, prefix []byte) bool {
	return bytes.HasPrefix(b, prefix)
}
