package codec

import (
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	key := make([]byte, 32) // deterministic all-zero key, fine for a test
	c, err := NewAESCodec(key)
	if err != nil {
		t.Fatalf("NewAESCodec: %v", err)
	}

	original, err := converter.GetDefaultDataConverter().ToPayload("hello world")
	if err != nil {
		t.Fatalf("ToPayload: %v", err)
	}

	encoded, err := c.Encode([]*commonpb.Payload{original})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(encoded) != 1 {
		t.Fatalf("expected 1 encoded payload, got %d", len(encoded))
	}
	if string(encoded[0].GetData()) == string(original.GetData()) {
		t.Fatalf("expected ciphertext to differ from plaintext")
	}
	if string(encoded[0].GetMetadata()["encoding"]) != EncodingEncrypted {
		t.Fatalf("expected encoding metadata %q, got %q", EncodingEncrypted, encoded[0].GetMetadata()["encoding"])
	}

	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 decoded payload, got %d", len(decoded))
	}
	if string(decoded[0].GetData()) != string(original.GetData()) {
		t.Fatalf("decoded data = %q, want %q", decoded[0].GetData(), original.GetData())
	}
	if string(decoded[0].GetMetadata()["encoding"]) != string(original.GetMetadata()["encoding"]) {
		t.Fatalf("decoded encoding = %q, want %q", decoded[0].GetMetadata()["encoding"], original.GetMetadata()["encoding"])
	}
}

func TestDecodePassthroughForUnencryptedPayload(t *testing.T) {
	key := make([]byte, 32)
	c, err := NewAESCodec(key)
	if err != nil {
		t.Fatalf("NewAESCodec: %v", err)
	}

	plain, err := converter.GetDefaultDataConverter().ToPayload("not encrypted")
	if err != nil {
		t.Fatalf("ToPayload: %v", err)
	}

	decoded, err := c.Decode([]*commonpb.Payload{plain})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(decoded[0].GetData()) != string(plain.GetData()) {
		t.Fatalf("expected passthrough of unencrypted payload")
	}
}

func TestNewAESCodecRejectsWrongKeyLength(t *testing.T) {
	tests := []struct {
		name      string
		keyLength int
	}{
		{"16-byte key (AES-128)", 16},
		{"24-byte key (AES-192)", 24},
		{"31-byte key", 31},
		{"33-byte key", 33},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLength)
			_, err := NewAESCodec(key)
			if err == nil {
				t.Fatalf("expected error for %d-byte key, got nil", tt.keyLength)
			}
		})
	}
}
