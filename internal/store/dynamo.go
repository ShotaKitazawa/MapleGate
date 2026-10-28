package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Records outlive their logical expiry by this much so TTL deletion never races with checks.
const ttlGrace = time.Hour

const workPK = "WORK"

// Dynamo implements both Store and NodeStore; IAM, not this type, keeps the agent out of sessions.
type Dynamo struct {
	Client *dynamodb.Client
	Table  string
}

type item = map[string]types.AttributeValue

func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func n(v int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}
}
func b(v bool) types.AttributeValue { return &types.AttributeValueMemberBOOL{Value: v} }
func t(v time.Time) types.AttributeValue {
	return n(v.Unix())
}

func key(pk, sk string) item     { return item{"PK": s(pk), "SK": s(sk)} }
func sessionKey(id string) item  { return key("SESSION#"+id, "META") }
func activeKey(user string) item { return key("USER#"+user, "ACTIVE") }
func workKey(sid string) item    { return key(workPK, sid) }
func nodeKey() item              { return key("NODE", "vpn") }
func healthKey() item            { return key("HEALTH", "janitor") }
func with(k item, kv ...any) item {
	for i := 0; i+1 < len(kv); i += 2 {
		k[kv[i].(string)] = kv[i+1].(types.AttributeValue)
	}
	return k
}

func str(it item, k string) string {
	if v, ok := it[k].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func tm(it item, k string) time.Time {
	if v, ok := it[k].(*types.AttributeValueMemberN); ok {
		i, _ := strconv.ParseInt(v.Value, 10, 64)
		return time.Unix(i, 0)
	}
	return time.Time{}
}

func flag(it item, k string) bool {
	v, ok := it[k].(*types.AttributeValueMemberBOOL)
	return ok && v.Value
}

// upd builds an UpdateExpression where every attribute name is a placeholder, sidestepping reserved words.
type upd struct {
	names map[string]string
	vals  item
	sets  []string
}

func newUpd() *upd { return &upd{names: map[string]string{}, vals: item{}} }

func (u *upd) set(attr string, v types.AttributeValue) {
	i := strconv.Itoa(len(u.sets))
	u.names["#a"+i] = attr
	u.vals[":v"+i] = v
	u.sets = append(u.sets, "#a"+i+" = :v"+i)
}

func (u *upd) setStr(attr, v string) {
	if v != "" {
		u.set(attr, s(v))
	}
}

func (u *upd) setTime(attr string, v time.Time) {
	if !v.IsZero() {
		u.set(attr, t(v))
	}
}

func (u *upd) expr() *string { return aws.String("SET " + strings.Join(u.sets, ", ")) }

func (d *Dynamo) update(ctx context.Context, k item, u *upd, cond string, names map[string]string, vals item) error {
	for k, v := range names {
		u.names[k] = v
	}
	for k, v := range vals {
		u.vals[k] = v
	}
	in := &dynamodb.UpdateItemInput{
		TableName: &d.Table, Key: k, UpdateExpression: u.expr(),
		ExpressionAttributeNames: u.names, ExpressionAttributeValues: u.vals,
	}
	if cond != "" {
		in.ConditionExpression = aws.String(cond)
	}
	_, err := d.Client.UpdateItem(ctx, in)
	return conflict(err)
}

func (d *Dynamo) CreateSession(ctx context.Context, sess Session, now time.Time) error {
	meta := with(sessionKey(sess.ID),
		"user", s(sess.User), "public_key", s(sess.PublicKey), "status", s(string(sess.Status)),
		"msg_synced", b(false), "created_at", t(sess.CreatedAt), "expires_at", t(sess.ExpiresAt),
		"ttl", t(sess.ExpiresAt.Add(24*time.Hour)))
	work := with(workKey(sess.ID),
		"user", s(sess.User), "phase", s(string(sess.Status)), "public_key", s(sess.PublicKey),
		"expires_at", t(sess.ExpiresAt), "msg_synced", b(false))
	active := with(activeKey(sess.User),
		"session_id", s(sess.ID), "expires_at", t(sess.ExpiresAt), "ttl", t(sess.ExpiresAt.Add(ttlGrace)))

	// The cancellation reasons are mapped back by index, so the order here is part of the contract.
	_, err := d.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: &d.Table, Item: meta, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
			{Put: &types.Put{
				TableName: &d.Table, Item: active,
				ConditionExpression:       aws.String("attribute_not_exists(PK) OR expires_at < :now"),
				ExpressionAttributeValues: item{":now": t(now)},
			}},
			{Put: &types.Put{TableName: &d.Table, Item: work, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
		},
	})
	var tc *types.TransactionCanceledException
	if errors.As(err, &tc) {
		for i, r := range tc.CancellationReasons {
			if aws.ToString(r.Code) != "ConditionalCheckFailed" {
				continue
			}
			if i == 1 {
				return ErrActive
			}
		}
	}
	return err
}

