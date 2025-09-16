package template_parser

import (
	"cfimporter/internal/aws/aws_iam"
	"cfimporter/internal/types"
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"gopkg.in/yaml.v3"
	"time"
)

type CFImport struct {
	Account  *string
	RoleName *string
}

func (cfi *CFImport) ParseCloudFormationImportTemplate(ctx context.Context, data []byte) ([]byte, []byte, error) {
	iamParser, err := createIAMParserClient(ctx, cfi)
	if err != nil {
		return nil, nil, err
	}

	var template types.CloudFormationTemplate
	err = yaml.Unmarshal(data, &template)
	if err != nil {
		return nil, nil, err
	}

	var importIdentities []*types.ImportResource
	resources := make(map[string]types.Resource)
	for resourceName, resource := range template.Resources {
		var identity *types.ImportResource
		if resource.Type == "AWS::IAM::ManagedPolicy" {
			identity = iamParser.parseIAMPolicy(ctx, resource, resourceName)
		}
		if resource.Type == "AWS::IAM::Role" {
			identity = iamParser.parseIAMRole(ctx, resource, resourceName)
		}
		if resource.Type == "AWS::IAM::InstanceProfile" {
			identity = iamParser.parseInstanceProfile(ctx, resource, resourceName, template.Resources)
		}

		if identity != nil {
			importIdentities = append(importIdentities, identity)
			resource.DeletionPolicy = "Retain"
			resources[resourceName] = resource
		}
	}

	output, err := json.Marshal(importIdentities)
	if err != nil {
		return nil, nil, err
	}

	importTemplate := types.CloudFormationTemplate{
		Resources: resources,
	}

	yamlData, err := yaml.Marshal(&importTemplate)
	if err != nil {
		return nil, nil, err
	}

	return output, yamlData, nil
}

func createIAMParserClient(ctx context.Context, cfi *CFImport) (*IAMParser, error) {
	baseCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	if cfi.RoleName != nil && *cfi.RoleName != "" {
		cfg, err := assumeRole(ctx, baseCfg, aws.ToString(cfi.Account), aws.ToString(cfi.RoleName))
		if err != nil {
			return nil, err
		}
		return &IAMParser{
			IAMClient: &aws_iam.AWSClient{
				Config: cfg,
			},
		}, nil
	}

	return &IAMParser{
		IAMClient: &aws_iam.AWSClient{
			Config: baseCfg,
		},
	}, nil
}

func assumeRole(ctx context.Context, baseCfg aws.Config, accountID, roleName string) (aws.Config, error) {
	stsClient := sts.NewFromConfig(baseCfg)

	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)

	out, err := stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String(fmt.Sprintf("stack-importer-%d", time.Now().Unix())),
		DurationSeconds: aws.Int32(3600),
	})
	if err != nil {
		return aws.Config{}, fmt.Errorf("assume role into %s failed: %w", accountID, err)
	}

	creds := aws.Credentials{
		AccessKeyID:     aws.ToString(out.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(out.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(out.Credentials.SessionToken),
		Source:          "AssumeRole",
		CanExpire:       true,
		Expires:         aws.ToTime(out.Credentials.Expiration),
	}

	assumedCfg := baseCfg.Copy()
	assumedCfg.Credentials = aws.NewCredentialsCache(
		aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return creds, nil
		}),
	)

	return assumedCfg, nil
}
