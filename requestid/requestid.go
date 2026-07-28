// Package requestid generates RFC 4122 version 4 UUIDs for request
// correlation. It has no dependencies outside the standard library.
package requestid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// New returns a random UUIDv4 string, e.g. "9f8b7c6d-1a2b-4c3d-8e9f-0a1b2c3d4e5f".
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the OS entropy source is broken;
		// panicking is preferable to silently correlating requests wrong.
		panic(fmt.Sprintf("requestid: crypto/rand unavailable: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant

	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}
