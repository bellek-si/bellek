package main

import (
	"os"

	"github.com/bellek-si/bellek/internal/mcpbridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve MCP over stdio by bridging to the server",
	RunE: withApp(func(cmd *cobra.Command, a *app) error {
		ctx := cmd.Context()
		url, token := a.remote()
		remote, err := mcpbridge.Connect(ctx, url, token, Version)
		if err != nil {
			return err
		}
		defer remote.Close()
		cwd, _ := os.Getwd()
		srv, err := mcpbridge.New(ctx, remote, mcpbridge.LocalsFrom(cwd), Version, mcpbridge.WithLogger(a.log))
		if err != nil {
			return err
		}
		a.log.Infow("mcp bridge ready", "server", a.cfg.ServerURL, "cwd", cwd)
		return srv.Run(ctx, &mcp.StdioTransport{})
	}),
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
