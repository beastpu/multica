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
	Short: "Call Multica API endpoints",
}

var apiGetCmd = &cobra.Command{
	Use:   "get <path>",
	Short: "GET a server-relative API path and print JSON",
	Args:  exactArgs(1),
	RunE:  runAPIGet,
}

var apiPostContentFile string

var apiPostCmd = &cobra.Command{
	Use:   "post <path>",
	Short: "POST a JSON body to a server-relative API path and print the JSON response",
	Long: "POST a JSON body to a server-relative /api/... path.\n\n" +
		"Provide the body with --content-file <file> (recommended: shell quoting\n" +
		"can't corrupt a file) or pipe it on stdin. The body must be one JSON value.",
	Args: exactArgs(1),
	RunE: runAPIPost,
}

func init() {
	apiCmd.AddCommand(apiGetCmd)
	apiCmd.AddCommand(apiPostCmd)
	apiPostCmd.Flags().StringVar(&apiPostContentFile, "content-file", "",
		"read the JSON request body from this file ('-' or unset reads stdin)")
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

func runAPIPost(cmd *cobra.Command, args []string) error {
	return runAPIPostWithWriter(cmd, args, os.Stdin, os.Stdout)
}

func runAPIPostWithWriter(cmd *cobra.Command, args []string, in io.Reader, outWriter io.Writer) error {
	path := strings.TrimSpace(args[0])
	if !strings.HasPrefix(path, "/api/") {
		return fmt.Errorf("path must be a server-relative /api/... path")
	}
	var body []byte
	var err error
	if apiPostContentFile == "" || apiPostContentFile == "-" {
		body, err = io.ReadAll(in)
	} else {
		body, err = os.ReadFile(apiPostContentFile)
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return fmt.Errorf("request body is empty; pass --content-file <file> or pipe JSON on stdin")
	}
	var probe any
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("request body is not valid JSON: %w", err)
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(cmd.Context())
	defer cancel()

	var raw json.RawMessage
	if err := client.PostJSON(ctx, path, json.RawMessage(body), &raw); err != nil {
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
