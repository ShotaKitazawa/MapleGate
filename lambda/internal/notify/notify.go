// Package notify turns what SNS delivers (Budgets notices and CloudWatch alarms) into a Discord post in the approval channel.
package notify

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-lambda-go/events"
)

// limit is Discord's cap on a message.
const limit = 2000

type Handler struct {
	Post func(ctx context.Context, text string) error
}

type alarm struct {
	AlarmName      string
	NewStateValue  string
	NewStateReason string
}

// Handle posts every record; the first failure fails the invocation so SNS delivers it again.
func (h Handler) Handle(ctx context.Context, e events.SNSEvent) error {
	for _, r := range e.Records {
		if err := h.Post(ctx, cut(text(r.SNS))); err != nil {
			return err
		}
	}
	return nil
}

func text(n events.SNSEntity) string {
	var a alarm
	if json.Unmarshal([]byte(n.Message), &a) == nil && a.AlarmName != "" {
		if a.NewStateValue == "OK" {
			return fmt.Sprintf("Alarm **%s** recovered: %s", a.AlarmName, a.NewStateReason)
		}
		return fmt.Sprintf("Alarm **%s** is %s: %s", a.AlarmName, a.NewStateValue, a.NewStateReason)
	}
	if n.Subject == "" {
		return n.Message
	}
	return fmt.Sprintf("**%s**\n%s", n.Subject, n.Message)
}

func cut(s string) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit-3] + "..."
}
