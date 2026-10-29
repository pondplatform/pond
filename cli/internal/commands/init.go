package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	"github.com/spf13/cobra"
)

// The commented blocks are the next things most services need. The db in
// pond.yml and in the input file match, so uncommenting both gives a working
// managed database.
const initPondYml = `version: 1
name: %s
image: %s

service:
  port: %d

# Expose it over HTTP at <name>.<environment base host>:
# gateway:
#   http:
#     enabled: true

# env:
#   LOG_LEVEL: info

# A database Pond creates for you; declare its input in pond-input-<env>.yml:
# dependencies:
#   db:
#     type: postgres

# Reference: https://github.com/pondplatform/pond/blob/main/docs/services/pond-yml.md
`

const initInput = `# Input for pond deploy to %s. Every dependency and variable
# declared in pond.yml needs a value here on every deploy.
#
# dependencies:
#   db:
#     managed: true      # Pond creates the database
#     values:
#       instances: 1
#
# variables:
#   SIGNING_KEY: "s3cr3t"
#
# Reference: https://github.com/pondplatform/pond/blob/main/docs/services/dependencies.md
`

var nonServiceNameChars = regexp.MustCompile(`[^a-z0-9]+`)

func NewInitCmd() *cobra.Command {
	var (
		filePath string
		name     string
		image    string
		port     int32
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a pond.yml and an input file for a new service",
		Args:  cobra.NoArgs,
		// init only writes local files: it needs the environment, not the server.
		Annotations: map[string]string{
			"skipServer": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, ok := cmd.Context().Value(pondconfig.ResolvedConfigKey{}).(pondconfig.ResolvedConfig)
			if !ok {
				return fmt.Errorf("internal error: resolved config not available")
			}

			filePath = filepath.Clean(filePath)
			dir := filepath.Dir(filePath)
			if name == "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				name = serviceName(filepath.Base(abs))
				if name == "" {
					return fmt.Errorf("can't derive a service name from directory %q: pass --name", filepath.Base(abs))
				}
			}

			if err := config.Validate(&serviceconfig.ServiceConfig{
				Version: 1,
				Name:    name,
				Image:   image,
				Service: &serviceconfig.ServiceSpec{Port: &port},
			}); err != nil {
				return err
			}

			// Check every file before writing any, so a refusal leaves nothing behind.
			if _, err := os.Stat(filePath); err == nil {
				return fmt.Errorf("%s already exists", filePath)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("check %q: %w", filePath, err)
			}
			var inputPath string
			if resolved.Env != "" {
				existing, err := defaultInputPath(dir, resolved.Env)
				if err != nil {
					return err
				}
				if existing != "" {
					return fmt.Errorf("%s already exists", existing)
				}
				inputPath = filepath.Join(dir, "pond-input-"+resolved.Env+".yml")
			}

			out := cmd.OutOrStdout()
			if err := os.WriteFile(filePath, fmt.Appendf(nil, initPondYml, name, image, port), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", filePath, err)
			}
			fmt.Fprintf(out, "Created %s\n", filePath)

			if inputPath == "" {
				fmt.Fprintln(out, "No environment set, so no input file was created: pass --env to create pond-input-<env>.yml.")
			} else {
				if err := os.WriteFile(inputPath, fmt.Appendf(nil, initInput, resolved.Env), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", inputPath, err)
				}
				fmt.Fprintf(out, "Created %s\n", inputPath)
			}

			fmt.Fprintln(out, "Next: pond deploy --tag <tag> --wait")
			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "pond-file", "f", "./pond.yml", "Path of the pond.yml to create; the input file goes next to it")
	cmd.Flags().StringVar(&name, "name", "", "Service name (default: the pond file's directory name, lowercased with other characters replaced by -)")
	cmd.Flags().StringVar(&image, "image", "", "Docker image reference, without tag")
	cmd.Flags().Int32Var(&port, "port", 8080, "Container port the application listens on")
	cmd.MarkFlagRequired("image")

	return cmd
}

// serviceName turns a directory name into a service name: lowercase
// alphanumerics, with every run of other characters replaced by one "-".
func serviceName(dir string) string {
	return strings.Trim(nonServiceNameChars.ReplaceAllString(strings.ToLower(dir), "-"), "-")
}
