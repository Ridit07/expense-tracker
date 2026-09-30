package services

import "testing"

func TestClassifyRejectsNonTransactions(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		reason RejectReason
	}{
		{
			name:   "otp",
			body:   "123456 is your OTP for login. Do not share it with anyone.",
			reason: ReasonOTP,
		},
		{
			name:   "otp quoting an amount",
			body:   "OTP 884512 for a transaction of Rs. 5000.00 on your HDFC card. Never share this code.",
			reason: ReasonOTP,
		},
		{
			name:   "promotional loan offer",
			body:   "You are pre-approved for a personal loan of Rs. 5,00,000 at lowest interest. Apply now!",
			reason: ReasonPromotional,
		},
		{
			name:   "delivery update",
			body:   "Your order OD12345 is out for delivery and will arrive today.",
			reason: ReasonDeliveryUpdate,
		},
		{
			name:   "delivery update quoting order value",
			body:   "Your package worth Rs. 1,299 has been shipped. Tracking ID: BD9921.",
			reason: ReasonDeliveryUpdate,
		},
		{
			name:   "balance enquiry",
			body:   "HDFC Bank: Avl Bal in A/c XX1234 is Rs. 24,510.22 as on 30-SEP-26.",
			reason: ReasonBalanceEnquiry,
		},
	}

	d := NewTransactionDetector()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.Classify("AD-HDFCBK", tc.body)
			if got.IsTransaction {
				t.Fatalf("classified as a transaction, want rejected (%s)", tc.reason)
			}
			if got.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.reason)
			}
		})
	}
}

func TestClassifyKeepsTransactions(t *testing.T) {
	bodies := []string{
		"HDFC Bank: Rs. 1,250.00 debited from A/c XX1234 towards ZOMATO on 19-09-26.",
		"ICICI Bank Acct XX123 debited for Rs 500.00 on 01-Oct-26; UBER credited.",
		"INR 2,300.00 spent on your Axis Bank Credit Card at AMAZON.",
		"Rs.15000 credited to your A/c XX9911 by NEFT from ACME PAYROLL.",
	}

	d := NewTransactionDetector()
	for _, body := range bodies {
		if got := d.Classify("AD-HDFCBK", body); !got.IsTransaction {
			t.Fatalf("rejected (%s) a real transaction: %s", got.Reason, body)
		}
	}
}

// Most real debit SMS carry a balance line and many carry marketing copy. The
// movement + amount signal has to outrank both or we'd reject actual spending.
func TestClassifyKeepsTransactionWithBalanceAndPromoNoise(t *testing.T) {
	d := NewTransactionDetector()

	withBalance := "HDFC Bank: Rs. 450.00 debited from A/c XX1234 towards SWIGGY. Avl Bal: Rs. 12,300.00"
	if got := d.Classify("AD-HDFCBK", withBalance); !got.IsTransaction {
		t.Fatalf("balance suffix caused a reject (%s)", got.Reason)
	}

	withPromo := "Rs. 999 debited for your subscription. Get 10% off on your next renewal, T&C apply."
	if got := d.Classify("AD-HDFCBK", withPromo); !got.IsTransaction {
		t.Fatalf("marketing suffix caused a reject (%s)", got.Reason)
	}
}

// The detector only rejects what it recognises; anything else is the parser's
// problem, because a false reject hides real spending.
func TestClassifyDefaultsToKeepingUnknownMessages(t *testing.T) {
	d := NewTransactionDetector()

	if got := d.Classify("UNKNOWN", "Some message we have no rule for at all."); !got.IsTransaction {
		t.Fatalf("unknown message rejected as %s, want kept", got.Reason)
	}
}
