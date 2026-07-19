package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Work with the current chat conversation",
}

var chatHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Overview of the channel this conversation is in (messages + thread list)",
	Long: `Show the overview of the chat channel (e.g. Slack) this conversation is in: the
recent top-level messages, and for each thread its thread_id, reply_count, and
latest_reply. It does NOT expand thread contents — it is the table of contents.

To read a specific thread's messages, take a thread_id from here and run
"multica chat thread <thread_id>".

It is the SAME command regardless of which channel the conversation came from,
and it reads only the conversation you are currently running for — it cannot
read any other session or channel.`,
	Args: cobra.NoArgs,
	RunE: runChatHistory,
}

var chatThreadCmd = &cobra.Command{
	Use:   "thread [id]",
	Short: "Read one thread's messages (the current thread, or a specific id)",
	Long: `Read the messages of a single thread.

With no id, read the thread you are currently in (the one you were @mentioned in).
With an id — a thread_id from "multica chat history" — read that specific thread.
Either way the thread is within the channel you are in; you cannot read another
channel.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runChatThread,
}

var chatAskCmd = &cobra.Command{
	Use:   "ask",
	Short: "Ask the user for a decision or for information (structured)",
	Long: `Declare that you need the user before you can continue. The channel renders
the matching UI from this declaration — never from your reply text.

Pick the type by your own state:
  --type confirm  The action is fully specified and you could execute it
                  immediately upon approval. REQUIRES --action describing
                  exactly what will run. Renders approve/cancel buttons.
  --type choice   The answer is one of a small set you already know.
                  Give each candidate with a repeated --option (2-6).
  --type input    You are missing information (a value, an id, a file …).
                  Renders as a plain question; the user answers by typing.

If you cannot fill in --action, you are missing information: use input.
After asking, finish your turn; the user's answer arrives as the next message.`,
	Args: cobra.NoArgs,
	RunE: runChatAsk,
}

func init() {
	for _, c := range []*cobra.Command{chatHistoryCmd, chatThreadCmd} {
		c.Flags().Int("limit", 0, "Maximum number of messages to return (the server clamps the range)")
		c.Flags().String("before", "", "Opaque cursor (a next_cursor from a prior page) to read older messages")
		c.Flags().String("output", "json", "Output format: table or json")
	}
	chatAskCmd.Flags().String("type", "", "Interaction type: confirm, choice, or input (required)")
	chatAskCmd.Flags().String("message", "", "The question shown to the user (required)")
	chatAskCmd.Flags().String("action", "", "confirm only: what will be executed upon approval")
	chatAskCmd.Flags().StringArray("option", nil, "choice only: one candidate answer (repeat 2-6 times)")
	chatAskCmd.Flags().String("hint", "", "input only: short hint for the expected answer, e.g. an example value")
	chatCmd.AddCommand(chatHistoryCmd)
	chatCmd.AddCommand(chatThreadCmd)
	chatCmd.AddCommand(chatAskCmd)
}

func runChatHistory(cmd *cobra.Command, _ []string) error {
	resp, err := fetchChatRead(cmd, "/api/chat/history", "")
	if err != nil {
		return err
	}
	return renderChatRead(cmd, resp, true)
}

func runChatThread(cmd *cobra.Command, args []string) error {
	threadID := ""
	if len(args) == 1 {
		threadID = args[0]
	}
	resp, err := fetchChatRead(cmd, "/api/chat/thread", threadID)
	if err != nil {
		return err
	}
	return renderChatRead(cmd, resp, false)
}

