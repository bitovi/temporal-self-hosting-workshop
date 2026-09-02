# Codec Server Demo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Temporal codec server (AES-256-GCM payload encryption) plus two demo workflows to the workshop, deployed into the k3d cluster the same way `worker-control-ui` is.

**Architecture:** A single new Go module `codec-demo/` with a shared `codec` package (the AES-256-GCM `PayloadCodec`), two binaries built from one Dockerfile (`codec-server` HTTP service, `worker` Temporal worker), and a `workflows` package with two demo workflows. Wired into `_scripts/start.sh`, k3d's port mappings, `.devcontainer/devcontainer.json`, and `README.md` following the exact patterns already established for `worker-control-ui`.

**Tech Stack:** Go 1.23, `go.temporal.io/sdk`, `go.temporal.io/api`, `google.golang.org/protobuf`, Docker, Kubernetes manifests applied via `kubectl` from `_scripts/start.sh`.

**Full design spec:** `docs/superpowers/specs/2026-07-31-codec-server-design.md`

## Global Constraints

- Go version: 1.23 (matches every other `go.mod`/Dockerfile in this repo).
- Module path: `github.com/bitovi/temporal-self-hosting-workshop/codec-demo` (matches the `worker-control-ui` module naming convention).
- No authentication on the codec server (explicit design decision — see spec).
- Static demo AES-256 key, base64: `cpJr5C9yXJD4iCP5AaejQQffGiS4/R39eKjFnDDTpJc=` — committed directly in the k8s Secret, matching this repo's existing convention of hardcoded demo credentials (e.g. MinIO's `admin`/`temporal`). Not for production use.
- Temporal namespace: `default`. Task queue: `codec-demo`.
- Codec server host port: `8091`, mapped to NodePort `30091`.
- **This session has no access to the k3d cluster.** Do not run `kubectl`, `helm`, or `k3d image import` against it, and do not `curl` any NodePort/cluster-internal address. Everything through Task 8 is verified locally (`go build`, `go test`, `docker build`, `docker run`, syntax/lint checks) without touching the cluster. Task 9 is an explicit exception: it is a checklist for **the user** to run themselves in their Codespace/devcontainer, not something the executing agent runs.

---

### Task 1: Shared AES-256-GCM codec package

**Files:**
- Create: `codec-demo/go.mod`
- Create: `codec-demo/codec/aesgcm.go`
- Test: `codec-demo/codec/aesgcm_test.go`

**Interfaces:**
- Produces: `codec.NewAESCodec(key []byte) (*AESCodec, error)`, `codec.NewAESCodecFromEnv() (*AESCodec, error)`, `(*AESCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error)`, `(*AESCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error)`, `codec.EncodingEncrypted` (string constant) — consumed by Task 2 (codec-server) and Task 4 (worker).

- [ ] **Step 1: Create the Go module**

```bash
mkdir -p codec-demo/codec
cd codec-demo
go mod init github.com/bitovi/temporal-self-hosting-workshop/codec-demo
```

- [ ] **Step 2: Fetch dependencies up front**

```bash
go get go.temporal.io/sdk@latest
go get go.temporal.io/api@latest
go get google.golang.org/protobuf@latest
```

Expected: `go.mod`/`go.sum` now list these as direct requirements. Fetching before writing any code means the next step's test failure is purely "undefined symbol", not a dependency-resolution error.

- [ ] **Step 3: Write the failing test**

Create `codec-demo/codec/aesgcm_test.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it fails**

```bash
cd codec-demo
go test ./codec/... -v
```

Expected: FAIL to compile — `undefined: NewAESCodec` and `undefined: EncodingEncrypted` (neither exists yet).

- [ ] **Step 5: Write the minimal implementation**

Create `codec-demo/codec/aesgcm.go`:

```go
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
```

- [ ] **Step 6: Tidy the module**

```bash
cd codec-demo
go mod tidy
```

- [ ] **Step 7: Run the test to verify it passes**

```bash
cd codec-demo
go test ./codec/... -v
```

Expected: PASS (`TestEncodeDecodeRoundtrip`, `TestDecodePassthroughForUnencryptedPayload`).

- [ ] **Step 8: Commit**

```bash
git add codec-demo/go.mod codec-demo/go.sum codec-demo/codec/
git commit -m "$(cat <<'EOF'
feat: add AES-256-GCM payload codec for codec-demo

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Codec server HTTP endpoints

