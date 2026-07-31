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
