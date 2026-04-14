package domain

// DependencySpec describes a built-in dependency type's schema.
type DependencySpec struct {
	Type         string
	Description  string
	ConfigFields []FieldSpec
	OutputFields []FieldSpec
}

type FieldSpec struct {
	Name        string
	Description string
	Required    bool
	Sensitive   bool
}


