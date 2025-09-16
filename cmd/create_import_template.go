package cmd

import (
	"cfimporter/internal/template_parser"
	"context"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"log"
	"os"
)

type ImportOptions struct {
	TemplateFile string
}

var importOptions = &ImportOptions{}

var importCmd = &cobra.Command{
	Use:   "create-import-template",
	Short: "Create import template",
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

	cfi := &template_parser.CFImport{}

	yamlData, importResources, err := cfi.ParseCloudFormationImportTemplate(ctx, data)
	if err != nil {
		log.Fatal(err)
		return
	}

	err = os.WriteFile("cloudformation_template.yaml", yamlData, 0644)
	if err != nil {
		log.Fatal(err)
	}
	output, err := json.Marshal(importResources)
	if err != nil {
		log.Fatal(err)
	}
	err = os.WriteFile("ResourcesToImport.txt", output, 0644)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Import template successfully created")
}
