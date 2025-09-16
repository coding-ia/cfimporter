package template_parser

import (
	"cfimporter/internal/aws/aws_iam"
	"cfimporter/internal/types"
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"log"
)

type IAMParser struct {
	IAMClient *aws_iam.AWSClient
}

func (ip *IAMParser) parseIAMRole(ctx context.Context, resource types.Resource, resourceName string) *cftypes.ResourceToImport {
	roleName := resource.Properties["RoleName"].(string)
	name, err := ip.IAMClient.GetIAMRoleName(ctx, roleName)
	if err != nil {
		log.Fatal(err)
	}
	if name == nil {
		return nil
	}

	fmt.Printf("IAM role name: %s\n", *name)

	return &cftypes.ResourceToImport{
		ResourceType:      aws.String(resource.Type),
		LogicalResourceId: aws.String(resourceName),
		ResourceIdentifier: map[string]string{
			"RoleName": *name,
		},
	}
}

func (ip *IAMParser) parseIAMPolicy(ctx context.Context, resource types.Resource, resourceName string) *cftypes.ResourceToImport {
	policyName := resource.Properties["ManagedPolicyName"].(string)
	arn, err := ip.IAMClient.FindPolicyArnByName(ctx, policyName)
	if err != nil {
		log.Fatal(err)
	}
	if arn == nil {
		return nil
	}
	fmt.Printf("Policy ARN: %s\n", *arn)

	return &cftypes.ResourceToImport{
		ResourceType:      aws.String(resource.Type),
		LogicalResourceId: aws.String(resourceName),
		ResourceIdentifier: map[string]string{
			"PolicyArn": *arn,
		},
	}
}

func (ip *IAMParser) parseInstanceProfile(ctx context.Context, resource types.Resource, resourceName string, resources map[string]types.Resource) *cftypes.ResourceToImport {
	val := resource.Properties["InstanceProfileName"]
	var profileName string

	if m, ok := val.(map[string]any); ok {
		roleRef := m["Ref"].(string)
		role := resources[roleRef]
		profileName = role.Properties["RoleName"].(string)
	} else if s, ok := val.(string); ok {
		profileName = s
	}

	name, err := ip.IAMClient.GetIAMInstanceProfileName(ctx, profileName)
	if err != nil {
		log.Fatal(err)
	}
	if name == nil {
		return nil
	}
	fmt.Printf("Instance profile name: %s\n", *name)

	return &cftypes.ResourceToImport{
		ResourceType:      aws.String(resource.Type),
		LogicalResourceId: aws.String(resourceName),
		ResourceIdentifier: map[string]string{
			"InstanceProfileName": *name,
		},
	}
}