func (d *Dynamo) get(ctx context.Context, k item) (item, error) {
	out, err := d.Client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &d.Table, Key: k, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, err
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	return out.Item, nil
}

func (d *Dynamo) GetSession(ctx context.Context, id string) (Session, error) {
	it, err := d.get(ctx, sessionKey(id))
	if err != nil {
		return Session{}, err
	}
	return Session{
		ID: id, User: str(it, "user"), PublicKey: str(it, "public_key"), Status: Status(str(it, "status")),
		Address: str(it, "address"), DiscordMessageID: str(it, "discord_message_id"), MsgSynced: flag(it, "msg_synced"),
		Approver: str(it, "approver"), CloseReason: str(it, "close_reason"), Error: str(it, "error"),
		CreatedAt: tm(it, "created_at"), ApprovedAt: tm(it, "approved_at"), ExpiresAt: tm(it, "expires_at"),
	}, nil
}

func (d *Dynamo) ActiveSessionID(ctx context.Context, user string) (string, error) {
	it, err := d.get(ctx, activeKey(user))
	if err != nil {
		return "", err
	}
	return str(it, "session_id"), nil
}

func (d *Dynamo) SetMessageID(ctx context.Context, id, messageID string) error {
	for _, k := range []item{sessionKey(id), workKey(id)} {
		u := newUpd()
		u.set("discord_message_id", s(messageID))
		if err := d.update(ctx, k, u, "attribute_exists(PK)", nil, nil); err != nil {
			return notFound(err)
		}
	}
	return nil
}

func (d *Dynamo) SetMsgSynced(ctx context.Context, id string, synced bool) error {
	for _, k := range []item{sessionKey(id), workKey(id)} {
		u := newUpd()
		u.set("msg_synced", b(synced))
		if err := d.update(ctx, k, u, "attribute_exists(PK)", nil, nil); err != nil {
			// WORK may already be gone; the META copy is the one that matters afterwards
			if errors.Is(err, ErrConflict) && k["PK"].(*types.AttributeValueMemberS).Value == workPK {
				continue
			}
			return notFound(err)
		}
	}
	return nil
}

func (d *Dynamo) Transition(ctx context.Context, id string, from []Status, to Status, u Update) error {
	sess, err := d.GetSession(ctx, id)
	if err != nil {
		return notFoundToConflict(err)
	}

	in := make([]string, len(from))
	vals := item{}
	for i, f := range from {
		in[i] = ":f" + strconv.Itoa(i)
		vals[in[i]] = s(string(f))
	}
	cond := "#st IN (" + strings.Join(in, ",") + ")"

	meta := newUpd()
	meta.set("status", s(string(to)))
	meta.setStr("address", u.Address)
	meta.setStr("approver", u.Approver)
	meta.setStr("error", u.Error)
	meta.setStr("close_reason", u.CloseReason)
	meta.setTime("approved_at", u.ApprovedAt)
	if !u.ExpiresAt.IsZero() {
		meta.set("expires_at", t(u.ExpiresAt))
		meta.set("ttl", t(u.ExpiresAt.Add(24*time.Hour)))
	}
	meta.names["#st"] = "status"
	for k, v := range vals {
		meta.vals[k] = v
	}

	work := newUpd()
	work.set("phase", s(string(to)))
	work.setStr("address", u.Address)
	work.setStr("close_reason", u.CloseReason)
	work.setTime("approved_at", u.ApprovedAt)
	work.setTime("expires_at", u.ExpiresAt)

	items := []types.TransactWriteItem{
		{Update: &types.Update{
			TableName: &d.Table, Key: sessionKey(id), UpdateExpression: meta.expr(), ConditionExpression: &cond,
			ExpressionAttributeNames: meta.names, ExpressionAttributeValues: meta.vals,
		}},
		{Update: &types.Update{
			TableName: &d.Table, Key: workKey(id), UpdateExpression: work.expr(),
			ConditionExpression:      aws.String("attribute_exists(PK)"),
			ExpressionAttributeNames: work.names, ExpressionAttributeValues: work.vals,
		}},
	}
	if !u.ExpiresAt.IsZero() {
		items = append(items, types.TransactWriteItem{Update: &types.Update{
			TableName: &d.Table, Key: activeKey(sess.User),
			UpdateExpression:         aws.String("SET session_id = :sid, expires_at = :exp, #ttl = :attl"),
			ConditionExpression:      aws.String("attribute_not_exists(PK) OR session_id = :sid"),
			ExpressionAttributeNames: map[string]string{"#ttl": "ttl"},
			ExpressionAttributeValues: item{
				":sid": s(id), ":exp": t(u.ExpiresAt), ":attl": t(u.ExpiresAt.Add(ttlGrace)),
			},
		}})
	}
	if _, err := d.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items}); err != nil {
		return conflict(err)
	}

	// Outside the transaction: a conditional delete inside it would cancel the whole transition when the slot is already gone.
	// The janitor repeats it for terminal sessions, so a failure here only delays it.
	if to.Terminal() {
		_ = d.ClearActive(ctx, sess.User, id)
	}
	return nil
}

