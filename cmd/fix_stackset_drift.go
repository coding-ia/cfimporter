package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/spf13/cobra"
	"log"
	"time"
)

type FixStackSetDriftOptions struct {
	StackSetName string
}

var fixStackSetDriftOptions = &FixStackSetDriftOptions{}

var fixStackSetDriftCmd = &cobra.Command{
	Use:   "fix-stackset-drift",
	Short: "Fixes stack set drift",
	Run: func(cmd *cobra.Command, args []string) {
		fixStackSetDrift(cmd.Context())
	},
}

func init() {
	rootCmd.AddCommand(fixStackSetDriftCmd)

	fixStackSetDriftCmd.Flags().StringVar(&fixStackSetDriftOptions.StackSetName, "stack-set-name", "", "StackSet Name")
}

func fixStackSetDrift(ctx context.Context) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load AWS SDK config, %v", err)
	}

	cfn := cloudformation.NewFromConfig(cfg)

	err = driftedStacks(ctx, cfn, fixStackSetDriftOptions.StackSetName)
	if err != nil {
		log.Fatal(err)
	}
}

func driftedStacks(ctx context.Context, cfn *cloudformation.Client, stackSetName string) error {
	instances, err := cfn.ListStackInstances(ctx, &cloudformation.ListStackInstancesInput{
		StackSetName: aws.String(stackSetName),
	})
	if err != nil {
		log.Fatalf("failed to list stack instances: %v", err)
	}

	for _, instance := range instances.Summaries {
		if instance.DriftStatus == cftypes.StackDriftStatusDrifted {
			drifts, err := cfn.DescribeStackResourceDrifts(ctx, &cloudformation.DescribeStackResourceDriftsInput{
				StackName: instance.StackId,
			})
			if err != nil {
				continue
			}

			for _, d := range drifts.StackResourceDrifts {
				if d.StackResourceDriftStatus != cftypes.StackResourceDriftStatusInSync {
					patchDifferences(ctx, d.PhysicalResourceId, d.ResourceType, d.PropertyDifferences)
				}
			}
		}
	}

	return nil
}

func patchDifferences(ctx context.Context, identifier, resourceType *string, differences []cftypes.PropertyDifference) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load AWS SDK config, %v", err)
	}

	ccc := cloudcontrol.NewFromConfig(cfg)

	for _, d := range differences {
		var patchDocument string

		switch d.DifferenceType {
		case cftypes.DifferenceTypeNotEqual:
			patchDocument = createReplacePatch(d)
		case cftypes.DifferenceTypeAdd:
			patchDocument = createRemovePatch(d)
		case cftypes.DifferenceTypeRemove:
			patchDocument = createAddPatch(d)
		}

		input := &cloudcontrol.UpdateResourceInput{
			Identifier:    identifier,
			TypeName:      resourceType,
			PatchDocument: aws.String(patchDocument),
		}
		out, err := ccc.UpdateResource(ctx, input)
		if err != nil {
			log.Printf("failed to update resource: %v", err)
		}
		err = waitForRequest(ctx, ccc, aws.ToString(out.ProgressEvent.RequestToken))
		if err != nil {
			log.Printf("failed to update resource: %v", err)
		}
	}
}

type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

func createRemovePatch(difference cftypes.PropertyDifference) string {
	patchDoc := []map[string]interface{}{
		{
			"op":   "remove",
			"path": difference.PropertyPath,
		},
	}
	patchBytes, _ := json.MarshalIndent(patchDoc, "", "  ")
	return string(patchBytes)
}

func createAddPatch(difference cftypes.PropertyDifference) string {
	var expectedValue interface{}
	if err := json.Unmarshal([]byte(aws.ToString(difference.ExpectedValue)), &expectedValue); err != nil {
		panic(err)
	}

	patchDoc := []map[string]interface{}{
		{
			"op":    "add",
			"path":  difference.PropertyPath,
			"value": expectedValue,
		},
	}
	patchBytes, _ := json.MarshalIndent(patchDoc, "", "  ")
	return string(patchBytes)
}

func createReplacePatch(difference cftypes.PropertyDifference) string {
	var expectedValue interface{}
	if err := json.Unmarshal([]byte(aws.ToString(difference.ExpectedValue)), &expectedValue); err != nil {
		expectedValue = difference.ExpectedValue
	}

	patchDoc := []map[string]interface{}{
		{
			"op":    "replace",
			"path":  difference.PropertyPath,
			"value": expectedValue,
		},
	}
	patchBytes, _ := json.MarshalIndent(patchDoc, "", "  ")
	return string(patchBytes)
}

func waitForRequest(ctx context.Context, client *cloudcontrol.Client, token string) error {
	for {
		out, err := client.GetResourceRequestStatus(ctx, &cloudcontrol.GetResourceRequestStatusInput{
			RequestToken: aws.String(token),
		})
		if err != nil {
			return err
		}

		status := string(out.ProgressEvent.OperationStatus)
		fmt.Println("Status:", status)

		switch out.ProgressEvent.OperationStatus {
		case "SUCCESS":
			return nil
		case "FAILED", "CANCEL_COMPLETE", "CANCEL_IN_PROGRESS":
			return fmt.Errorf("operation failed: %v", out.ProgressEvent.StatusMessage)
		}

		time.Sleep(5 * time.Second)
	}
}
