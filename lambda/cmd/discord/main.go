package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/ShotaKitazawa/MapleGate/internal/store"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/config"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/discord"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/interactions"
	"github.com/ShotaKitazawa/MapleGate/lambda/internal/presign"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type selfInvoker struct {
	client *lambdasvc.Client
	name   string
}

func (s selfInvoker) InvokeAsync(ctx context.Context, payload []byte) error {
	_, err := s.client.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(s.name),
		InvocationType: lambdatypes.InvocationTypeEvent,
		Payload:        payload,
	})
	return err
}

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
	ssmClient := ssm.NewFromConfig(awsCfg)
	params := &config.Params{Client: ssmClient, Prefix: cfg.ParamPrefix}

	h := &interactions.Handler{
		Cfg:     cfg,
		Store:   &store.Dynamo{Client: dynamodb.NewFromConfig(awsCfg), Table: cfg.TableName},
		Discord: discord.NewREST(func(ctx context.Context) (string, error) { return params.Get(ctx, "discord/bot_token") }, params.Invalidate),
		Invoker: selfInvoker{client: lambdasvc.NewFromConfig(awsCfg), name: os.Getenv("AWS_LAMBDA_FUNCTION_NAME")},
		Now:     time.Now,
		DownloadURL: func(ctx context.Context, platform string) (string, error) {
			key := map[string]string{"macos": cfg.DistKeyMacOS, "windows": cfg.DistKeyWindows}[platform]
			if key == "" {
				return "", nil
			}
			return presign.GetURL(ctx, awsCfg.Credentials, awsCfg.Region, cfg.DistBucket, key, 15*time.Minute, time.Now())
		},
		ChannelID: func(ctx context.Context) (string, error) {
			return params.Get(ctx, "discord/channel_id")
		},
		PublicKey: func(ctx context.Context) (ed25519.PublicKey, error) {
			v, err := params.Get(ctx, "discord/app_public_key")
			if err != nil {
				return nil, err
			}
			return hex.DecodeString(v)
		},
	}

	lambda.Start(func(ctx context.Context, raw json.RawMessage) (any, error) {
		var probe struct {
			Action         string          `json:"action"`
			RequestContext json.RawMessage `json:"requestContext"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, err
		}
		// A Function URL request carries requestContext; the self-invoked async event does not.
		if probe.RequestContext == nil {
			var ev interactions.AsyncEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				return nil, err
			}
			return nil, h.HandleAsync(ctx, ev)
		}
		var req events.LambdaFunctionURLRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return h.Handle(ctx, req), nil
	})
}
