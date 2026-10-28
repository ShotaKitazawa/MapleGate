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
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/sessionapi"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/oklog/ulid/v2"
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

	h := &sessionapi.Handler{
		Cfg:     cfg,
		Store:   &store.Dynamo{Client: dynamodb.NewFromConfig(awsCfg), Table: cfg.TableName},
		EC2:     &ec2ctl.AWS{EC2: ec2.NewFromConfig(awsCfg), InstanceID: cfg.InstanceID},
		Discord: discord.NewREST(func(ctx context.Context) (string, error) { return params.Get(ctx, "discord/bot_token") }, params.Invalidate),
		Auth:    sessionapi.IAMAuth{},
		Now:     time.Now,
		NewID:   func() string { return ulid.Make().String() },
		ChannelID: func(ctx context.Context) (string, error) {
			return params.Get(ctx, "discord/channel_id")
		},
		ServerPublicKey: func(ctx context.Context) (string, error) {
			return params.Get(ctx, "server_public_key")
		},
	}

	lambda.Start(handler(h))
}

// lambda.Start rejects a handler that returns a single non-error value, only at the first invoke.
func handler(h *sessionapi.Handler) func(context.Context, events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	return func(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
		return h.Handle(ctx, req), nil
	}
}