**Files:**
- Create: `codec-demo/cmd/codec-server/main.go`
- Test: `codec-demo/cmd/codec-server/main_test.go`

**Interfaces:**
- Consumes: `codec.NewAESCodec`, `codec.NewAESCodecFromEnv`, `(*codec.AESCodec).Encode`, `(*codec.AESCodec).Decode` (Task 1).
- Produces: the `codec-server` binary (via `go build ./cmd/codec-server`), listening on `:8091` with `POST /decode`, `POST /encode`, `GET /healthz` — consumed by Task 5 (Dockerfile) and Task 6 (Kubernetes manifests).

- [ ] **Step 1: Write the failing test**

Create `codec-demo/cmd/codec-server/main_test.go`:

```go
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

func TestHealthzReturns200(t *testing.T) {
	key := make([]byte, 32)
	c, err := codec.NewAESCodec(key)
	if err != nil {
		t.Fatalf("NewAESCodec: %v", err)
	}

	srv := httptest.NewServer(newMux(c))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd codec-demo
go test ./cmd/codec-server/... -v
```

Expected: FAIL to compile — `undefined: newMux`.

- [ ] **Step 3: Write the minimal implementation**

Create `codec-demo/cmd/codec-server/main.go`:

```go
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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
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
		w.Write(out)
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
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd codec-demo
go test ./cmd/codec-server/... -v
```

Expected: PASS (`TestDecodeHandlerRoundtrip`, `TestHealthzReturns200`).

- [ ] **Step 5: Commit**

```bash
git add codec-demo/cmd/codec-server/
git commit -m "$(cat <<'EOF'
feat: add codec-demo codec server HTTP endpoints

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Demo workflows and activity

**Files:**
- Create: `codec-demo/workflows/onboarding.go`
- Create: `codec-demo/workflows/payment.go`

**Interfaces:**
- Produces: `workflows.CustomerInfo`, `workflows.OnboardingResult`, `workflows.CustomerOnboardingWorkflow(ctx workflow.Context, info CustomerInfo) (OnboardingResult, error)`; `workflows.CardDetails`, `workflows.PaymentRequest`, `workflows.PaymentResult`, `workflows.ChargeReceipt`, `workflows.ProcessPaymentWorkflow(ctx workflow.Context, req PaymentRequest) (PaymentResult, error)`, `workflows.ChargeCardActivity(ctx context.Context, card CardDetails, amount float64) (ChargeReceipt, error)` — consumed by Task 4 (worker registration) and Task 8 (README CLI examples reference the JSON field names below).

No unit tests for this task (per the design spec's testing section — only the crypto package gets a dedicated test suite; everything else is verified live in Task 9). The deliverable is a successful build.

- [ ] **Step 1: Write `onboarding.go`**

Create `codec-demo/workflows/onboarding.go`:

```go
package workflows

import "go.temporal.io/sdk/workflow"

type CustomerInfo struct {
	Name  string `json:"name"`
	SSN   string `json:"ssn"`
	Email string `json:"email"`
}

type OnboardingResult struct {
	CustomerID string `json:"customerId"`
	Status     string `json:"status"`
}

// CustomerOnboardingWorkflow has no activities -- it demonstrates
// encryption of the top-level workflow input/result payloads only.
func CustomerOnboardingWorkflow(ctx workflow.Context, info CustomerInfo) (OnboardingResult, error) {
	last4 := info.SSN
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	return OnboardingResult{
		CustomerID: "cust-" + last4,
		Status:     "onboarded",
	}, nil
}
```

- [ ] **Step 2: Write `payment.go`**

Create `codec-demo/workflows/payment.go`:

```go
package workflows

