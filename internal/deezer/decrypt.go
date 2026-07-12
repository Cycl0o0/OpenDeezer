// Package deezer: Blowfish BF_CBC_STRIPE decryption for Deezer CDN streams.
package deezer

import (
	"crypto/md5"
	"encoding/hex"

	"golang.org/x/crypto/blowfish"
)

const chunkSize = 2048

// secret is the fixed key-derivation secret.
var secret = []byte("g4el58wc0zvf9na1")

// stripeIV is the per-chunk CBC IV (reset each chunk).
var stripeIV = []byte{0, 1, 2, 3, 4, 5, 6, 7}

// BlowfishKey derives the per-track Blowfish key:
// key[i] = md5hex[i] ^ md5hex[i+16] ^ secret[i].
func BlowfishKey(trackID string) []byte {
	sum := md5.Sum([]byte(trackID))
	h := []byte(hex.EncodeToString(sum[:])) // 32 lowercase hex chars
	key := make([]byte, 16)
	for i := 0; i < 16; i++ {
		key[i] = h[i] ^ h[i+16] ^ secret[i]
	}
	return key
}

// StripeDecryptor streams BF_CBC_STRIPE plaintext out of ciphertext fed in
// arbitrary-sized pieces. Mirrors C++ DeezerStripeDecryptor.
type StripeDecryptor struct {
	cipher     *blowfish.Cipher
	buf        []byte
	chunkIndex int
}

// NewStripeDecryptor builds a decryptor keyed for trackID.
func NewStripeDecryptor(trackID string) (*StripeDecryptor, error) {
	c, err := blowfish.NewCipher(BlowfishKey(trackID))
	if err != nil {
		return nil, err
	}
	return &StripeDecryptor{cipher: c, buf: make([]byte, 0, chunkSize)}, nil
}

// decryptChunk does CBC over a full 2048-byte chunk with the fixed IV.
func (d *StripeDecryptor) decryptChunk(in []byte) []byte {
	out := make([]byte, chunkSize)
	prev := make([]byte, 8)
	copy(prev, stripeIV)
	for off := 0; off+8 <= chunkSize; off += 8 {
		ct := in[off : off+8]
		pt := out[off : off+8]
		d.cipher.Decrypt(pt, ct)
		for k := 0; k < 8; k++ {
			pt[k] ^= prev[k]
		}
		copy(prev, ct)
	}
	return out
}

// Feed appends decrypted/passthrough output for n input bytes.
func (d *StripeDecryptor) Feed(data []byte, out []byte) []byte {
	i := 0
	for i < len(data) {
		need := chunkSize - len(d.buf)
		take := need
		if rem := len(data) - i; rem < need {
			take = rem
		}
		d.buf = append(d.buf, data[i:i+take]...)
		i += take
		if len(d.buf) == chunkSize {
			if d.chunkIndex%3 == 0 {
				out = append(out, d.decryptChunk(d.buf)...)
			} else {
				out = append(out, d.buf...)
			}
			d.chunkIndex++
			d.buf = d.buf[:0]
		}
	}
	return out
}

// Consumed reports how many ciphertext bytes have been fed so far (full chunks
// already emitted plus the partial chunk still buffered). The stripe cipher is
// byte-for-byte 1:1, so this is exactly the offset to resume a dropped CDN
// download from via an HTTP Range request: re-issuing the GET with
// `Range: bytes=<Consumed()>-` and continuing to Feed the same decryptor yields
// output identical to an uninterrupted download.
func (d *StripeDecryptor) Consumed() int64 {
	return int64(d.chunkIndex)*chunkSize + int64(len(d.buf))
}

// Finish flushes the trailing partial chunk (always plaintext).
func (d *StripeDecryptor) Finish(out []byte) []byte {
	if len(d.buf) > 0 {
		out = append(out, d.buf...)
		d.buf = d.buf[:0]
	}
	return out
}

// DecryptTrack decrypts a whole in-memory buffer (used by tests).
func DecryptTrack(trackID string, data []byte) ([]byte, error) {
	d, err := NewStripeDecryptor(trackID)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data))
	out = d.Feed(data, out)
	out = d.Finish(out)
	return out, nil
}
