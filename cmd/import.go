package cmd

import (
	"cfimporter/cmd/aws/aws_iam"
	"context"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"log"
	"os"
)

type ImportOptions struct {
	TemplateFile string
}

var importOptions = &ImportOptions{}

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Create import",
	Run: func(cmd *cobra.Command, args []string) {
		createImportTemplate(cmd.Context())
	},
}

func init() {
	rootCmd.AddCommand(importCmd)

	importCmd.Flags().StringVar(&importOptions.TemplateFile, "cf-template", "", "CloudFormation template file")
}

func createImportTemplate(ctx context.Context) {
	data, err := os.ReadFile(importOptions.TemplateFile)
	if err != nil {
		log.Fatal(err)
	}

	var template CloudFormationTemplate
	err = yaml.Unmarshal(data, &template)
	if err != nil {
		log.Fatal(err)
	}

	var importIdentities []*ImportResource
	resources := make(map[string]Resource)
	for resourceName, resource := range template.Resources {
		var identity *ImportResource
		if resource.Type == "AWS::IAM::ManagedPolicy" {
			identity = parseIAMPolicy(ctx, resource, resourceName)
		}
		if resource.Type == "AWS::IAM::Role" {
			identity = parseIAMRole(ctx, resource, resourceName)
		}
		if resource.Type == "AWS::IAM::InstanceProfile" {
			fmt.Println("IAM Instance Profile")
			identity = parseInstanceProfile(ctx, resource, resourceName)
		}

		if identity != nil {
			importIdentities = append(importIdentities, identity)
			resource.DeletionPolicy = "Retain"
			resources[resourceName] = resource
		}
	}

	output, err := json.Marshal(importIdentities)
	if err != nil {
		log.Fatal(err)
	}

	importTemplate := CloudFormationTemplate{
		Resources: resources,
	}

	yamlData, err := yaml.Marshal(&importTemplate)
	if err != nil {
		log.Fatal(err)
	}

	err = os.WriteFile("cloudformation_template.yaml", yamlData, 0644)
	if err != nil {
		log.Fatal(err)
	}
	err = os.WriteFile("ResourcesToImport.txt", output, 0644)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Import template successfully created")
}

func parseIAMRole(ctx context.Context, resource Resource, resourceName string) *ImportResource {
	roleName := resource.Properties["RoleName"].(string)
	name, err := aws_iam.GetIAMRoleName(ctx, roleName)
	if err != nil {
		log.Fatal(err)
	}
	if name == nil {
		return nil
	}

	fmt.Printf("IAM role name: %s\n", *name)

	return &ImportResource{
		ResourceType:      resource.Type,
		LogicalResourceId: resourceName,
		ResourceIdentifier: map[string]string{
			"RoleName": *name,
		},
	}
}

func parseIAMPolicy(ctx context.Context, resource Resource, resourceName string) *ImportResource {
	policyName := resource.Properties["ManagedPolicyName"].(string)
	arn, err := aws_iam.FindPolicyArnByName(ctx, policyName)
	if err != nil {
		log.Fatal(err)
	}
	if arn == nil {
		return nil
	}
	fmt.Printf("Policy ARN: %s\n", *arn)

	return &ImportResource{
		ResourceType:      resource.Type,
		LogicalResourceId: resourceName,
		ResourceIdentifier: map[string]string{
			"PolicyArn": *arn,
		},
	}
}

func parseInstanceProfile(ctx context.Context, resource Resource, resourceName string) *ImportResource {
	val := resource.Properties["InstanceProfileName"]
	var profileName string

	if m, ok := val.(map[string]any); ok {
		profileName = m["Ref"].(string)
	} else if s, ok := val.(string); ok {
		profileName = s
	}

	name, err := aws_iam.GetIAMInstanceProfileName(ctx, profileName)
	if err != nil {
		log.Fatal(err)
	}
	if name == nil {
		return nil
	}
	fmt.Printf("Instance profile name: %s\n", *name)

	return &ImportResource{
		ResourceType:      resource.Type,
		LogicalResourceId: resourceName,
		ResourceIdentifier: map[string]string{
			"InstanceProfileName": *name,
		},
	}
}
