// cmd_xander.go — otoxan xander subcommand group (forwards to Xander daemon over Unix socket)
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/silas/otoxan/internal/xander"
	"github.com/spf13/cobra"
)

func newXanderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "xander",
		Short: "Xander admin daemon commands",
		Long:  `Forward admin commands to the running Xander daemon over its Unix socket.`,
	}
	cmd.AddCommand(
		newXanderHealthCmd(),
		newXanderAuditCmd(),
		newXanderCreateAgentCmd(),
		newXanderListAgentsCmd(),
		newXanderDisableAgentCmd(),
		newXanderUpgradeAgentCmd(),
		newXanderGrantScopeCmd(),
		newXanderRotateSelfCmd(),
	)
	return cmd
}

// ------------------------------------------------------------------
// xander health
// ------------------------------------------------------------------

func newXanderHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check Xander daemon health",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := sendXanderOp(xander.OpHealth, nil)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("xander: %s", resp.Error)
			}
			var res xander.HealthResult
			if err := json.Unmarshal(resp.Result, &res); err != nil {
				return fmt.Errorf("xander: decode response: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "status:   %s\nversion:  %s\nuptime:   %ds\n",
				res.Status, res.Version, res.UptimeSec)
			return nil
		},
	}
}

// ------------------------------------------------------------------
// xander audit
// ------------------------------------------------------------------

func newXanderAuditCmd() *cobra.Command {
	var agentName string
	var limit int
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Tail recent audit events from Xander",
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit <= 0 {
				limit = 50
			}
			payload, _ := json.Marshal(xander.AuditTailPayload{
				AgentName: agentName,
				Limit:     limit,
			})
			resp, err := sendXanderOp(xander.OpAuditTail, payload)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("xander: %s", resp.Error)
			}
			var res xander.AuditTailResult
			if err := json.Unmarshal(resp.Result, &res); err != nil {
				return fmt.Errorf("xander: decode response: %w", err)
			}
			if len(res.Events) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no audit events)")
				return nil
			}
			for _, evRaw := range res.Events {
				ev, ok := evRaw.(map[string]any)
				if !ok {
					continue
				}
				ts := "?"
				if t, ok := ev["requested_at"].(string); ok {
					if parsed, err := time.Parse(time.RFC3339, t); err == nil {
						ts = parsed.Format("2006-01-02T15:04:05Z")
					}
				}
				status := "OK"
				if s, ok := ev["success"].(bool); ok && !s {
					status = "FAIL"
				}
				msg := ""
				if e, ok := ev["error"].(string); ok {
					msg = e
				}
				if msg == "" {
					if paths, ok := ev["paths"].([]any); ok {
						msg = fmt.Sprintf("%d paths", len(paths))
					}
				}
				if cmd.Flags().Changed("agent") {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", ts, status, msg)
				} else {
					name := ""
					if n, ok := ev["agent_name"].(string); ok {
						name = n
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %-8s  %-8s  %s\n", ts, name, status, msg)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "filter by agent name")
	cmd.Flags().IntVar(&limit, "limit", 50, "max events to return")
	return cmd
}

// ------------------------------------------------------------------
// xander create-agent
// ------------------------------------------------------------------

func newXanderCreateAgentCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "create-agent <name>",
		Short: "Register a new agent via Xander",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if role == "" {
				role = "worker"
			}
			payload, _ := json.Marshal(xander.CreateAgentPayload{Name: args[0], Role: role})
			resp, err := sendXanderOp(xander.OpCreateAgent, payload)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("xander: %s", resp.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp.Result))
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "worker", "agent role")
	return cmd
}

// ------------------------------------------------------------------
// xander list-agents
// ------------------------------------------------------------------

func newXanderListAgentsCmd() *cobra.Command {
	var limit int
	var includeDeleted bool
	cmd := &cobra.Command{
		Use:   "list-agents",
		Short: "List registered agents via Xander",
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, _ := json.Marshal(xander.ListAgentsPayload{Limit: limit, IncludeDeleted: includeDeleted})
			resp, err := sendXanderOp(xander.OpListAgents, payload)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("xander: %s", resp.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp.Result))
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max results")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "show soft-deleted agents")
	return cmd
}

// ------------------------------------------------------------------
// xander disable-agent
// ------------------------------------------------------------------

func newXanderDisableAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable-agent <name>",
		Short: "Disable an agent via Xander",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, _ := json.Marshal(xander.DisableAgentPayload{AgentName: args[0]})
			resp, err := sendXanderOp(xander.OpDisableAgent, payload)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("xander: %s", resp.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp.Result))
			return nil
		},
	}
}

// ------------------------------------------------------------------
// xander upgrade-agent
// ------------------------------------------------------------------

func newXanderUpgradeAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade-agent <name> <new-role>",
		Short: "Change an agent's role via Xander",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, _ := json.Marshal(xander.UpgradeAgentPayload{AgentName: args[0], NewRole: args[1]})
			resp, err := sendXanderOp(xander.OpUpgradeAgent, payload)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("xander: %s", resp.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp.Result))
			return nil
		},
	}
}

// ------------------------------------------------------------------
// xander grant-scope
// ------------------------------------------------------------------

func newXanderGrantScopeCmd() *cobra.Command {
	var paths []string
	cmd := &cobra.Command{
		Use:   "grant-scope <agent-name>",
		Short: "Grant secret paths to an agent via Xander",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(paths) == 0 {
				return fmt.Errorf("at least one --path is required")
			}
			payload, _ := json.Marshal(xander.GrantScopePayload{AgentName: args[0], SecretPaths: paths})
			resp, err := sendXanderOp(xander.OpGrantScope, payload)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("xander: %s", resp.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp.Result))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&paths, "path", nil, "secret path(s) to grant (required, repeatable)")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

// ------------------------------------------------------------------
// xander rotate-self
// ------------------------------------------------------------------

func newXanderRotateSelfCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate-self",
		Short: "Rotate Xander's own Infisical credential",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := sendXanderOp(xander.OpRotateSelf, nil)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("xander: %s", resp.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(resp.Result))
			return nil
		},
	}
}

// ------------------------------------------------------------------
// IPC client helper
// ------------------------------------------------------------------

func sendXanderOp(op xander.OpType, payload json.RawMessage) (*xander.Response, error) {
	path := xander.SocketPath()
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("xander not running; start with `systemctl --user start xander`")
	}
	defer conn.Close()

	req := xander.Request{
		Op:      op,
		ID:      fmt.Sprintf("cli-%d", time.Now().UnixNano()),
		Payload: payload,
	}
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	if _, err := conn.Write(b); err != nil {
		return nil, fmt.Errorf("xander write: %w", err)
	}

	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("xander read: %w", err)
	}
	var resp xander.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("xander response decode: %w", err)
	}
	return &resp, nil
}