func (d *Dynamo) ClearActive(ctx context.Context, user, sid string) error {
	_, err := d.Client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.Table, Key: activeKey(user),
		ConditionExpression:       aws.String("session_id = :sid"),
		ExpressionAttributeValues: item{":sid": s(sid)},
	})
	if errors.Is(conflict(err), ErrConflict) {
		return nil
	}
	return err
}

func (d *Dynamo) LeaseIP(ctx context.Context, addr, sid string, until time.Time) error {
	it := with(key("IP#"+addr, "LEASE"), "session_id", s(sid), "ttl", t(until.Add(ttlGrace)))
	_, err := d.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &d.Table, Item: it,
		ConditionExpression:       aws.String("attribute_not_exists(PK) OR #ttl < :now"),
		ExpressionAttributeNames:  map[string]string{"#ttl": "ttl"},
		ExpressionAttributeValues: item{":now": t(time.Now())},
	})
	return conflict(err)
}

// RenewLease is separate from LeaseIP on purpose: the approval path releases an address it fails to use, so an acquisition
// that also succeeded for the current holder would let a duplicate approval free the address of the session that won.
func (d *Dynamo) RenewLease(ctx context.Context, addr, sid string, until time.Time) error {
	_, err := d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.Table, Key: key("IP#"+addr, "LEASE"),
		UpdateExpression:          aws.String("SET #ttl = :ttl"),
		ConditionExpression:       aws.String("session_id = :sid"),
		ExpressionAttributeNames:  map[string]string{"#ttl": "ttl"},
		ExpressionAttributeValues: item{":sid": s(sid), ":ttl": t(until.Add(ttlGrace))},
	})
	return conflict(err)
}

func (d *Dynamo) ReleaseIP(ctx context.Context, addr, sid string) error {
	_, err := d.Client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.Table, Key: key("IP#"+addr, "LEASE"),
		ConditionExpression:       aws.String("session_id = :sid"),
		ExpressionAttributeValues: item{":sid": s(sid)},
	})
	if errors.Is(conflict(err), ErrConflict) {
		return nil
	}
	return err
}

func toWork(it item) Work {
	return Work{
		SID: str(it, "SK"), User: str(it, "user"), Phase: Status(str(it, "phase")),
		PublicKey: str(it, "public_key"), Address: str(it, "address"), ExpiresAt: tm(it, "expires_at"),
		ApprovedAt: tm(it, "approved_at"), DiscordMessageID: str(it, "discord_message_id"),
		MsgSynced: flag(it, "msg_synced"), CloseReason: str(it, "close_reason"),
		AppliedAt: tm(it, "applied_at"), AppliedBootID: str(it, "applied_boot_id"),
		LastHandshakeAt: tm(it, "last_handshake_at"), RemovedAt: tm(it, "removed_at"), NodeError: str(it, "node_error"),
	}
}

