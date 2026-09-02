package main

import (
	"io"
	"log"
	"net/http"

	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/bitovi/temporal-self-hosting-workshop/codec-demo/codec"
)

// newMux builds the codec server's HTTP routes. Split out from main so
// tests can exercise it directly via httptest without starting a real
// network listener.
func newMux(c *codec.AESCodec) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/decode", withCORS(payloadsHandler(c.Decode)))
	mux.HandleFunc("/encode", withCORS(payloadsHandler(c.Encode)))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// withCORS allows the browser-based Temporal Web UI (a different origin)
// to call this server directly. No auth is required (an explicit choice
// for this workshop), so the origin is unrestricted.
func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Namespace")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// payloadsHandler adapts a codec.AESCodec transform function (Encode or
// Decode) to Temporal's codec-server HTTP contract: POST a JSON-encoded
// commonpb.Payloads, get one back.
func payloadsHandler(transform func([]*commonpb.Payload) ([]*commonpb.Payload, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var payloads commonpb.Payloads
		if err := protojson.Unmarshal(body, &payloads); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		result, err := transform(payloads.GetPayloads())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		out, err := protojson.Marshal(&commonpb.Payloads{Payloads: result})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(out); err != nil {
			log.Printf("writing response: %v", err)
		}
	}
}

func main() {
	c, err := codec.NewAESCodecFromEnv()
	if err != nil {
		log.Fatalf("initializing codec: %v", err)
	}

	addr := ":8091"
	log.Printf("codec server listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, newMux(c)))
}
