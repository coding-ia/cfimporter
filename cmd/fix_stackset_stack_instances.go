package cmd

import (
	"bytes"
	"cfimporter/internal/template_parser"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
	"log"
	"strings"
	"time"
)

type FixStackSetOptions struct {
	StackSetName string
	RoleName     string
	S3Bucket     string
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
	fixStackSetCmd.Flags().StringVar(&fixStackSetOptions.S3Bucket, "s3-bucket", "", "Bucket to place templates")
}

func fixStackSet(ctx context.Context) {
	if fixStackSetOptions.RoleName == "" {
		fmt.Println("You must specify --role-name to assume into each account")
		return
	}

	parseFailedStackSetInstances(ctx, fixStackSetOptions.StackSetName, fixStackSetOptions.RoleName, fixStackSetOptions.S3Bucket)
}

func parseFailedStackSetInstances(ctx context.Context, stackSetName string, roleName string, bucketName string) {
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

	data := []byte(template)
	templateName, _ := randomFilename(32)
	templateUrl, err := uploadS3File(ctx, cfg, bucketName, templateName, data)
	if err != nil {
		log.Fatal(err)
	}

	var nextToken *string
	for {
		instances, err := cfn.ListStackInstances(ctx, &cloudformation.ListStackInstancesInput{
			StackSetName: aws.String(stackSetName),
			NextToken:    nextToken,
		})
		if err != nil {
			log.Fatalf("failed to list stack instances: %v", err)
		}

		for _, instance := range instances.Summaries {
			if instance.StackInstanceStatus.DetailedStatus == cftypes.StackInstanceDetailedStatusFailed {
				assumedCfg, err := assumeRole(ctx, cfg, aws.ToString(instance.Region), aws.ToString(instance.Account), roleName)
				if err != nil {
					log.Fatal(err)
				}
				assumedCfn := cloudformation.NewFromConfig(assumedCfg)

				cfi := &template_parser.CFImport{
					Config: &assumedCfg,
				}

				importTemplate, resourcesToImport, err := cfi.ParseCloudFormationImportTemplate(ctx, data)
				if err != nil {
					log.Fatal(err)
				}

				importData := []byte(importTemplate)
				importTemplateName, _ := randomFilename(32)
				importTemplateUrl, err := uploadS3File(ctx, cfg, bucketName, importTemplateName, importData)
				if err != nil {
					log.Fatal(err)
				}

				stackName := extractStackName(*instance.StackId)
				stackId, err := importStack(ctx, assumedCfn, stackName, "ImportChangeSet", importTemplateUrl, resourcesToImport)
				if err != nil {
					log.Fatal(err)
				}

				err = waitForImport(ctx, assumedCfn, stackName)
				if err != nil {
					log.Fatal(err)
				}

				err = updateStack(ctx, assumedCfn, stackName, templateUrl)
				if err != nil {
					log.Fatal(err)
				}

				err = deleteStackInstanceFromStackSet(ctx, cfn, stackSetName, aws.ToString(instance.Account), aws.ToString(instance.Region))
				if err != nil {
					log.Fatal(err)
				}

				err = importStackToStackSet(ctx, cfn, stackSetName, aws.ToString(stackId))
				if err != nil {
					log.Fatal(err)
				}

				fmt.Println("Stack instance successfully imported")
			}
		}

		if instances.NextToken == nil {
			break
		}
		nextToken = instances.NextToken
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

func updateStack(ctx context.Context, cfn *cloudformation.Client, stackName, templateUrl string) error {
	input := &cloudformation.UpdateStackInput{
		StackName:   aws.String(stackName),
		TemplateURL: aws.String(templateUrl),
		Capabilities: []cftypes.Capability{
			cftypes.CapabilityCapabilityNamedIam,
		},
		DisableRollback: aws.Bool(true),
	}
	_, err := cfn.UpdateStack(ctx, input)
	if err != nil {
		return err
	}

	waiter := cloudformation.NewStackUpdateCompleteWaiter(cfn)

	err = waiter.Wait(ctx, &cloudformation.DescribeStacksInput{
		StackName: aws.String(stackName),
	}, 30*time.Minute) // max wait time
	return err
}

func importStack(ctx context.Context, cfn *cloudformation.Client, stackName, changeSetName, templateUrl string, resourcesToImport []cftypes.ResourceToImport) (*string, error) {
	input := &cloudformation.CreateChangeSetInput{
		ChangeSetName: aws.String(changeSetName),
		StackName:     aws.String(stackName),
		Capabilities: []cftypes.Capability{
			cftypes.CapabilityCapabilityNamedIam,
		},
		ChangeSetType:     cftypes.ChangeSetTypeImport,
		TemplateURL:       aws.String(templateUrl),
		ResourcesToImport: resourcesToImport,
	}
	output, err := cfn.CreateChangeSet(ctx, input)
	if err != nil {
		return nil, err
	}

	waiter := cloudformation.NewChangeSetCreateCompleteWaiter(cfn)

	err = waiter.Wait(
		ctx,
		&cloudformation.DescribeChangeSetInput{
			StackName:     aws.String(stackName),
			ChangeSetName: aws.String(changeSetName),
		},
		5*time.Minute, // max wait time
	)
	if err != nil {
		return output.StackId, err
	}

	_, err = cfn.ExecuteChangeSet(ctx, &cloudformation.ExecuteChangeSetInput{
		StackName:     aws.String(stackName),
		ChangeSetName: aws.String(changeSetName),
	})
	if err != nil {
		return output.StackId, err
	}

	return output.StackId, nil
}

func importStackToStackSet(ctx context.Context, cfn *cloudformation.Client, stackSetName, stackId string) error {
	stackIds := []string{stackId}
	input := &cloudformation.ImportStacksToStackSetInput{
		StackSetName: aws.String(stackSetName),
		StackIds:     stackIds,
	}

	_, err := cfn.ImportStacksToStackSet(ctx, input)
	return err
}

func deleteStackInstanceFromStackSet(ctx context.Context, cfn *cloudformation.Client, stackSetName, accountId, region string) error {
	accounts := []string{accountId}
	regions := []string{region}
	input := &cloudformation.DeleteStackInstancesInput{
		Regions:      regions,
		RetainStacks: aws.Bool(false),
		Accounts:     accounts,
		StackSetName: aws.String(stackSetName),
	}

	output, err := cfn.DeleteStackInstances(ctx, input)
	if err != nil {
		return err
	}

	operationID := *output.OperationId

	for {
		op, err := cfn.DescribeStackSetOperation(ctx, &cloudformation.DescribeStackSetOperationInput{
			StackSetName: aws.String(stackSetName),
			OperationId:  aws.String(operationID),
		})
		if err != nil {
			return err
		}

		status := string(op.StackSetOperation.Status)
		if status == string(cftypes.StackSetOperationStatusSucceeded) {
			break
		}

		if status == string(cftypes.StackSetOperationStatusFailed) ||
			status == string(cftypes.StackSetOperationStatusStopped) {
			return fmt.Errorf("Deletion failed with status: %s", status)
		}

		time.Sleep(10 * time.Second)
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

func uploadS3File(ctx context.Context, cfg aws.Config, bucket, key string, data []byte) (string, error) {
	client := s3.NewFromConfig(cfg)

	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload data to S3: %w", err)
	}

	region := cfg.Region
	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, key)
	return url, nil
}

func randomFilename(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random filename: %w", err)
	}
	return hex.EncodeToString(b), nil
}
