module github.com/pondplatform/pond/cli

go 1.25.5

require (
	github.com/google/uuid v1.6.0
	github.com/pondplatform/pond/shared v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/pondplatform/pond/shared => ../shared
