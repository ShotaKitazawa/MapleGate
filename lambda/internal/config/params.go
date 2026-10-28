package config

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const paramsTTL = 5 * time.Minute

type entry struct {
	value   string
	fetched time.Time
}

// Params caches SSM parameters for a few minutes so a rotated secret reaches warm containers,
// and keeps serving the stale value if Parameter Store is unavailable.
type Params struct {
	Client *ssm.Client
	Prefix string
	Now    func() time.Time

	mu    sync.Mutex
	cache map[string]entry
}

func (p *Params) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Params) Get(ctx context.Context, name string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.cache[name]
	if ok && p.now().Sub(e.fetched) < paramsTTL {
		return e.value, nil
	}
	out, err := p.Client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(p.Prefix + "/" + name), WithDecryption: aws.Bool(true)})
	if err != nil {
		if ok {
			slog.Warn("parameter store unavailable, using stale value", "name", name, "err", err)
			return e.value, nil
		}
		return "", err
	}
	if p.cache == nil {
		p.cache = map[string]entry{}
	}
	p.cache[name] = entry{value: aws.ToString(out.Parameter.Value), fetched: p.now()}
	return p.cache[name].value, nil
}

// Invalidate drops every cached value, e.g. after Discord rejected the bot token.
func (p *Params) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache = nil
}
