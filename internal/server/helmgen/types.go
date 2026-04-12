package helmgen

type HelmValues struct {
	ReplicaCount     int               `yaml:"replicaCount"`
	Image            Image             `yaml:"image"`
	NameOverride     string            `yaml:"nameOverride"`
	FullnameOverride string            `yaml:"fullnameOverride"`
	Service          HelmService       `yaml:"service"`
	Ingress          HelmIngress       `yaml:"ingress"`
	LivenessProbe    *HelmProbe        `yaml:"livenessProbe,omitempty"`
	ReadinessProbe   *HelmProbe        `yaml:"readinessProbe,omitempty"`
	Env              map[string]string `yaml:"env"`
	Configs          []HelmConfig      `yaml:"configs"`
	PodAnnotations   map[string]string `yaml:"podAnnotations,omitempty"`
	PodLabels        map[string]string `yaml:"podLabels,omitempty"`
}

type Image struct {
	Repository string `yaml:"repository"`
	Tag        string `yaml:"tag"`
	PullPolicy string `yaml:"pullPolicy"`
}

type HelmService struct {
	Type string `yaml:"type"`
	Port int    `yaml:"port"`
}

type HelmIngress struct {
	Enabled     bool              `yaml:"enabled"`
	ClassName   string            `yaml:"className"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
	Hosts       []HelmIngressHost `yaml:"hosts"`
	TLS         []HelmIngressTLS  `yaml:"tls,omitempty"`
}

type HelmIngressHost struct {
	Host  string            `yaml:"host"`
	Paths []HelmIngressPath `yaml:"paths"`
}

type HelmIngressPath struct {
	Path     string `yaml:"path"`
	PathType string `yaml:"pathType"`
}

type HelmIngressTLS struct {
	Hosts      []string `yaml:"hosts"`
	SecretName string   `yaml:"secretName"`
}

type HelmProbe struct {
	HTTPGet HelmHTTPGet `yaml:"httpGet"`
}

type HelmHTTPGet struct {
	Path string `yaml:"path"`
	Port int    `yaml:"port"`
}

type HelmConfig struct {
	Enabled       bool   `yaml:"enabled"`
	MountLocation string `yaml:"mountLocation"`
	Data          string `yaml:"data"`
}
