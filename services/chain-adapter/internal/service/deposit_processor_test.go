package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMintServiceClientConfirmDepositUsesInternalEndpointAndAuth 验证
// MintServiceClient.ConfirmDeposit 会调用正确的内部端点
// (/api/v1/internal/deposit-confirm)、携带 X-Internal-API-Key 鉴权头，
// 并按约定序列化请求体（deposit_intent_id / deposit_tx_id / amount_minor）。
func TestMintServiceClientConfirmDepositUsesInternalEndpointAndAuth(t *testing.T) {
	var gotPath string
	var gotKey string
	var gotReq ConfirmDepositRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Internal-API-Key")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewMintServiceClient(server.URL, "internal-secret")
	err := client.ConfirmDeposit(context.Background(), ConfirmDepositRequest{
		DepositIntentID: "di_test",
		DepositTxID:     "tx_test",
		AmountMinor:     100,
	})
	if err != nil {
		t.Fatalf("ConfirmDeposit returned error: %v", err)
	}

	if gotPath != "/api/v1/internal/deposit-confirm" {
		t.Fatalf("path = %q, want internal deposit-confirm path", gotPath)
	}
	if gotKey != "internal-secret" {
		t.Fatalf("internal api key = %q, want internal-secret", gotKey)
	}
	if gotReq.DepositIntentID != "di_test" || gotReq.DepositTxID != "tx_test" || gotReq.AmountMinor != 100 {
		t.Fatalf("unexpected request body: %+v", gotReq)
	}
}
