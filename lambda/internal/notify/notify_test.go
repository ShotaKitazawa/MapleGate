package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func record(subject, message string) events.SNSEventRecord {
	return events.SNSEventRecord{SNS: events.SNSEntity{Subject: subject, Message: message}}
}

type poster struct {
	posted []string
	err    error
}

func (p *poster) post(_ context.Context, text string) error {
	if p.err != nil {
		return p.err
	}
	p.posted = append(p.posted, text)
	return nil
}

func TestABudgetNoticeIsPostedAsItIs(t *testing.T) {
	p := &poster{}
	h := Handler{Post: p.post}

	require.NoError(t, h.Handle(context.Background(), events.SNSEvent{Records: []events.SNSEventRecord{
		record("AWS Budgets: gamevpn-monthly has exceeded your alert threshold", "Your actual cost is $8.10."),
	}}))

	require.Len(t, p.posted, 1)
	assert.Contains(t, p.posted[0], "AWS Budgets: gamevpn-monthly has exceeded your alert threshold")
	assert.Contains(t, p.posted[0], "Your actual cost is $8.10.")
}

func TestAnAlarmIsPostedWithItsNameStateAndReason(t *testing.T) {
	p := &poster{}
	h := Handler{Post: p.post}
	msg := `{"AlarmName":"gamevpn-janitor-silent","NewStateValue":"ALARM","NewStateReason":"no invocations for 5 minutes"}`

	require.NoError(t, h.Handle(context.Background(), events.SNSEvent{Records: []events.SNSEventRecord{record("", msg)}}))

	require.Len(t, p.posted, 1)
	assert.Contains(t, p.posted[0], "gamevpn-janitor-silent")
	assert.Contains(t, p.posted[0], "ALARM")
	assert.Contains(t, p.posted[0], "no invocations for 5 minutes")
}

func TestARecoveryIsSaidToBeOne(t *testing.T) {
	p := &poster{}
	h := Handler{Post: p.post}
	msg := `{"AlarmName":"gamevpn-janitor-silent","NewStateValue":"OK","NewStateReason":"back"}`

	require.NoError(t, h.Handle(context.Background(), events.SNSEvent{Records: []events.SNSEventRecord{record("", msg)}}))

	assert.Contains(t, p.posted[0], "recovered")
}

func TestATextOverDiscordsLimitIsCut(t *testing.T) {
	p := &poster{}
	h := Handler{Post: p.post}

	require.NoError(t, h.Handle(context.Background(), events.SNSEvent{Records: []events.SNSEventRecord{
		record("big", strings.Repeat("x", 5000)),
	}}))

	assert.LessOrEqual(t, len(p.posted[0]), 2000)
}

func TestAFailedPostFailsTheInvocationSoSNSRetries(t *testing.T) {
	p := &poster{err: errors.New("discord down")}
	h := Handler{Post: p.post}

	err := h.Handle(context.Background(), events.SNSEvent{Records: []events.SNSEventRecord{record("s", "m")}})

	assert.Error(t, err)
}
