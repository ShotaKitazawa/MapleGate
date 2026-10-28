package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/ec2ctl"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/janitor"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("aws config", "err", err)
		os.Exit(1)
	}
	params := &config.Params{Client: ssm.NewFromConfig(awsCfg), Prefix: cfg.ParamPrefix}

	j := &janitor.Janitor{
		Cfg: janitor.Config{
			ProvisionTimeout: cfg.ProvisionWait, HandshakeIdle: cfg.HandshakeIdle, StaleAfter: cfg.StaleAfter,
			MsgSyncGiveUp: cfg.MsgSyncGiveUp, StartCooldown: cfg.StartCooldown, BreakerLimit: cfg.BreakerLimit,
			InstanceID: cfg.InstanceID, UnresponsiveAfter: cfg.UnresponsiveAfter, OverstayAfter: cfg.OverstayAfter, AlertRepeat: cfg.AlertRepeat,
		},
		Store:   &store.Dynamo{Client: dynamodb.NewFromConfig(awsCfg), Table: cfg.TableName},
		EC2:     &ec2ctl.AWS{EC2: ec2.NewFromConfig(awsCfg), InstanceID: cfg.InstanceID},
		Discord: discord.NewREST(func(ctx context.Context) (string, error) { return params.Get(ctx, "discord/bot_token") }, params.Invalidate),
		ChannelID: func(ctx context.Context) (string, error) {
			return params.Get(ctx, "discord/channel_id")
		},
		Now: time.Now,
	}

	lambda.Start(j.Run)
}