import (
	"context"
	"time"

	"go.temporal.io/sdk/workflow"
)

type CardDetails struct {
	CardNumber string `json:"cardNumber"`
	CVV        string `json:"cvv"`
	ExpMonth   int    `json:"expMonth"`
	ExpYear    int    `json:"expYear"`
}

type PaymentRequest struct {
	CustomerID  string      `json:"customerId"`
	CardDetails CardDetails `json:"cardDetails"`
	Amount      float64     `json:"amount"`
}

type PaymentResult struct {
	ReceiptID string `json:"receiptId"`
	Status    string `json:"status"`
}

type ChargeReceipt struct {
	ReceiptID string  `json:"receiptId"`
	Last4     string  `json:"last4"`
	Amount    float64 `json:"amount"`
}

// ProcessPaymentWorkflow calls ChargeCardActivity, whose own input/result
// are a distinct payload surface from the workflow's -- this demonstrates
// the codec applies to activity payloads too, not just top-level workflow
// args.
func ProcessPaymentWorkflow(ctx workflow.Context, req PaymentRequest) (PaymentResult, error) {
	ao := workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var receipt ChargeReceipt
	err := workflow.ExecuteActivity(ctx, ChargeCardActivity, req.CardDetails, req.Amount).Get(ctx, &receipt)
	if err != nil {
		return PaymentResult{}, err
	}

	return PaymentResult{
		ReceiptID: receipt.ReceiptID,
		Status:    "charged",
	}, nil
}

func ChargeCardActivity(ctx context.Context, card CardDetails, amount float64) (ChargeReceipt, error) {
	last4 := card.CardNumber
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	return ChargeReceipt{
		ReceiptID: "receipt-" + last4,
		Last4:     last4,
		Amount:    amount,
	}, nil
}
```

- [ ] **Step 3: Verify it builds**

```bash
cd codec-demo
go build ./...
```

Expected: exits 0 with no output.

- [ ] **Step 4: Commit**

```bash
git add codec-demo/workflows/
git commit -m "$(cat <<'EOF'
feat: add codec-demo onboarding and payment workflows

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Worker binary

**Files:**
- Create: `codec-demo/cmd/worker/main.go`

**Interfaces:**
- Consumes: `codec.NewAESCodecFromEnv` (Task 1); `workflows.CustomerOnboardingWorkflow`, `workflows.ProcessPaymentWorkflow`, `workflows.ChargeCardActivity` (Task 3).
- Produces: the `worker` binary (via `go build ./cmd/worker`), a long-running process polling task queue `codec-demo` in namespace `default` against `cluster-1-temporal-frontend:7233` — consumed by Task 5 (Dockerfile) and Task 6 (Kubernetes manifests).

