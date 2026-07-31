// Package codec implements a Temporal PayloadCodec that encrypts/decrypts
// Payloads with AES-256-GCM. It's shared by the worker (which uses it as
// part of its DataConverter for real workflow execution) and the codec
// server (which uses it standalone to decrypt payloads for human viewing
// via the Web UI/CLI) so both sides stay wire-compatible.
package codec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

// EncodingEncrypted marks a Payload's "encoding" metadata as having been
// produced by AESCodec.Encode. Decode uses this to distinguish payloads it
// should decrypt from ones (e.g. never encrypted, or from a different
// codec) it should pass through unchanged -- attempting to AES-GCM-open a
// payload that was never encrypted with this key would otherwise fail the
// whole batch.
const EncodingEncrypted = "binary/encrypted"

const metadataEncodingKey = "encoding"

// AESCodec implements go.temporal.io/sdk/converter.PayloadCodec.
type AESCodec struct {
	gcm cipher.AEAD
}

// Compile-time assertion that AESCodec satisfies converter.PayloadCodec, so
// a future signature drift fails here (the package that owns the contract)
// instead of only surfacing as a build error in the consuming worker binary.
var _ converter.PayloadCodec = (*AESCodec)(nil)

// NewAESCodecFromEnv builds an AESCodec from the CODEC_AES_KEY environment
// variable, a base64-encoded 32-byte AES-256 key.
func NewAESCodecFromEnv() (*AESCodec, error) {
	keyB64 := os.Getenv("CODEC_AES_KEY")
	if keyB64 == "" {
		return nil, fmt.Errorf("CODEC_AES_KEY environment variable is required")
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("decoding CODEC_AES_KEY: %w", err)
	}
	return NewAESCodec(key)
}

// NewAESCodec builds an AESCodec from a raw 32-byte AES-256 key.
func NewAESCodec(key []byte) (*AESCodec, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes for AES-256, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	return &AESCodec{gcm: gcm}, nil
}

// Encode encrypts each payload's serialized proto bytes with AES-256-GCM,
// prepending a random nonce to the ciphertext.
func (c *AESCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		data, err := proto.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshaling payload %d: %w", i, err)
		}
		nonce := make([]byte, c.gcm.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return nil, fmt.Errorf("generating nonce: %w", err)
		}
		ciphertext := c.gcm.Seal(nonce, nonce, data, nil)
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{
				metadataEncodingKey: []byte(EncodingEncrypted),
			},
			Data: ciphertext,
		}
	}
	return result, nil
}

// Decode reverses Encode. Payloads not marked with EncodingEncrypted are
// passed through unchanged (see EncodingEncrypted's doc comment for why).
func (c *AESCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.GetMetadata()[metadataEncodingKey]) != EncodingEncrypted {
			result[i] = p
			continue
		}
		nonceSize := c.gcm.NonceSize()
		if len(p.GetData()) < nonceSize {
			return nil, fmt.Errorf("payload %d: ciphertext shorter than nonce", i)
		}
		nonce, ciphertext := p.GetData()[:nonceSize], p.GetData()[nonceSize:]
		plaintext, err := c.gcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return nil, fmt.Errorf("decrypting payload %d: %w", i, err)
		}
		decoded := &commonpb.Payload{}
		if err := proto.Unmarshal(plaintext, decoded); err != nil {
			return nil, fmt.Errorf("unmarshaling decrypted payload %d: %w", i, err)
		}
		result[i] = decoded
	}
	return result, nil
}
