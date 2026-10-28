package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	TableName         string
	InstanceID        string
	ParamPrefix       string
	WGPort            int
	WGSubnet          string // e.g. "10.66.0"
	SessionTTL        time.Duration
	PendingTTL        time.Duration
	ProvisionWait     time.Duration
	StaleAfter        time.Duration
	HandshakeIdle     time.Duration
	MsgSyncGiveUp     time.Duration
	StartCooldown     time.Duration
	BreakerLimit      int
	ExtendEvery       time.Duration
	UnresponsiveAfter time.Duration
	OverstayAfter     time.Duration
	AlertRepeat       time.Duration
	AllowedIPs        []string
	Keepalive         int
}

func Load() (Config, error) {
	c := Config{
		TableName:         os.Getenv("TABLE_NAME"),
		InstanceID:        os.Getenv("INSTANCE_ID"),
		ParamPrefix:       envOr("PARAM_PREFIX", "/gamevpn"),
		WGPort:            envInt("WG_PORT", 51820),
		WGSubnet:          envOr("WG_SUBNET_PREFIX", "10.66.0"),
		SessionTTL:        envDur("SESSION_TTL", 4*time.Hour),
		PendingTTL:        envDur("PENDING_TTL", 15*time.Minute),
		ProvisionWait:     envDur("PROVISION_TIMEOUT", 5*time.Minute),
		StaleAfter:        envDur("NODE_STALE_AFTER", 2*time.Minute),
		HandshakeIdle:     envDur("HANDSHAKE_IDLE", 10*time.Minute),
		MsgSyncGiveUp:     envDur("MSG_SYNC_GIVEUP", time.Hour),
		StartCooldown:     envDur("START_COOLDOWN", 15*time.Minute),
		BreakerLimit:      envInt("START_BREAKER_LIMIT", 2),
		ExtendEvery:       envDur("SESSION_EXTEND_EVERY", 30*time.Minute),
		UnresponsiveAfter: envDur("NODE_UNRESPONSIVE_AFTER", 10*time.Minute),
		OverstayAfter:     envDur("NODE_OVERSTAY_AFTER", 35*time.Minute),
		AlertRepeat:       envDur("NODE_ALERT_REPEAT", time.Hour),
		AllowedIPs:        strings.Split(envOr("ALLOWED_IPS", "0.0.0.0/0,::/0"), ","),
		Keepalive:         envInt("PERSISTENT_KEEPALIVE", 25),
	}
	if c.TableName == "" || c.InstanceID == "" {
		return c, fmt.Errorf("TABLE_NAME and INSTANCE_ID are required")
	}
	return c, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