No unit test for this task (it's a thin wiring `main()`; its behavior is verified live in Task 9). The deliverable is a successful build.

- [ ] **Step 1: Write `main.go`**

Create `codec-demo/cmd/worker/main.go`:

```go
package main

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"

	"github.com/bitovi/temporal-self-hosting-workshop/codec-demo/codec"
	"github.com/bitovi/temporal-self-hosting-workshop/codec-demo/workflows"
)

// TaskQueue matches the queue name learners target with `temporal workflow
// start --task-queue codec-demo` (see README.md).
const TaskQueue = "codec-demo"

func main() {
	aesCodec, err := codec.NewAESCodecFromEnv()
	if err != nil {
		log.Fatalf("initializing codec: %v", err)
	}

	c, err := client.Dial(client.Options{
		// Matches worker-control-ui's frontend addressing convention: the
		// cluster-1 Helm release's frontend Service, same namespace.
		HostPort:  "cluster-1-temporal-frontend:7233",
		Namespace: "default",
		DataConverter: converter.NewCodecDataConverter(
			converter.GetDefaultDataConverter(),
			aesCodec,
		),
	})
	if err != nil {
		log.Fatalf("creating Temporal client: %v", err)
	}
	defer c.Close()

	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(workflows.CustomerOnboardingWorkflow)
	w.RegisterWorkflow(workflows.ProcessPaymentWorkflow)
	w.RegisterActivity(workflows.ChargeCardActivity)

	log.Printf("starting worker on task queue %q", TaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker run failed: %v", err)
	}
}
```

- [ ] **Step 2: Verify it builds**

```bash
cd codec-demo
go build ./...
```

Expected: exits 0 with no output. If it fails with a signature mismatch on `converter.NewCodecDataConverter`, run `go doc go.temporal.io/sdk/converter.NewCodecDataConverter` to check the installed SDK version's exact signature and adjust the call accordingly.

- [ ] **Step 3: Commit**

```bash
git add codec-demo/cmd/worker/
git commit -m "$(cat <<'EOF'
feat: add codec-demo worker binary

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Dockerfile

**Files:**
- Create: `codec-demo/Dockerfile`

**Interfaces:**
- Consumes: `codec-demo/cmd/codec-server`, `codec-demo/cmd/worker` (Tasks 2 and 4).
- Produces: image `codec-demo:local` containing `/usr/local/bin/codec-server` and `/usr/local/bin/worker`, default `CMD` running `codec-server` — consumed by Task 6 (manifests reference this image and override `command` for the worker Deployment) and Task 7 (`start.sh` builds it with this tag).

- [ ] **Step 1: Write the Dockerfile**

Create `codec-demo/Dockerfile`:

```dockerfile
# Build stage
FROM golang:1.23 AS builder

WORKDIR /usr/src/codec-demo

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 go build -v -o /usr/local/bin/codec-server ./cmd/codec-server
RUN CGO_ENABLED=0 go build -v -o /usr/local/bin/worker ./cmd/worker

# Runtime stage
FROM scratch

COPY --from=builder /usr/local/bin/codec-server /usr/local/bin/codec-server
COPY --from=builder /usr/local/bin/worker /usr/local/bin/worker

EXPOSE 8091

# manifests.yaml overrides this command for the worker Deployment.
CMD ["/usr/local/bin/codec-server"]
```

- [ ] **Step 2: Build the image locally**

```bash
docker build -t codec-demo:local ./codec-demo
```

Expected: build succeeds (exit 0).

- [ ] **Step 3: Smoke-test the codec-server binary**

```bash
docker run -d --rm --name codec-demo-smoke -p 18091:8091 \
  -e CODEC_AES_KEY=cpJr5C9yXJD4iCP5AaejQQffGiS4/R39eKjFnDDTpJc= \
  codec-demo:local
sleep 1
curl -sf http://localhost:18091/healthz && echo "OK"
docker stop codec-demo-smoke
```

Expected: prints `OK`. This is a plain local Docker container, not the k3d cluster — no cluster access is involved.

- [ ] **Step 4: Smoke-test the worker binary starts and attempts to connect**

```bash
docker run --rm \
  -e CODEC_AES_KEY=cpJr5C9yXJD4iCP5AaejQQffGiS4/R39eKjFnDDTpJc= \
  --entrypoint /usr/local/bin/worker \
  codec-demo:local
```

Expected: the binary runs and logs a Temporal client dial failure (e.g. `dial tcp: lookup cluster-1-temporal-frontend`) — there is no such host reachable outside the k3d cluster. This confirms the `worker` binary exists, is executable, and reaches the client-dial step; the dial failure itself is expected and fine here (real connectivity is exercised in Task 9, inside the cluster).

- [ ] **Step 5: Commit**

```bash
git add codec-demo/Dockerfile
git commit -m "$(cat <<'EOF'
feat: add codec-demo Dockerfile

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Kubernetes manifests

**Files:**
- Create: `codec-demo/manifests.yaml`

**Interfaces:**
- Consumes: image `codec-demo:local` (Task 5).
- Produces: Secret `codec-demo-key` (env `CODEC_AES_KEY`), Deployment `codec-server` (Service `codec-server`, NodePort `30091` → container port `8091`), Deployment `codec-worker` — consumed by Task 7 (`start.sh`'s `ensure_codec_demo`/`verify_codec_demo` reference these exact names).

- [ ] **Step 1: Write the manifests**

Create `codec-demo/manifests.yaml`:

```yaml
# Deploys the codec-demo codec server + worker into the k3d cluster, the
# same way worker-control-ui is -- see _scripts/start.sh's
# ensure_codec_demo, which builds the image from this directory and
# `k3d image import`s it (it's local repo code, not published to a
# registry) before applying this file.
apiVersion: v1
kind: Secret
metadata:
  name: codec-demo-key
type: Opaque
stringData:
  # Static demo-only AES-256 key, base64-encoded. Shared by the worker
  # (encrypts/decrypts for real workflow execution) and the codec server
  # (decrypts for human viewing via the Web UI/CLI) so their ciphertexts
  # stay compatible. NOT suitable for production use.
  CODEC_AES_KEY: "cpJr5C9yXJD4iCP5AaejQQffGiS4/R39eKjFnDDTpJc="
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: codec-server
  labels:
    app: codec-server
spec:
  replicas: 1
  selector:
    matchLabels:
      app: codec-server
  template:
    metadata:
      labels:
        app: codec-server
    spec:
      containers:
        - name: codec-server
          image: codec-demo:local
          imagePullPolicy: IfNotPresent
          command: ["/usr/local/bin/codec-server"]
          ports:
            - name: http
              containerPort: 8091
          envFrom:
            - secretRef:
                name: codec-demo-key
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 2
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: codec-server
spec:
  type: NodePort
  selector:
    app: codec-server
  ports:
    - port: 8091
      targetPort: 8091
      nodePort: 30091
      name: http
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: codec-worker
  labels:
    app: codec-worker
spec:
  replicas: 1
  selector:
    matchLabels:
      app: codec-worker
  template:
    metadata:
      labels:
        app: codec-worker
    spec:
      containers:
        - name: codec-worker
          image: codec-demo:local
          imagePullPolicy: IfNotPresent
          command: ["/usr/local/bin/worker"]
          envFrom:
            - secretRef:
                name: codec-demo-key
```

- [ ] **Step 2: Validate the YAML statically (no cluster access)**

```bash
python3 -c "
import yaml
with open('codec-demo/manifests.yaml') as f:
    docs = list(yaml.safe_load_all(f))
assert len(docs) == 4, f'expected 4 documents, got {len(docs)}'
kinds = [d['kind'] for d in docs]
assert kinds == ['Secret', 'Deployment', 'Service', 'Deployment'], kinds
print('OK:', kinds)
"
```

Expected: prints `OK: ['Secret', 'Deployment', 'Service', 'Deployment']`. This only parses the YAML locally — it does not contact the k3d cluster.

- [ ] **Step 3: Commit**

```bash
git add codec-demo/manifests.yaml
git commit -m "$(cat <<'EOF'
feat: add codec-demo Kubernetes manifests

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Wire into start.sh, k3d ports, and devcontainer.json

**Files:**
- Modify: `_scripts/start.sh`
- Modify: `.devcontainer/devcontainer.json`

**Interfaces:**
- Consumes: `codec-demo/manifests.yaml` (Task 6), image tag `codec-demo:local` (Task 5).
- Produces: host port `8091` reachable from the Codespace/devcontainer, functions `ensure_codec_demo`/`verify_codec_demo` in `start.sh` — consumed by Task 9 (manual acceptance).

- [ ] **Step 1: Add the codec server's host port to `ensure_k3d_cluster`**

In `_scripts/start.sh`, find the `k3d cluster create dev` port list inside `ensure_k3d_cluster` (currently ending with `-p "8090:30890@server:0" \`) and add the codec server's mapping right after it:

```bash
    k3d cluster create dev \
      -p "8080:30080@server:0" \
      -p "7233:30233@server:0" \
      -p "3000:30000@server:0" \
      -p "8233:31233@server:0" \
      -p "8181:31080@server:0" \
      -p "9090:30090@server:0" \
      -p "8090:30890@server:0" \
      -p "8091:30091@server:0" \
      --wait --timeout 120s
```

- [ ] **Step 2: Add `ensure_codec_demo` next to `ensure_worker_control_ui`**

In `_scripts/start.sh`, immediately after the `ensure_worker_control_ui` function definition, add:

```bash
# codec-demo is this repo's own code, not a published image, so it's built
# and loaded into the k3d cluster directly rather than pulled from a
# registry -- same reasoning as ensure_worker_control_ui.
ensure_codec_demo() {
  echo "Building codec-demo image..."
  docker build -t codec-demo:local ./codec-demo
  echo "Importing codec-demo image into the k3d cluster..."
  k3d image import codec-demo:local -c dev

  echo "Applying codec-demo manifests..."
  kubectl apply -f codec-demo/manifests.yaml

  # The image tag never changes across rebuilds, so `kubectl apply` alone
  # won't pick up new image content unless the pod spec itself changed --
  # force a fresh rollout so a rebuilt image is always picked up, same
  # reasoning as the forced restart in ensure_worker_control_ui.
  kubectl rollout restart deployment/codec-server deployment/codec-worker
  kubectl rollout status deployment/codec-server --timeout=2m
  kubectl rollout status deployment/codec-worker --timeout=2m
}
```

- [ ] **Step 3: Add `verify_codec_demo` next to `verify_worker_control_ui`**

In `_scripts/start.sh`, immediately after the `verify_worker_control_ui` function definition, add:

```bash
verify_codec_demo() {
  echo "Verifying codec-demo is reachable..."
  if curl -sf http://localhost:8091/healthz >/dev/null 2>&1; then
    echo "codec-demo is healthy!"
    return
  fi

  echo "codec-demo not reachable -- retrying..."
  ensure_codec_demo
  if ! curl -sf http://localhost:8091/healthz >/dev/null 2>&1; then
    echo "ERROR: codec-demo is still not reachable after retry." >&2
    exit 1
  fi
  echo "codec-demo healthy on retry!"
}
```

- [ ] **Step 4: Call the new functions from `main`**

In `_scripts/start.sh`'s main section, change:

```bash
ensure_worker_control_ui
```

to:

```bash
ensure_worker_control_ui
ensure_codec_demo
```

And change:

```bash
verify_worker_control_ui

echo ""
echo "All services up and validated successfully!"
```

to:

```bash
verify_worker_control_ui
verify_codec_demo

echo ""
echo "All services up and validated successfully!"
```

- [ ] **Step 5: Update `.devcontainer/devcontainer.json`**

Add `8091` to `forwardPorts` and a matching entry to `portsAttributes`:

```json
  "forwardPorts": [
    8080,
    3000,
    8181,
    9090,
    8090,
    8091
  ],
  "portsAttributes": {
    "8080": {
      "label": "WebUI for cluster-1"
    },
    "3000": {
      "label": "Grafana"
    },
    "8181": {
      "label": "WebUI for cluster-2"
    },
    "9090": {
      "label": "S3 WebUI for MinIO"
    },
    "8090": {
      "label": "Worker Control UI"
    },
    "8091": {
      "label": "Codec Server"
    }
  },
```

- [ ] **Step 6: Validate syntax (no cluster access, no execution)**

```bash
bash -n _scripts/start.sh
python3 -m json.tool .devcontainer/devcontainer.json > /dev/null
```

Expected: both exit 0 with no output — this only checks shell syntax and JSON validity, it does not run the script or contact the cluster.

- [ ] **Step 7: Commit**

```bash
git add _scripts/start.sh .devcontainer/devcontainer.json
git commit -m "$(cat <<'EOF'
feat: wire codec-demo into start.sh and cluster ports

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: README documentation

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: task queue `codec-demo`, workflow types and JSON field names from Task 3, port `8091` from Task 7.
- Produces: none (terminal documentation task).

- [ ] **Step 1: Add a Service Addresses table row**

In `README.md`'s Service Addresses table, add a row after the Worker Control UI row:

```markdown
| Codec Server | `8091` | Codec Server |
```

- [ ] **Step 2: Add a new "Codec Server" section**

Append this section to `README.md`, after the existing "Temporal CLI" section:

```markdown
## Codec Server

This workshop includes a Temporal [codec server](https://docs.temporal.io/production-deployment/data-encryption) that encrypts workflow and activity payloads with AES-256-GCM. Two demo workflows run continuously on the `codec-demo` task queue in the `default` namespace, so their history is genuinely unreadable in the raw Web UI/CLI output until you point a client at the codec server.

**Start the onboarding demo (encrypted workflow input/result):**

```bash
temporal workflow start \
  --namespace default \
  --task-queue codec-demo \
  --type CustomerOnboardingWorkflow \
  --input '{"name":"Jane Doe","ssn":"123-45-6789","email":"jane@example.com"}'
```

**Start the payment demo (encrypted activity input/result):**

```bash
temporal workflow start \
  --namespace default \
  --task-queue codec-demo \
  --type ProcessPaymentWorkflow \
  --input '{"customerId":"cust-6789","cardDetails":{"cardNumber":"4111111111111111","cvv":"123","expMonth":12,"expYear":2030},"amount":49.99}'
```

**See the raw ciphertext (no codec configured):**

```bash
temporal workflow show --workflow-id <workflow-id> --namespace default
```

The `Input`/`Result` fields will show base64 ciphertext, not readable JSON.

**Decode it via the CLI:**

```bash
temporal workflow show --workflow-id <workflow-id> --namespace default \
  --codec-endpoint http://localhost:8091
```

**Decode it in the Web UI:**

1. Open the Web UI (port `8080`) and navigate to the workflow's history — the input/result will show as ciphertext.
2. Open the namespace's **Codec Server** setting (in the Web UI's settings for the `default` namespace).
3. Set the endpoint to `http://localhost:8091` and save.
4. Reload the workflow's history — the payloads now decode live to readable JSON.
```

- [ ] **Step 3: Verify Markdown renders sensibly**

```bash
grep -n "Codec Server" README.md
```

Expected: shows the new table row and section heading.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs: document the codec server demo in README

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Manual acceptance walkthrough (USER-EXECUTED — requires the live Codespace/k3d cluster)

**This task is not run by the executing agent.** The agent implementing this plan has no access to the k3d cluster (see Global Constraints). Present this checklist to the user and ask them to run it themselves in their Codespace/devcontainer, then report back whether each check passed.

- [ ] Recreate or start the `dev` k3d cluster so it picks up the new `8091` port mapping (delete and let `start.sh` recreate if it already existed before this change: `k3d cluster delete dev`, then re-run `_scripts/start.sh`).
- [ ] Confirm `start.sh` completes with `All services up and validated successfully!` (i.e. `verify_codec_demo` passed).
- [ ] Run the `CustomerOnboardingWorkflow` start command from the README; confirm it completes (`temporal workflow describe --workflow-id <id> --namespace default` shows `WorkflowExecutionCompleted`).
- [ ] Run the `ProcessPaymentWorkflow` start command from the README; confirm it completes.
- [ ] Run `temporal workflow show --workflow-id <id> --namespace default` for each and confirm the `Input`/`Result` fields are base64 ciphertext, not readable JSON.
- [ ] Re-run with `--codec-endpoint http://localhost:8091` and confirm the same fields now show readable JSON matching what was submitted (for the payment workflow, confirm both the workflow-level input/result AND the `ChargeCardActivity` input/result decode correctly).
- [ ] In the Web UI (port `8080`), open one of the workflows and confirm the history shows ciphertext by default.
- [ ] Set the namespace's Codec Server endpoint to `http://localhost:8091` in the Web UI settings and confirm the same history now decodes live to readable JSON.
