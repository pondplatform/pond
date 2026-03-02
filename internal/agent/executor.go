package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

type executor struct {
	helmRunner HelmRunner
	tofuRunner TofuRunner
}

func NewExecutor(helmRunner HelmRunner, tofuRunner TofuRunner) CommandExecutor {
	return &executor{
		helmRunner: helmRunner,
		tofuRunner: tofuRunner,
	}
}

func (e *executor) Execute(ctx context.Context, cmd *Command) (*CommandResult, error) {
	var result *CommandResult
	var err error

	switch cmd.Type {
	case CommandHelmUpgrade:
		result, err = e.executeHelmUpgrade(ctx, cmd)
	case CommandTofuApply:
		result, err = e.executeTofuApply(ctx, cmd)
	case CommandTofuOutput:
		result, err = e.executeTofuOutput(ctx, cmd)
	default:
		return &CommandResult{
			CommandID: cmd.ID,
			Success:   false,
			Error:     fmt.Sprintf("unknown command type: %s", cmd.Type),
		}, nil
	}

	if err != nil {
		return &CommandResult{
			CommandID: cmd.ID,
			Success:   false,
			Error:     err.Error(),
		}, nil
	}

	return result, nil
}

func (e *executor) executeHelmUpgrade(ctx context.Context, cmd *Command) (*CommandResult, error) {
	var req HelmUpgradeRequest
	if err := json.Unmarshal(cmd.Payload, &req); err != nil {
		return nil, fmt.Errorf("unmarshal helm upgrade request: %w", err)
	}

	if err := e.helmRunner.Upgrade(ctx, req); err != nil {
		return nil, fmt.Errorf("helm upgrade: %w", err)
	}

	return &CommandResult{
		CommandID: cmd.ID,
		Success:   true,
	}, nil
}

func (e *executor) executeTofuApply(ctx context.Context, cmd *Command) (*CommandResult, error) {
	var payload struct {
		WorkDir string            `json:"workDir"`
		Vars    map[string]string `json:"vars"`
	}
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal tofu apply request: %w", err)
	}

	if err := e.tofuRunner.Init(ctx, payload.WorkDir); err != nil {
		return nil, fmt.Errorf("tofu init: %w", err)
	}

	if err := e.tofuRunner.Apply(ctx, payload.WorkDir, payload.Vars); err != nil {
		return nil, fmt.Errorf("tofu apply: %w", err)
	}

	outputs, err := e.tofuRunner.Output(ctx, payload.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	outputJSON, err := json.Marshal(outputs)
	if err != nil {
		return nil, fmt.Errorf("marshal outputs: %w", err)
	}

	return &CommandResult{
		CommandID: cmd.ID,
		Success:   true,
		Output:    outputJSON,
	}, nil
}

func (e *executor) executeTofuOutput(ctx context.Context, cmd *Command) (*CommandResult, error) {
	var payload struct {
		WorkDir string `json:"workDir"`
	}
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal tofu output request: %w", err)
	}

	outputs, err := e.tofuRunner.Output(ctx, payload.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	outputJSON, err := json.Marshal(outputs)
	if err != nil {
		return nil, fmt.Errorf("marshal outputs: %w", err)
	}

	return &CommandResult{
		CommandID: cmd.ID,
		Success:   true,
		Output:    outputJSON,
	}, nil
}