func (d *Dynamo) ListWork(ctx context.Context) ([]Work, error) {
	var out []Work
	var start item
	for {
		res, err := d.Client.Query(ctx, &dynamodb.QueryInput{
			TableName: &d.Table, ConsistentRead: aws.Bool(true), ExclusiveStartKey: start,
			KeyConditionExpression:    aws.String("PK = :p"),
			ExpressionAttributeValues: item{":p": s(workPK)},
		})
		if err != nil {
			return nil, err
		}
		for _, it := range res.Items {
			out = append(out, toWork(it))
		}
		if res.LastEvaluatedKey == nil {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

func (d *Dynamo) GetWork(ctx context.Context, sid string) (Work, error) {
	it, err := d.get(ctx, workKey(sid))
	if err != nil {
		return Work{}, err
	}
	return toWork(it), nil
}

func (d *Dynamo) MarkRemoved(ctx context.Context, sid string, at time.Time) error {
	return d.ReportRemoved(ctx, sid, at)
}

func (d *Dynamo) DeleteWork(ctx context.Context, sid string) error {
	_, err := d.Client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &d.Table, Key: workKey(sid),
		ConditionExpression: aws.String("attribute_not_exists(PK) OR attribute_exists(removed_at)"),
	})
	return conflict(err)
}

func (d *Dynamo) GetNode(ctx context.Context) (Node, error) {
	it, err := d.get(ctx, nodeKey())
	if err != nil {
		return Node{}, err
	}
	return Node{
		InstanceID: str(it, "instance_id"), BootID: str(it, "boot_id"), PublicIP: str(it, "public_ip"),
		LastSeen: tm(it, "last_seen"), State: str(it, "state"), Mode: str(it, "mode"),
	}, nil
}

func (d *Dynamo) PutNode(ctx context.Context, nd Node, now time.Time, takeoverAfter time.Duration) error {
	it := with(nodeKey(),
		"instance_id", s(nd.InstanceID), "boot_id", s(nd.BootID), "public_ip", s(nd.PublicIP),
		"last_seen", t(nd.LastSeen), "state", s(nd.State), "mode", s(nd.Mode))
	_, err := d.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &d.Table, Item: it,
		ConditionExpression:       aws.String("attribute_not_exists(PK) OR instance_id = :me OR last_seen < :cutoff"),
		ExpressionAttributeValues: item{":me": s(nd.InstanceID), ":cutoff": t(now.Add(-takeoverAfter))},
	})
	return conflict(err)
}

func (d *Dynamo) GetHealth(ctx context.Context) (Health, error) {
	it, err := d.get(ctx, healthKey())
	if errors.Is(err, ErrNotFound) {
		return Health{}, nil
	}
	if err != nil {
		return Health{}, err
	}
	sf, _ := strconv.Atoi(numStr(it, "start_failures"))
	return Health{
		LastRun: tm(it, "last_run"), LastStartAt: tm(it, "last_start_at"), StartFailures: sf, CooldownUntil: tm(it, "cooldown_until"),
		RunningSince: tm(it, "running_since"), IdleSince: tm(it, "idle_since"),
		UnresponsiveAlertedAt: tm(it, "unresponsive_alerted_at"), OverstayAlertedAt: tm(it, "overstay_alerted_at"),
	}, nil
}

func (d *Dynamo) PutHealth(ctx context.Context, h Health) error {
	it := with(healthKey(), "last_run", t(h.LastRun), "last_start_at", t(h.LastStartAt), "start_failures", n(int64(h.StartFailures)), "cooldown_until", t(h.CooldownUntil),
		"running_since", t(h.RunningSince), "idle_since", t(h.IdleSince),
		"unresponsive_alerted_at", t(h.UnresponsiveAlertedAt), "overstay_alerted_at", t(h.OverstayAlertedAt))
	_, err := d.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: &d.Table, Item: it})
	return err
}

func numStr(it item, k string) string {
	if v, ok := it[k].(*types.AttributeValueMemberN); ok {
		return v.Value
	}
	return "0"
}

func (d *Dynamo) workUpdate(ctx context.Context, sid string, fill func(*upd)) error {
	u := newUpd()
	fill(u)
	return notFound(d.update(ctx, workKey(sid), u, "attribute_exists(PK)", nil, nil))
}

func (d *Dynamo) ReportApplied(ctx context.Context, sid, bootID string, at time.Time) error {
	u := newUpd()
	u.set("applied_at", t(at))
	u.set("applied_boot_id", s(bootID))
	// A re-applied peer must not look removed.
	err := d.update(ctx, workKey(sid), u, "attribute_exists(PK)", nil, nil)
	if err != nil {
		return notFound(err)
	}
	_, err = d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &d.Table, Key: workKey(sid), UpdateExpression: aws.String("REMOVE removed_at"),
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	return notFound(conflict(err))
}

func (d *Dynamo) ReportRemoved(ctx context.Context, sid string, at time.Time) error {
	return d.workUpdate(ctx, sid, func(u *upd) { u.set("removed_at", t(at)) })
}

func (d *Dynamo) ReportHandshake(ctx context.Context, sid string, at time.Time) error {
	return d.workUpdate(ctx, sid, func(u *upd) { u.set("last_handshake_at", t(at)) })
}

func (d *Dynamo) ReportError(ctx context.Context, sid, msg string) error {
	return d.workUpdate(ctx, sid, func(u *upd) { u.set("node_error", s(msg)) })
}

func conflict(err error) error {
	var cc *types.ConditionalCheckFailedException
	var tc *types.TransactionCanceledException
	if errors.As(err, &cc) || errors.As(err, &tc) {
		return ErrConflict
	}
	return err
}

func notFound(err error) error {
	if errors.Is(err, ErrConflict) {
		return ErrNotFound
	}
	return err
}

func notFoundToConflict(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	return err
}
