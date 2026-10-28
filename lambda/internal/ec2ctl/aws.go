package ec2ctl

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/smithy-go"
)

type AWS struct {
	EC2        *ec2.Client
	InstanceID string
}

func (a *AWS) State(ctx context.Context) (State, error) {
	out, err := a.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{a.InstanceID}})
	if err != nil {
		var ae smithy.APIError
		if errors.As(err, &ae) && ae.ErrorCode() == "InvalidInstanceID.NotFound" {
			return StateMissing, nil
		}
		return "", err
	}
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			return State(i.State.Name), nil
		}
	}
	return StateMissing, nil
}

func (a *AWS) EnsureStarted(ctx context.Context) error {
	st, err := a.State(ctx)
	if err != nil {
		return err
	}
	switch st {
	case StateStopped:
		_, err := a.EC2.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{a.InstanceID}})
		return err
	case StateMissing, StateTerminated, StateShuttingDown:
		return fmt.Errorf("instance %s is %s", a.InstanceID, st)
	}
	return nil
}
