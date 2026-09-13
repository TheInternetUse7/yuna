package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/applog"
	"github.com/TheInternetUse7/yuna/internal/config"
)

// Response is the part of a chat completion Yuna cares about.
type Response struct {
	Text       string
	Provider   string // provider that actually answered
	Model      string
	IsFallback bool
	ToolCalls  []ToolCall

	// PrimaryProvider and PrimaryModel name what the request asked for first
	// and are set only when IsFallback is true, so a caller can say which
	// provider was skipped. The reason it was skipped lives in the debug log:
	// Bifrost reports the failed attempt to the logger supplied at Init.
	PrimaryProvider string
	PrimaryModel    string
}

// ToolCall is one function call the model asked for.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // stringified JSON
}

// ChatError carries the upstream status code so callers can tell a schema
// rejection from an outage.
type ChatError struct {
	StatusCode int
	Message    string
}

func (e *ChatError) Error() string {
	if e.StatusCode == 0 {
		return "ai request failed: " + e.Message
	}
	return fmt.Sprintf("ai request failed (HTTP %d): %s", e.StatusCode, e.Message)
}

// Client wraps the single process-wide Bifrost instance.
type Client struct {
	bf  *bifrost.Bifrost
	log *applog.Logger
}

// NewClient starts Bifrost with the configured account.
func NewClient(ctx context.Context, account *Account, log *applog.Logger) (*Client, error) {
	// Routing decisions -- which provider failed and why a fallback took over --
	// are only visible through Bifrost's own logger, so hand it Yuna's.
	bf, err := bifrost.Init(ctx, schemas.BifrostConfig{
		Account: account,
		Logger:  newBifrostLogger(log),
	})
	if err != nil {
		return nil, fmt.Errorf("initialise bifrost: %w", err)
	}
	return &Client{bf: bf, log: log}, nil
}

// Close shuts Bifrost and its provider pools down.
func (c *Client) Close() { c.bf.Shutdown() }

// request is the internal shape shared by chat and summarisation calls.
type request struct {
	primary     config.Provider
	model       string // defaults to primary's first model
	fallbacks   []schemas.Fallback
	messages    []schemas.ChatMessage
	tools       []schemas.ChatTool
	temperature *float64
}

func (c *Client) do(ctx context.Context, req request) (*Response, error) {
	model := req.model
	if model == "" {
		model = req.primary.Default()
	}

	params := &schemas.ChatParameters{}
	if len(req.tools) > 0 {
		params.Tools = req.tools
	}
	if req.temperature != nil {
		params.Temperature = req.temperature
	}

	// The caller's context must stay the parent so cancellation propagates;
	// Bifrost owns the per-provider deadline.
	bfCtx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
	started := time.Now()
	resp, berr := c.bf.ChatCompletionRequest(bfCtx, &schemas.BifrostChatRequest{
		Provider:  req.primary.Driver,
		Model:     model,
		Input:     req.messages,
		Params:    params,
		Fallbacks: req.fallbacks,
	})
	if berr != nil {
		return nil, toChatError(berr)
	}
	out := parseResponse(resp)
	// Bifrost's own lines say that a request ran but not how long it took,
	// and the duration is what makes a slow provider visible before it fails.
	c.log.Debugf("chat_completion provider=%s model=%s fallback=%t took=%s",
		out.Provider, out.Model, out.IsFallback, time.Since(started).Round(time.Millisecond))
	if out.IsFallback {
		c.log.Warnf("provider %s did not answer; %s served the request instead (see the debug log for the failure)",
			out.PrimaryProvider, out.Provider)
	}
	return out, nil
}

// Chat sends one request, letting Bifrost fail over down the chain.
func (c *Client) Chat(ctx context.Context, primary config.Provider, chain []config.Provider,
	msgs []schemas.ChatMessage, tools []schemas.ChatTool) (*Response, error) {
	return c.do(ctx, request{
		primary:   primary,
		fallbacks: fallbacksFor(chain, primary, primary.Default()),
		messages:  msgs,
		tools:     tools,
	})
}

// ExecFunc runs one tool call locally and returns the text handed back to the
// model. It must never perform network I/O on the model's behalf.
type ExecFunc func(ctx context.Context, call ToolCall) string

// ChatWithTools runs a single-round tool loop: ask, execute locally, then ask
// once more with the results appended.
func (c *Client) ChatWithTools(ctx context.Context, primary config.Provider, chain []config.Provider,
	msgs []schemas.ChatMessage, tools []schemas.ChatTool, exec ExecFunc) (*Response, error) {
	if !primary.Tools || len(tools) == 0 {
		return c.Chat(ctx, primary, chain, msgs, nil)
	}

	first, err := c.Chat(ctx, primary, chain, msgs, tools)
	if err != nil {
		// Some providers reject a tool schema outright. Serve the turn without
		// tools instead of failing it.
		var ce *ChatError
		if errors.As(err, &ce) && ce.StatusCode == http.StatusBadRequest {
			c.log.Warnf("provider %s rejected the tool schema (HTTP 400); retrying without tools", primary.Name)
			return c.Chat(ctx, primary, chain, msgs, nil)
		}
		return nil, err
	}
	if len(first.ToolCalls) == 0 {
		return first, nil
	}
	return c.continueWithTools(ctx, chain, msgs, first, exec)
}

