package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/notify"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

func main() {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		slog.Error("aws config", "err", err)
		os.Exit(1)
	}
	prefix := os.Getenv("PARAM_PREFIX")
	if prefix == "" {
		prefix = "/gamevpn"
	}
	params := &config.Params{Client: ssm.NewFromConfig(awsCfg), Prefix: prefix}
	rest := discord.NewREST(func(ctx context.Context) (string, error) { return params.Get(ctx, "discord/bot_token") }, params.Invalidate)

	h := notify.Handler{Post: func(ctx context.Context, text string) error {
		ch, err := params.Get(ctx, "discord/channel_id")
		if err != nil {
			return err
		}
		_, err = rest.PostMessage(ctx, ch, discord.Message{Content: text, Components: []discord.Component{}})
		return err
	}}
	lambda.Start(h.Handle)
}
