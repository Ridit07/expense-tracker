package services

import (
	"regexp"
	"strings"
)

// TransactionDetector is the first processing stage after ingestion. Its only
// job is to throw away the messages that plainly aren't transactions — OTPs,
// marketing, delivery updates, balance enquiries — so the parser downstream
// only ever sees candidates. It is deliberately conservative: a message it
// isn't sure about stays RECEIVED, because a false REJECT hides real spending
// while a false RECEIVED only costs the parser a wasted pass.
//
// Nothing here mutates the message: rejection is a status change on a row whose
// Body is kept verbatim, so a later, better detector can re-run over the
// REJECTED rows and move them back. That reversibility is the whole reason
// raw_messages exists as its own table.
type TransactionDetector struct{}

func NewTransactionDetector() *TransactionDetector {
	return &TransactionDetector{}
}

// RejectReason names the rule that fired. It is "" when the message looks like
// a transaction. Values are stable strings so they can be logged or, later,
// persisted for auditing detector changes.
type RejectReason string

const (
	ReasonOTP            RejectReason = "otp"
	ReasonPromotional    RejectReason = "promotional"
	ReasonDeliveryUpdate RejectReason = "delivery_update"
	ReasonBalanceEnquiry RejectReason = "balance_enquiry"
)

// Detection is the detector's verdict on one message.
type Detection struct {
	IsTransaction bool
	Reason        RejectReason // set only when IsTransaction is false
}

var (
	// A money amount: "Rs. 1,250.00", "INR 500", "₹49". Bare numbers don't
	// count — far too many OTPs and order ids would look like amounts.
	amountRe = regexp.MustCompile(`(?i)\b(?:rs\.?|inr|₹)\s?[\d,]+(?:\.\d{1,2})?\b`)

	// A verb that describes money actually moving. "Avl bal" and "limit" are
	// deliberately absent: they describe a state, not an event.
	movementRe = regexp.MustCompile(`(?i)\b(debited|credited|withdrawn|spent|paid|payment of|purchase of|txn of|transaction of|transferred|transfer of|sent to|received from|charged)\b`)

	// OTP wins over everything else. "OTP for a txn of Rs 5000" contains a
	// perfectly good amount and movement verb, but the OTP is never the record
	// of the transaction — the bank sends a separate message for that. Treating
	// it as a candidate would double-count every online payment.
	otpRe = regexp.MustCompile(`(?i)(\botp\b|one[- ]time password|verification code|\bdo not share\b|\bnever share\b|security code)`)

	// Marketing. Any of these on a message with no movement verb is enough.
	promoRe = regexp.MustCompile(`(?i)(pre-?approved|loan offer|apply now|click here|limited period|\bt&c\b|terms and conditions apply|unsubscribe|lowest interest|\d+% off\b|\bflat \d+%|\bsale\b|\bdiscount\b|\bcashback offer\b|\bwin\b|\bupgrade your\b|\brefer and earn\b)`)

	// Courier / order status. These often quote an order value, which is why
	// the movement verb is what separates them from a real payment message.
	deliveryRe = regexp.MustCompile(`(?i)(out for delivery|has been (shipped|dispatched|delivered)|your (order|package|parcel|shipment)|tracking (id|number)|arriving (today|tomorrow)|delivery (partner|agent|executive)|otp for delivery)`)

	// Balance / statement notifications. A real debit SMS usually also carries
	// "Avl Bal", so this only fires when nothing moved.
	balanceRe = regexp.MustCompile(`(?i)(avl\.? ?bal|available balance|a/c balance|account balance|closing balance|\bbalance (is|as on|enquiry)|statement is ready|mini statement)`)
)

// Classify decides whether a raw message is worth parsing. sender is accepted
// because some rules will eventually want it (a known bank short-code is weak
// evidence for a transaction); today only the body is inspected.
func (d *TransactionDetector) Classify(sender, body string) Detection {
	text := strings.TrimSpace(body)

	// Checked before the movement test on purpose — see otpRe.
	if otpRe.MatchString(text) {
		return Detection{Reason: ReasonOTP}
	}

	// The single strongest signal: a money verb next to a money amount. If both
	// are present the message stays a candidate even when it also carries
	// marketing copy or a balance line, which most bank SMS do.
	if movementRe.MatchString(text) && amountRe.MatchString(text) {
		return Detection{IsTransaction: true}
	}

	switch {
	case deliveryRe.MatchString(text):
		return Detection{Reason: ReasonDeliveryUpdate}
	case balanceRe.MatchString(text):
		return Detection{Reason: ReasonBalanceEnquiry}
	case promoRe.MatchString(text):
		return Detection{Reason: ReasonPromotional}
	}

	// No rule was confident enough. Leave it for the parser rather than guess.
	return Detection{IsTransaction: true}
}
