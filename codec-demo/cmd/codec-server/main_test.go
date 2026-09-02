package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/bitovi/temporal-self-hosting-workshop/codec-demo/codec"
)

func mustReadAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return b
}

func TestDecodeHandlerRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	c, err := codec.NewAESCodec(key)
	if err != nil {
		t.Fatalf("NewAESCodec: %v", err)
	}

	original, err := converter.GetDefaultDataConverter().ToPayload("hello world")
	if err != nil {
		t.Fatalf("ToPayload: %v", err)
	}
	encrypted, err := c.Encode([]*commonpb.Payload{original})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	reqBody, err := protojson.Marshal(&commonpb.Payloads{Payloads: encrypted})
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}

	srv := httptest.NewServer(newMux(c))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/decode", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /decode: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var decoded commonpb.Payloads
	if err := protojson.Unmarshal(mustReadAll(t, resp.Body), &decoded); err != nil {
		t.Fatalf("unmarshaling response body: %v", err)
	}
	if len(decoded.GetPayloads()) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(decoded.GetPayloads()))
	}
	if string(decoded.GetPayloads()[0].GetData()) != string(original.GetData()) {
		t.Fatalf("decoded data = %q, want %q", decoded.GetPayloads()[0].GetData(), original.GetData())
	}
}

func TestHealthReturns200(t *testing.T) {
	key := make([]byte, 32)
	c, err := codec.NewAESCodec(key)
	if err != nil {
		t.Fatalf("NewAESCodec: %v", err)
	}

	srv := httptest.NewServer(newMux(c))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
