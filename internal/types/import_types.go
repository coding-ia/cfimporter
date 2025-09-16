package types

type Resource struct {
	Type           string                 `yaml:"Type"`
	DeletionPolicy string                 `yaml:"DeletionPolicy"`
	Properties     map[string]interface{} `yaml:"Properties"`
}

type CloudFormationTemplate struct {
	Resources map[string]Resource `yaml:"Resources"`
}
