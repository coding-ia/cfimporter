package cmd

type Resource struct {
	Type       string                 `yaml:"Type"`
	Properties map[string]interface{} `yaml:"Properties"`
}

type CloudFormationTemplate struct {
	Resources map[string]Resource `yaml:"Resources"`
}

type ImportResource struct {
	ResourceType       string            `json:"ResourceType"`
	LogicalResourceId  string            `json:"LogicalResourceId"`
	ResourceIdentifier map[string]string `json:"ResourceIdentifier"`
}
