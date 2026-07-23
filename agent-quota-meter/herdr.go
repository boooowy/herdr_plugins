package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

type agentSession struct {
	Value string `json:"value"`
}

type herdrAgent struct {
	PaneID       string       `json:"pane_id"`
	Agent        string       `json:"agent"`
	AgentStatus  string       `json:"agent_status"`
	CWD          string       `json:"cwd"`
	AgentSession agentSession `json:"agent_session"`
}

type agentListResponse struct {
	Result struct {
		Agents []herdrAgent `json:"agents"`
	} `json:"result"`
}

func (a *app) herdrAgents() ([]herdrAgent, error) {
	output, err := a.runHerdr(10*time.Second, "agent", "list")
	if err != nil {
		return nil, err
	}
	var response agentListResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, err
	}
	return response.Result.Agents, nil
}

func (a *app) report(paneID string, args []string) {
	chunks, err := splitReportArgs(args)
	if err != nil {
		return
	}
	for _, chunk := range chunks {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		commandArgs := []string{
			"pane", "report-metadata", paneID,
			"--source", pluginID,
		}
		commandArgs = append(commandArgs, chunk...)
		_ = exec.CommandContext(ctx, a.herdr, commandArgs...).Run()
		cancel()
	}
}
