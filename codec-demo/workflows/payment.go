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
