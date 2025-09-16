package cmd

import (
	"cfimporter/internal/template_parser"
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
	"log"
	"strings"
	"time"
)

type FixStackSetOptions struct {
	StackSetName string
	RoleName     string
}

var fixStackSetOptions = &FixStackSetOptions{}

var fixStackSetCmd = &cobra.Command{
	Use:   "fix-stackset-stack-instances",
	Short: "Fixes a stack set stack instances",
	Run: func(cmd *cobra.Command, args []string) {
		fixStackSet(cmd.Context())
	},
}

func init() {
	rootCmd.AddCommand(fixStackSetCmd)

	fixStackSetCmd.Flags().StringVar(&fixStackSetOptions.StackSetName, "stack-set-name", "", "StackSet Name")
	fixStackSetCmd.Flags().StringVar(&fixStackSetOptions.RoleName, "role-name", "", "Role name to assume into each account")
}

func fixStackSet(ctx context.Context) {
	if fixStackSetOptions.RoleName == "" {
		fmt.Println("You must specify --role-name to assume into each account")
		return
	}

	parseFailedStackSetInstances(ctx, fixStackSetOptions.StackSetName, fixStackSetOptions.RoleName)
}

func parseFailedStackSetInstances(ctx context.Context, stackSetName string, roleName string) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load AWS SDK config, %v", err)
	}

	cfn := cloudformation.NewFromConfig(cfg)

	template, err := getStackSetTemplate(ctx, cfn, stackSetName)
	if err != nil {
		log.Fatalf("unable to get stack set template, %v", err)
		return
	}

	var nextToken *string
	for {
		out, err := cfn.ListStackInstances(ctx, &cloudformation.ListStackInstancesInput{
			StackSetName: aws.String(stackSetName),
			NextToken:    nextToken,
		})
		if err != nil {
			log.Fatalf("failed to list stack instances: %v", err)
		}

		for _, instance := range out.Summaries {
			if instance.StackInstanceStatus.DetailedStatus == cftypes.StackInstanceDetailedStatusFailed {
				assumedCfg, err := assumeRole(ctx, cfg, aws.ToString(instance.Region), aws.ToString(instance.Account), roleName)
				if err != nil {
					log.Fatal(err)
				}
				assumedCfn := cloudformation.NewFromConfig(assumedCfg)

				data := []byte(template)

				cfi := &template_parser.CFImport{
					Config: &assumedCfg,
				}

				importTemplate, resourcesToImport, err := cfi.ParseCloudFormationImportTemplate(ctx, data)
				if err != nil {
					log.Fatal(err)
				}

				stackName := extractStackName(*instance.StackId)
				err = importStack(ctx, assumedCfn, stackName, "ImportChangeSet", string(importTemplate), resourcesToImport)
				if err != nil {
					log.Fatal(err)
				}

				err = waitForImport(ctx, assumedCfn, stackName)
				if err != nil {
					log.Fatal(err)
				}

				err = updateStackSet(ctx, cfn, stackSetName, aws.ToString(instance.Account), aws.ToString(instance.Region))
				if err != nil {
					log.Fatal(err)
				}

				fmt.Println("Stack instance successfully imported")
			}
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
}

func getStackSetTemplate(ctx context.Context, cfn *cloudformation.Client, stackSetName string) (string, error) {
	out, err := cfn.DescribeStackSet(ctx, &cloudformation.DescribeStackSetInput{
		StackSetName: aws.String(stackSetName),
	})
	if err != nil {
		return "", err
	}

	if out != nil {
		return *out.StackSet.TemplateBody, nil
	}

	return "", errors.New("stack set not found")
}

func importStack(ctx context.Context, cfn *cloudformation.Client, stackSetName, changeSetName, templateBody string, resourcesToImport []cftypes.ResourceToImport) error {
	input := &cloudformation.CreateChangeSetInput{
		ChangeSetName: aws.String(changeSetName),
		StackName:     aws.String(stackSetName),
		Capabilities: []cftypes.Capability{
			cftypes.CapabilityCapabilityNamedIam,
		},
		ChangeSetType:     cftypes.ChangeSetTypeImport,
		TemplateBody:      aws.String(templateBody),
		ResourcesToImport: resourcesToImport,
	}
	_, err := cfn.CreateChangeSet(ctx, input)
	if err != nil {
		return err
	}

	waiter := cloudformation.NewChangeSetCreateCompleteWaiter(cfn)

	err = waiter.Wait(
		ctx,
		&cloudformation.DescribeChangeSetInput{
			StackName:     aws.String(stackSetName),
			ChangeSetName: aws.String(changeSetName),
		},
		5*time.Minute, // max wait time
	)
	if err != nil {
		return err
	}

	_, err = cfn.ExecuteChangeSet(ctx, &cloudformation.ExecuteChangeSetInput{
		StackName:     aws.String(stackSetName),
		ChangeSetName: aws.String(changeSetName),
	})
	if err != nil {
		return err
	}

	return nil
}

func updateStackSet(ctx context.Context, cfn *cloudformation.Client, stackSetName, account, region string) error {
	accounts := []string{account}
	regions := []string{region}

	_, err := cfn.UpdateStackInstances(ctx, &cloudformation.UpdateStackInstancesInput{
		StackSetName: aws.String(stackSetName),
		Accounts:     accounts,
		Regions:      regions,
	})
	if err != nil {
		return err
	}
	return nil
}

func extractStackName(arn string) string {
	parts := strings.Split(arn, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func waitForImport(ctx context.Context, cfn *cloudformation.Client, stackName string) error {
	waiter := cloudformation.NewStackImportCompleteWaiter(cfn)

	err := waiter.Wait(
		ctx,
		&cloudformation.DescribeStacksInput{
			StackName: aws.String(stackName),
		},
		15*time.Minute, // adjust as needed
	)
	if err != nil {
		return fmt.Errorf("failed while waiting for stack import to complete: %w", err)
	}

	return nil
}

func assumeRole(ctx context.Context, baseCfg aws.Config, region, accountID, roleName string) (aws.Config, error) {
	baseCfg.Region = region
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