func (c *Client) continueWithTools(ctx context.Context, chain []config.Provider,
	msgs []schemas.ChatMessage, first *Response, exec ExecFunc) (*Response, error) {
	winner, ok := providerByDriver(chain, first.Provider)
	if !ok {
		c.log.Warnf("tool calls came back from unconfigured provider %q; ignoring them", first.Provider)
		if first.Text != "" {
			return first, nil
		}
		return nil, fmt.Errorf("tool calls from unrecognised provider %q", first.Provider)
	}

	// Tool call IDs are only valid on the provider that issued them, so the
	// continuation is pinned there with no fallbacks.
	convo := make([]schemas.ChatMessage, 0, len(msgs)+1+len(first.ToolCalls))
	convo = append(convo, msgs...)
	convo = append(convo, schemas.ChatMessage{
		Role:                 schemas.ChatMessageRoleAssistant,
		ChatAssistantMessage: &schemas.ChatAssistantMessage{ToolCalls: toolCallsToSchema(first.ToolCalls)},
	})
	for _, call := range first.ToolCalls {
		convo = append(convo, schemas.ChatMessage{
			Role:            schemas.ChatMessageRoleTool,
			Content:         &schemas.ChatMessageContent{ContentStr: schemas.Ptr(exec(ctx, call))},
			ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: schemas.Ptr(call.ID)},
		})
	}

	final, err := c.do(ctx, request{primary: winner, model: first.Model, messages: convo})
	if err != nil {
		if first.Text != "" {
			c.log.Warnf("tool continuation failed (%v); falling back to the first response", err)
			return first, nil
		}
		return nil, err
	}
	if final.Text == "" {
		final.Text = first.Text
	}
	return final, nil
}

// fallbacksFor builds the ladder Bifrost walks after an attempt on
// primary/model fails. It tries the rest of the primary provider's models
// first, so a rate-limited or retired model rolls to a sibling model at the
// same vendor before the request leaves for a different one, then appends the
// other configured providers in their configured order.
//
// The attempted (driver, model) pair is skipped, and repeats are dropped: a
// model listed under two entries would otherwise burn a fallback slot and add
// a round trip to every failure.
func fallbacksFor(chain []config.Provider, primary config.Provider, model string) []schemas.Fallback {
	out := make([]schemas.Fallback, 0, len(chain))
	seen := make(map[schemas.Fallback]bool)
	add := func(p config.Provider, m string) {
		if m == "" {
			return
		}
		f := schemas.Fallback{Provider: p.Driver, Model: m}
		if f.Provider == primary.Driver && f.Model == model {
			return
		}
		if seen[f] {
			return
		}
		seen[f] = true
		out = append(out, f)
	}

	for _, m := range primary.Models {
		add(primary, m)
	}
	for _, p := range chain {
		if p.Name == primary.Name {
			continue
		}
		add(p, p.Default())
	}
	return out
}

func providerByDriver(chain []config.Provider, driver string) (config.Provider, bool) {
	for _, p := range chain {
		if string(p.Driver) == driver {
			return p, true
		}
	}
	return config.Provider{}, false
}

func toolCallsToSchema(calls []ToolCall) []schemas.ChatAssistantMessageToolCall {
	out := make([]schemas.ChatAssistantMessageToolCall, 0, len(calls))
	for i, call := range calls {
		out = append(out, schemas.ChatAssistantMessageToolCall{
			Index: uint16(i),
			Type:  schemas.Ptr("function"),
			ID:    schemas.Ptr(call.ID),
			Function: schemas.ChatAssistantMessageToolCallFunction{
				Name:      schemas.Ptr(call.Name),
				Arguments: call.Arguments,
			},
		})
	}
	return out
}

func toChatError(berr *schemas.BifrostError) *ChatError {
	e := &ChatError{Message: berr.GetErrorString()}
	if berr.StatusCode != nil {
		e.StatusCode = *berr.StatusCode
	}
	return e
}

func parseResponse(resp *schemas.BifrostChatResponse) *Response {
	out := &Response{}
	if resp == nil {
		return out
	}

	ri := resp.ExtraFields.RoutingInfo
	out.Provider = string(ri.Provider)
	out.Model = ri.Model
	out.IsFallback = ri.IsFallback
	// Bifrost only fills these in once fallback resolution has run.
	if ri.PrimaryProvider != nil {
		out.PrimaryProvider = string(*ri.PrimaryProvider)
	}
	if ri.PrimaryModel != nil {
		out.PrimaryModel = *ri.PrimaryModel
	}
	if out.Model == "" {
		out.Model = resp.Model
	}

	for _, choice := range resp.Choices {
		// Message is promoted from an embedded pointer: touching it while the
		// pointer is nil panics, so both guards are required.
		if choice.ChatNonStreamResponseChoice == nil || choice.Message == nil {
			continue
		}
		msg := choice.Message
		if msg.Content != nil && msg.Content.ContentStr != nil {
			out.Text = *msg.Content.ContentStr
		}
		if msg.ChatAssistantMessage != nil {
			for _, tc := range msg.ChatAssistantMessage.ToolCalls {
				out.ToolCalls = append(out.ToolCalls, ToolCall{
					ID:        derefString(tc.ID),
					Name:      derefString(tc.Function.Name),
					Arguments: tc.Function.Arguments,
				})
			}
		}
		break
	}
	return out
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