func runChatAsk(cmd *cobra.Command, _ []string) error {
	askType, _ := cmd.Flags().GetString("type")
	message, _ := cmd.Flags().GetString("message")
	action, _ := cmd.Flags().GetString("action")
	options, _ := cmd.Flags().GetStringArray("option")
	hint, _ := cmd.Flags().GetString("hint")

	payload, err := buildChatAskPayload(askType, message, action, options, hint)
	if err != nil {
		return err
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var resp map[string]any
	if err := client.PostJSON(ctx, "/api/chat/ask", payload, &resp); err != nil {
		return fmt.Errorf("create ask: %w", err)
	}
	return cli.PrintJSON(os.Stdout, resp)
}

// buildChatAskPayload validates the per-type flag combinations client-side so
// the agent gets an actionable error before any network call. Length limits
// stay server-side (single source of truth).
func buildChatAskPayload(askType, message, action string, options []string, hint string) (map[string]any, error) {
	askType = strings.TrimSpace(askType)
	message = strings.TrimSpace(message)
	action = strings.TrimSpace(action)
	hint = strings.TrimSpace(hint)
	if message == "" {
		return nil, errors.New("--message is required")
	}
	switch askType {
	case "confirm":
		if action == "" {
			return nil, errors.New("--type confirm requires --action (what will run upon approval); if you are missing information, use --type input instead")
		}
		if len(options) > 0 {
			return nil, errors.New("--option is only valid with --type choice")
		}
	case "choice":
		if action != "" {
			return nil, errors.New("--action is only valid with --type confirm")
		}
		if len(options) < 2 || len(options) > 6 {
			return nil, errors.New("--type choice requires 2-6 --option flags")
		}
	case "input":
		if action != "" || len(options) > 0 {
			return nil, errors.New("--type input takes neither --action nor --option")
		}
	case "":
		return nil, errors.New("--type is required: confirm, choice, or input")
	default:
		return nil, fmt.Errorf("unknown --type %q: use confirm, choice, or input", askType)
	}
	payload := map[string]any{
		"type":    askType,
		"message": message,
	}
	if action != "" {
		payload["action"] = action
	}
	if len(options) > 0 {
		payload["options"] = options
	}
	if hint != "" {
		payload["hint"] = hint
	}
	return payload, nil
}

// fetchChatRead builds the request (shared --limit/--before paging, plus the
// optional thread id) and decodes the response.
func fetchChatRead(cmd *cobra.Command, basePath, threadID string) (map[string]any, error) {
	client, err := newAPIClient(cmd)
	if err != nil {
		return nil, err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	limit, _ := cmd.Flags().GetInt("limit")
	before, _ := cmd.Flags().GetString("before")

	q := url.Values{}
	if threadID != "" {
		q.Set("id", threadID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if before != "" {
		q.Set("before", before)
	}
	path := basePath
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var resp map[string]any
	if err := client.GetJSON(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("read chat: %w", err)
	}
	return resp, nil
}

// renderChatRead prints the response as JSON (default) or a table. The overview
// table adds the thread columns so the agent can pick a thread_id to drill into.
func renderChatRead(cmd *cobra.Command, resp map[string]any, overview bool) error {
	output, _ := cmd.Flags().GetString("output")
	if output != "table" {
		return cli.PrintJSON(os.Stdout, resp)
	}
	if note := strVal(resp, "note"); note != "" {
		fmt.Fprintln(os.Stdout, note)
		return nil
	}
	msgs, _ := resp["messages"].([]any)
	var headers []string
	if overview {
		headers = []string{"TS", "ROLE", "AUTHOR", "THREAD_ID", "REPLIES", "TEXT"}
	} else {
		headers = []string{"TS", "ROLE", "AUTHOR", "TEXT"}
	}
	rows := make([][]string, 0, len(msgs))
	for _, mi := range msgs {
		m, ok := mi.(map[string]any)
		if !ok {
			continue
		}
		if overview {
			rows = append(rows, []string{strVal(m, "ts"), strVal(m, "role"), strVal(m, "author"), strVal(m, "thread_id"), numVal(m, "reply_count"), strVal(m, "text")})
		} else {
			rows = append(rows, []string{strVal(m, "ts"), strVal(m, "role"), strVal(m, "author"), strVal(m, "text")})
		}
	}
	cli.PrintTable(os.Stdout, headers, rows)
	return nil
}

// numVal renders a numeric JSON field as a string, blank when zero/absent.
func numVal(m map[string]any, key string) string {
	if v, ok := m[key].(float64); ok && v != 0 {
		return strconv.Itoa(int(v))
	}
	return ""
}
