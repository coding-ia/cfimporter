package aws_iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
)

type AWSClient struct {
	Config aws.Config
}

func createIAMClient(_ context.Context, cfg aws.Config) *iam.Client {
	iamClient := iam.NewFromConfig(cfg)
	return iamClient
}

func (awsClient *AWSClient) GetIAMRoleName(ctx context.Context, roleName string) (*string, error) {
	client := createIAMClient(ctx, awsClient.Config)
	output, err := client.GetRole(ctx, &iam.GetRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		var notFound *types.NoSuchEntityException
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get role: %w", err)
	}

	return output.Role.RoleName, nil
}

func (awsClient *AWSClient) GetIAMInstanceProfileName(ctx context.Context, profileName string) (*string, error) {
	client := createIAMClient(ctx, awsClient.Config)
	output, err := client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	})
	if err != nil {
		var notFound *types.NoSuchEntityException
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get instance profile: %w", err)
	}

	return output.InstanceProfile.InstanceProfileName, nil
}

func (awsClient *AWSClient) FindPolicyArnByName(ctx context.Context, policyName string) (*string, error) {
	client := createIAMClient(ctx, awsClient.Config)
	var marker *string

	for {
		output, err := client.ListPolicies(ctx, &iam.ListPoliciesInput{
			Scope:  "All", // Includes AWS managed and customer managed
			Marker: marker,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list policies: %w", err)
		}

		for _, policy := range output.Policies {
			if strings.EqualFold(policyName, aws.ToString(policy.PolicyName)) {
				return policy.Arn, nil
			}
		}

		if output.IsTruncated {
			marker = output.Marker
		} else {
			break
		}
	}

	return nil, nil
}
