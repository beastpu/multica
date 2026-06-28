package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Call read-only Multica API endpoints",
}

var apiGetCmd = &cobra.Command{
	Use:   "get <path>",
	Short: "GET a server-relative API path and print JSON",
	Args:  exactArgs(1),
	RunE:  runAPIGet,
}

func init() {
	apiCmd.AddCommand(apiGetCmd)
}

func runAPIGet(cmd *cobra.Command, args []string) error {
	return runAPIGetWithWriter(cmd, args, os.Stdout)
}

func runAPIGetWithWriter(cmd *cobra.Command, args []string, outWriter io.Writer) error {
	path := strings.TrimSpace(args[0])
	if !strings.HasPrefix(path, "/api/") {
		return fmt.Errorf("path must be a server-relative /api/... path")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(cmd.Context())
	defer cancel()

	var raw json.RawMessage
	if err := client.GetJSON(ctx, path, &raw); err != nil {
		return err
	}
	if len(raw) == 0 {
		raw = []byte("null")
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	return cli.PrintJSON(outWriter, out)
}
