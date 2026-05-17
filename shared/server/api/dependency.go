package api

type FieldSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Sensitive   bool   `json:"sensitive"`
}

type DependencySpec struct {
	Type         string      `json:"type"`
	Description  string      `json:"description"`
	ConfigFields []FieldSpec `json:"configFields"`
	OutputFields []FieldSpec `json:"outputFields"`
}
