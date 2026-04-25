package domain

// DependencySpec describes a built-in dependency type's schema.
type DependencySpec struct {
	Type         string      `json:"type"`
	Description  string      `json:"description"`
	ConfigFields []FieldSpec `json:"configFields"`
	OutputFields []FieldSpec `json:"outputFields"`
}

type FieldSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Sensitive   bool   `json:"sensitive"`
}


