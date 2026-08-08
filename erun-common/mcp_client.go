package eruncommon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// Client side of the per-environment MCP edge. The JSON-RPC round-trip is
// implemented over net/http rather than the MCP SDK so this stays usable as a
// standalone library, and so any caller — CLI, script, agent — reaches an
// environment through erun instead of re-deriving the token and framing rules.

const (
	// MCPServerPath is the HTTP path the per-env erun-mcp edge serves MCP on.
	MCPServerPath = "/mcp"

	// The edge rejects a POST naming a protocol revision it does not support, so
	// this is pinned rather than negotiated downward.
	mcpClientProtocolVersion = "2025-06-18"
	mcpClientName            = "erun"

	mcpSessionHeader         = "Mcp-Session-Id"
	mcpProtocolVersionHeader = "Mcp-Protocol-Version"
)

var (
	// ErrMCPEndpointUnreachable means nothing answered on the local MCP port,
	// which normally means the port-forward is not up rather than that the
	// environment is broken.
	ErrMCPEndpointUnreachable = errors.New("MCP endpoint is not reachable")

	// ErrMCPUnauthorized means the edge refused the bearer.
	ErrMCPUnauthorized = errors.New("MCP endpoint rejected the bearer token")

	// errMCPSessionLost means the edge no longer knows the session the client
	// pinned — an edge that restarted inside a still-running pod, or a session
	// that aged out. It is recoverable by handshaking again, so it never leaves
	// this package.
	errMCPSessionLost = errors.New("MCP endpoint no longer knows this session")
)

// MCPLocalEndpoint is the loopback URL a port-forwarded environment edge answers
// on.
func MCPLocalEndpoint(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, MCPServerPath)
}

// MCPTokenMinter returns a freshly signed bearer. It is invoked immediately
// before every request so a tool call that runs for minutes cannot fail because
// the token aged out while the call was in flight.
type MCPTokenMinter func() (string, error)

type MCPToolCallParams struct {
	Endpoint      string
	MintToken     MCPTokenMinter
	ClientVersion string
	Tool          string
	Arguments     map[string]any
}

