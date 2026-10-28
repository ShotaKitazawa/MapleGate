package janitor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ec2ctl"
)

// watch posts to Discord when the node outlives what its own logic should allow. It only tells a human: stopping an
// instance from here could cut a live session on a wrong guess, and the janitor holds no StopInstances right.
func (j *Janitor) watch(ctx context.Context, h *store.Health, state ec2ctl.State, node store.Node, needNode int, now time.Time) {
	if state == "" {
		return
	}
	if state != ec2ctl.StateRunning {
		h.RunningSince, h.IdleSince = time.Time{}, time.Time{}
		h.UnresponsiveAlertedAt, h.OverstayAlertedAt = time.Time{}, time.Time{}
		return
	}
	if h.RunningSince.IsZero() {
		h.RunningSince = now
	}

	// A heartbeat older than the boot is a leftover from the previous run, so silence only counts after the grace period.
	silent := now.Sub(h.RunningSince) > j.Cfg.UnresponsiveAfter && now.Sub(node.LastSeen) > j.Cfg.UnresponsiveAfter
	switch {
	case needNode > 0 || silent:
		h.IdleSince = time.Time{}
	case h.IdleSince.IsZero():
		h.IdleSince = now
	}
	overstay := !h.IdleSince.IsZero() && now.Sub(h.IdleSince) > j.Cfg.OverstayAfter

	silentFor := now.Sub(node.LastSeen)
	if node.LastSeen.Before(h.RunningSince) {
		silentFor = now.Sub(h.RunningSince)
	}
	j.alert(ctx, &h.UnresponsiveAlertedAt, silent, now, fmt.Sprintf(
		"VPN node `%s` is running but has not reported for %d min. It may be hung; check it in the EC2 console and stop it if it is not needed.",
		j.Cfg.InstanceID, int(silentFor.Minutes())))
	j.alert(ctx, &h.OverstayAlertedAt, overstay, now, fmt.Sprintf(
		"VPN node `%s` has been running with no live session for %d min, longer than it should. Check it and stop it if it is not needed.",
		j.Cfg.InstanceID, int(now.Sub(h.IdleSince).Minutes())))
}

// alert posts once per incident and again every AlertRepeat while it lasts; a failed post is retried on the next run.
func (j *Janitor) alert(ctx context.Context, at *time.Time, active bool, now time.Time, text string) {
	if !active {
		*at = time.Time{}
		return
	}
	if !at.IsZero() && now.Sub(*at) < j.Cfg.AlertRepeat {
		return
	}
	ch, err := j.ChannelID(ctx)
	if err == nil {
		_, err = j.Discord.PostMessage(ctx, ch, discord.Message{Content: text, Components: []discord.Component{}})
	}
	if err != nil {
		slog.Warn("post node alert", "err", err)
		return
	}
	*at = now
}
