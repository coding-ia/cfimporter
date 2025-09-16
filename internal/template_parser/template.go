package template_parser

import (
	"cfimporter/internal/aws/aws_iam"
	"cfimporter/internal/types"
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"gopkg.in/yaml.v3"
)

type CFImport struct {
	Config *aws.Config
}

func (cfi *CFImport) ParseCloudFormationImportTemplate(ctx context.Context, data []byte) ([]byte, []cftypes.ResourceToImport, error) {
	iamParser, err := createIAMParserClient(ctx, cfi)
	if err != nil {
		return nil, nil, err
	}

	var template types.CloudFormationTemplate
	err = yaml.Unmarshal(data, &template)
	if err != nil {
		return nil, nil, err
	}

	var importIdentities []cftypes.ResourceToImport
	resources := make(map[string]types.Resource)
	for resourceName, resource := range template.Resources {
		var identity *cftypes.ResourceToImport
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
			importIdentities = append(importIdentities, *identity)
			resource.DeletionPolicy = "Retain"
			resources[resourceName] = resource
		}
	}

	importTemplate := types.CloudFormationTemplate{
		Resources: resources,
	}

	yamlData, err := yaml.Marshal(&importTemplate)
	if err != nil {
		return nil, nil, err
	}

	return yamlData, importIdentities, nil
}

func createIAMParserClient(ctx context.Context, cfi *CFImport) (*IAMParser, error) {
	baseCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	if cfi.Config != nil {
		return &IAMParser{
			IAMClient: &aws_iam.AWSClient{
				Config: *cfi.Config,
			},
		}, nil
	}

	return &IAMParser{
		IAMClient: &aws_iam.AWSClient{
			Config: baseCfg,
		},
	}, nil
}