type MCPToolCallResult struct {
	Tool       string          `json:"tool"`
	Text       string          `json:"text,omitempty"`
	Structured json.RawMessage `json:"structured,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
}

type MCPToolListParams struct {
	Endpoint      string
	MintToken     MCPTokenMinter
	ClientVersion string
}

type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type MCPToolListResult struct {
	Tools []MCPTool `json:"tools"`
}

// CallMCPTool performs one tool call against an environment's MCP edge. A tool
// that reports its own failure comes back as a populated result plus an error, so
// a caller can surface the tool's own message and still render the payload.
func CallMCPTool(ctx context.Context, params MCPToolCallParams) (MCPToolCallResult, error) {
	tool := strings.TrimSpace(params.Tool)
	if tool == "" {
		return MCPToolCallResult{}, fmt.Errorf("MCP tool name is required")
	}
	session, err := openMCPSession(ctx, params.Endpoint, params.MintToken, params.ClientVersion)
	if err != nil {
		return MCPToolCallResult{}, err
	}
	arguments := params.Arguments
	if arguments == nil {
		arguments = map[string]any{}
	}
	raw, err := session.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": arguments})
	if err != nil {
		return MCPToolCallResult{}, err
	}
	var payload mcpToolCallPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return MCPToolCallResult{}, fmt.Errorf("decode MCP result for tool %s: %w", tool, err)
	}
	result := MCPToolCallResult{
		Tool:       tool,
		Text:       payload.text(),
		Structured: payload.StructuredContent,
		IsError:    payload.IsError,
	}
	if payload.IsError {
		detail := result.Text
		if strings.TrimSpace(detail) == "" {
			detail = "no detail reported"
		}
		return result, fmt.Errorf("MCP tool %s reported an error: %s", tool, detail)
	}
	return result, nil
}

// ListMCPTools returns the tools an environment's MCP edge exposes, with their
// input schemas.
func ListMCPTools(ctx context.Context, params MCPToolListParams) (MCPToolListResult, error) {
	session, err := openMCPSession(ctx, params.Endpoint, params.MintToken, params.ClientVersion)
	if err != nil {
		return MCPToolListResult{}, err
	}
	raw, err := session.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return MCPToolListResult{}, err
	}
	var result MCPToolListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return MCPToolListResult{}, fmt.Errorf("decode MCP tool list: %w", err)
	}
	return result, nil
}

type mcpSession struct {
	endpoint      string
	mintToken     MCPTokenMinter
	clientVersion string
	client        *http.Client
	sessionID     string
	nextID        int
	// recovering suppresses a nested re-handshake so one lost session costs at
	// most one retry.
	recovering bool
	// notice reports a recovery the caller would otherwise never see; nil when
	// the caller has nowhere to put diagnostics.
	notice func(string)
}

// newMCPSession prepares the transport without performing the handshake, so a
// relay that forwards a client's own initialize can share the header, session,
// and framing rules with the typed callers.
func newMCPSession(endpoint string, mintToken MCPTokenMinter, clientVersion string) (*mcpSession, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("MCP endpoint is required")
	}
	if mintToken == nil {
		return nil, fmt.Errorf("MCP token minter is required")
	}
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = "unknown"
	}
	// Deliberately no client timeout: a tool call may legitimately run for many
	// minutes, so the deadline belongs to the caller's context.
	return &mcpSession{
		endpoint:      endpoint,
		mintToken:     mintToken,
		clientVersion: clientVersion,
		client:        &http.Client{},
	}, nil
}

func openMCPSession(ctx context.Context, endpoint string, mintToken MCPTokenMinter, clientVersion string) (*mcpSession, error) {
	session, err := newMCPSession(endpoint, mintToken, clientVersion)
	if err != nil {
		return nil, err
	}
	if err := session.handshake(ctx); err != nil {
		return nil, err
	}
	return session, nil
}

// handshake claims a session on the edge. It is also the recovery path, so the
// two cannot drift apart on what a live session requires.
func (s *mcpSession) handshake(ctx context.Context) error {
	if _, err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion": mcpClientProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": mcpClientName, "version": s.clientVersion},
	}); err != nil {
		return err
	}
	return s.notify(ctx, "notifications/initialized")
}

func (s *mcpSession) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.nextID++
	response, err := s.post(ctx, mcpJSONRPCRequest{JSONRPC: "2.0", ID: s.nextID, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("MCP %s returned no reply", method)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("MCP %s failed: %s", method, response.Error.detail())
	}
	return response.Result, nil
}

// A notification carries no id, so the edge answers 202 with no body.
func (s *mcpSession) notify(ctx context.Context, method string) error {
	_, err := s.post(ctx, mcpJSONRPCRequest{JSONRPC: "2.0", Method: method})
	return err
}

func (s *mcpSession) post(ctx context.Context, payload mcpJSONRPCRequest) (*mcpJSONRPCResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	raw, err := s.postRaw(ctx, body)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var decoded mcpJSONRPCResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode MCP reply: %w", err)
	}
	return &decoded, nil
}

// postRaw sends one already-encoded JSON-RPC message and returns the raw reply,
// or nil when the edge answered without a body — the 202 a notification gets.
// Bearer minting and session-id capture live here so a raw relay and a typed
// call cannot drift apart on either.
//
// A session the edge has forgotten is recovered rather than surfaced: a
// long-lived relay pins one session id for its whole lifetime, so treating the
// edge's 404 as terminal would kill every request for the environment until the
// relay itself was restarted. The re-handshake happens at most once per request.
func (s *mcpSession) postRaw(ctx context.Context, body []byte) ([]byte, error) {
	raw, err := s.postOnce(ctx, body)
	if err == nil || s.recovering || !errors.Is(err, errMCPSessionLost) {
		return raw, err
	}
	s.recovering = true
	defer func() { s.recovering = false }()
	s.sessionID = ""
	if err := s.handshake(ctx); err != nil {
		return nil, fmt.Errorf("re-establish the MCP session with %s: %w", s.endpoint, err)
	}
	s.report(fmt.Sprintf("the MCP edge at %s had forgotten its session; re-initialized and retried", s.endpoint))
	return s.postOnce(ctx, body)
}

func (s *mcpSession) report(message string) {
	if s.notice == nil {
		return
	}
	s.notice(message)
}

func (s *mcpSession) postOnce(ctx context.Context, body []byte) ([]byte, error) {
	token, err := s.mintToken()
	if err != nil {
		return nil, err
	}
	req, err := s.newRequest(ctx, body, token)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, s.transportError(err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	pinned := s.sessionID != ""
	if id := strings.TrimSpace(resp.Header.Get(mcpSessionHeader)); id != "" {
		s.sessionID = id
	}
	if err := mcpStatusError(s.endpoint, resp, pinned); err != nil {
		return nil, err
	}
	return decodeMCPReply(resp)
}

func (s *mcpSession) newRequest(ctx context.Context, body []byte, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(mcpProtocolVersionHeader, mcpClientProtocolVersion)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", mcpClientName+"/"+s.clientVersion)
	if s.sessionID != "" {
		req.Header.Set(mcpSessionHeader, s.sessionID)
	}
	return req, nil
}

// A failure to dial the loopback port is reported as unreachable rather than as a
// generic error, because the operator's fix is to bring the port-forward up.
func (s *mcpSession) transportError(err error) error {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return fmt.Errorf("%w: %s (%v)", ErrMCPEndpointUnreachable, s.endpoint, err)
	}
	return fmt.Errorf("call MCP endpoint %s: %w", s.endpoint, err)
}

// sessionPinned says the request carried a session id, which is what separates
// the edge having dropped that session from the endpoint path simply not
// existing — only the former is worth re-handshaking for.
func mcpStatusError(endpoint string, resp *http.Response, sessionPinned bool) error {
	if resp.StatusCode < 400 {
		return nil
	}
	detail := ""
	if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
		if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
			detail = ": " + trimmed
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: %s (HTTP %d)%s", ErrMCPUnauthorized, endpoint, resp.StatusCode, detail)
	}
	if resp.StatusCode == http.StatusNotFound && sessionPinned {
		return fmt.Errorf("%w: %s (HTTP %d)%s", errMCPSessionLost, endpoint, resp.StatusCode, detail)
	}
	return fmt.Errorf("MCP endpoint %s returned HTTP %d%s", endpoint, resp.StatusCode, detail)
}

// The edge answers either a plain JSON body or an SSE-framed stream depending on
// how it was configured, so both framings are accepted. The reply is returned as
// the raw JSON-RPC object so a relay can forward it verbatim.
func decodeMCPReply(resp *http.Response) ([]byte, error) {
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return decodeMCPEventStream(resp.Body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read MCP reply: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	return body, nil
}

// Only the first event carrying a JSON-RPC result or error is consumed; progress
// notifications on the same stream are skipped. Each event's payload is one line,
// which is how the edge frames it.
func decodeMCPEventStream(body io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "data:")
		if !ok {
			continue
		}
		payload := strings.TrimSpace(data)
		var decoded mcpJSONRPCResponse
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			continue
		}
		if decoded.Result != nil || decoded.Error != nil {
			return []byte(payload), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read MCP event stream: %w", err)
	}
	return nil, fmt.Errorf("MCP event stream carried no JSON-RPC reply")
}

type mcpJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *mcpJSONRPCError `json:"error,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e mcpJSONRPCError) detail() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "no message"
	}
	if len(e.Data) == 0 {
		return fmt.Sprintf("%s (code %d)", message, e.Code)
	}
	return fmt.Sprintf("%s (code %d): %s", message, e.Code, string(e.Data))
}

type mcpToolCallPayload struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

func (p mcpToolCallPayload) text() string {
	parts := make([]string, 0, len(p.Content))
	for _, item := range p.Content {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		parts = append(parts, item.Text)
	}
	return strings.Join(parts, "\n")
}
