package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client side of the per-environment MCP edge, over net/http rather than the MCP SDK so it
// stays a standalone library.

const (
	// MCPServerPath is the HTTP path the edge serves MCP on.
	MCPServerPath = "/mcp"

	// MCPIdleProbeHeader ("true") exempts a request from the edge's idle-timer activity recording.
	MCPIdleProbeHeader = "X-Erun-Idle-Probe"

	// The edge rejects unsupported protocol revisions, so this is pinned rather than negotiated.
	mcpClientProtocolVersion = "2025-06-18"
	mcpClientName            = "erun"

	mcpSessionHeader         = "Mcp-Session-Id"
	mcpProtocolVersionHeader = "Mcp-Protocol-Version"
)

var (
	// ErrMCPEndpointUnreachable usually means the port-forward is down, not that the environment is broken.
	ErrMCPEndpointUnreachable = errors.New("MCP endpoint is not reachable")

	// ErrMCPTargetNotAnswering means the forward is up but the target is still starting; re-opening won't help.
	ErrMCPTargetNotAnswering = errors.New("the local port-forward is up but the environment behind it did not answer")

	// ErrMCPUnauthorized means the edge refused the bearer.
	ErrMCPUnauthorized = errors.New("MCP endpoint rejected the bearer token")

	// ErrMCPSessionLost means the edge answered without recognizing this client's session. It proves
	// the environment is reachable, unlike ErrMCPEndpointUnreachable.
	ErrMCPSessionLost = errors.New("the MCP edge answered, but without a session it recognizes for this client")
)

// MCPLocalEndpoint is the loopback URL of a port-forwarded edge.
func MCPLocalEndpoint(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, MCPServerPath)
}

// MCPTokenMinter runs before every request so long calls never carry an expired token.
type MCPTokenMinter func() (string, error)

type MCPToolCallParams struct {
	Endpoint      string
	MintToken     MCPTokenMinter
	ClientVersion string
	Tool          string
	Arguments     map[string]any
	// IdleProbe skips idle-timer activity for the whole request; never set it for a mutating tool.
	IdleProbe bool
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
	// IdleProbe skips idle-timer activity; listing tools is read-only.
	IdleProbe bool
}

// MCPTool keeps the descriptor's wire JSON so structured output re-emits the protocol's own
// shape; the typed fields serve the text renderer.
type MCPTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	// raw is empty only for a tool constructed locally.
	raw json.RawMessage
}

func (t *MCPTool) UnmarshalJSON(data []byte) error {
	var fields struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	t.Name = fields.Name
	t.Description = fields.Description
	t.InputSchema = fields.InputSchema
	t.raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON re-emits the decoded descriptor verbatim so unknown fields survive.
func (t MCPTool) MarshalJSON() ([]byte, error) {
	if len(t.raw) > 0 {
		return t.raw, nil
	}
	type constructedTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	}
	return json.Marshal(constructedTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
}

type MCPToolListResult struct {
	Tools []MCPTool `json:"tools"`
}

// CallMCPTool performs one tool call. A tool-reported failure returns both the result and an error.
func CallMCPTool(ctx context.Context, params MCPToolCallParams) (MCPToolCallResult, error) {
	tool := strings.TrimSpace(params.Tool)
	if tool == "" {
		return MCPToolCallResult{}, fmt.Errorf("MCP tool name is required")
	}
	session, err := openMCPSession(ctx, params.Endpoint, params.MintToken, params.ClientVersion, params.IdleProbe)
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

// ListMCPTools returns the tools an environment's MCP edge exposes.
func ListMCPTools(ctx context.Context, params MCPToolListParams) (MCPToolListResult, error) {
	session, err := openMCPSession(ctx, params.Endpoint, params.MintToken, params.ClientVersion, params.IdleProbe)
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
	idleProbe     bool
	// localPort is 0 unless the endpoint is a local port-forward, the only kind that goes stale.
	localPort int
	// recovering limits one bad request to a single retry.
	recovering bool
	// awaitStartup gives the first request a bounded wait. Only the stdio proxy sets it: a client's
	// initialize is never repeated, while typed callers already reattach and retry.
	awaitStartup bool
	// startupWaited is set on the first attempt regardless of outcome.
	startupWaited bool
	// startupWait overrides mcpStartupReachabilityWait in tests; zero means the production bound.
	startupWait time.Duration
	// notice reports otherwise invisible recoveries; nil when there is nowhere to report.
	notice func(string)
}

// newMCPSession skips the handshake so a relay forwarding the client's own initialize shares framing.
func newMCPSession(endpoint string, mintToken MCPTokenMinter, clientVersion string, idleProbe, awaitStartup bool) (*mcpSession, error) {
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
	// No client timeout: tool calls can run for minutes, so the caller's context owns the deadline.
	return &mcpSession{
		endpoint:      endpoint,
		mintToken:     mintToken,
		clientVersion: clientVersion,
		client:        &http.Client{},
		idleProbe:     idleProbe,
		localPort:     localMCPEndpointPort(endpoint),
		awaitStartup:  awaitStartup,
	}, nil
}

// localMCPEndpointPort returns the port of an http://127.0.0.1 or localhost URL, else 0.
func localMCPEndpointPort(endpoint string) int {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return 0
	}
	host := parsed.Hostname()
	if host != "127.0.0.1" && host != "localhost" {
		return 0
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port <= 0 {
		return 0
	}
	return port
}

// openMCPSession serves typed callers, which reattach and retry on their own, so no startup wait.
func openMCPSession(ctx context.Context, endpoint string, mintToken MCPTokenMinter, clientVersion string, idleProbe bool) (*mcpSession, error) {
	session, err := newMCPSession(endpoint, mintToken, clientVersion, idleProbe, false)
	if err != nil {
		return nil, err
	}
	if err := session.handshake(ctx); err != nil {
		return nil, err
	}
	return session, nil
}

// handshake is also the recovery path, so both agree on what a live session requires.
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

// postRaw sends one encoded JSON-RPC message and returns the reply, or nil for a 202.
// Each request recovers at most once from: a forgotten session (404, or 200 with the
// session-initialization error), by re-handshaking; and a dropped local tunnel, by resending
// on the same session.
func (s *mcpSession) postRaw(ctx context.Context, body []byte) ([]byte, error) {
	raw, err := s.postOnce(ctx, body)
	if err == nil || s.recovering {
		return raw, err
	}
	if errors.Is(err, ErrMCPSessionLost) {
		return s.recoverLostSession(ctx, body)
	}
	if errors.Is(err, ErrMCPEndpointUnreachable) && s.localPort > 0 && CanReachLocalMCPEndpoint(s.localPort) {
		return s.retryAfterTunnelRecovery(ctx, body)
	}
	return raw, err
}

func (s *mcpSession) recoverLostSession(ctx context.Context, body []byte) ([]byte, error) {
	s.recovering = true
	defer func() { s.recovering = false }()
	s.sessionID = ""
	if err := s.handshake(ctx); err != nil {
		return nil, fmt.Errorf("re-establish the MCP session with %s: %w", s.endpoint, err)
	}
	s.report(fmt.Sprintf("the MCP edge at %s had forgotten its session; re-initialized and retried", s.endpoint))
	return s.postOnce(ctx, body)
}

func (s *mcpSession) retryAfterTunnelRecovery(ctx context.Context, body []byte) ([]byte, error) {
	s.recovering = true
	defer func() { s.recovering = false }()
	s.report(fmt.Sprintf("the local port-forward to %s answers again after dropping mid-request; retrying", s.endpoint))
	return s.postOnce(ctx, body)
}

func (s *mcpSession) report(message string) {
	if s.notice == nil {
		return
	}
	s.notice(message)
}

// postOnce fails fast on a bound but dead local port instead of waiting on the caller's deadline.
// An unbound port is left to the dial, except an awaitStartup session's first request, which
// waits briefly for the forward to come up.
func (s *mcpSession) postOnce(ctx context.Context, body []byte) ([]byte, error) {
	s.awaitStartupOnce(ctx)
	if s.localPort > 0 && !CanReachLocalMCPEndpoint(s.localPort) && LocalPortIsBound(s.localPort) {
		return nil, s.unreachableTunnelError()
	}
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
	return s.decodePostReply(resp)
}

// decodePostReply treats a 200 carrying the session-initialization error as a lost session.
func (s *mcpSession) decodePostReply(resp *http.Response) ([]byte, error) {
	reply, err := decodeMCPReply(resp)
	if err != nil {
		return nil, err
	}
	if err := mcpUninitializedSessionError(s.endpoint, resp.StatusCode, reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// Matched on the message: the SDK raises it as a generic internal error code.
const mcpSessionInitializationGuard = "is invalid during session initialization"

// mcpUninitializedSessionError reports a 200 session-initialization refusal as the same
// recoverable loss as a 404.
func mcpUninitializedSessionError(endpoint string, status int, reply []byte) error {
	if len(reply) == 0 {
		return nil
	}
	decoded, ok := decodeJSONRPCResponse(reply)
	if !ok {
		return nil
	}
	if decoded.Error == nil || !strings.Contains(decoded.Error.Message, mcpSessionInitializationGuard) {
		return nil
	}
	return fmt.Errorf("%w: %s (HTTP %d) answered %s", ErrMCPSessionLost, endpoint, status, decoded.Error.detail())
}

func (s *mcpSession) startupReachabilityWait() time.Duration {
	if s.startupWait > 0 {
		return s.startupWait
	}
	return mcpStartupReachabilityWait
}

// awaitStartupOnce waits for the local port once per session.
func (s *mcpSession) awaitStartupOnce(ctx context.Context) {
	if !s.awaitStartup || s.localPort <= 0 || s.startupWaited {
		return
	}
	s.startupWaited = true
	awaitLocalMCPEndpointReachable(ctx, s.localPort, s.startupReachabilityWait())
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
	if s.idleProbe {
		req.Header.Set(MCPIdleProbeHeader, "true")
	}
	return req, nil
}

// unreachableTunnelError reports a held but unanswering port; an accept-then-close means the
// target is still starting, so re-opening won't help.
func (s *mcpSession) unreachableTunnelError() error {
	if ClassifyLocalMCPUnreachable(s.localPort) == LocalMCPTargetNotAnswering {
		return fmt.Errorf("%w: %w: %s (127.0.0.1:%d is held and took the connection, but the environment behind it closed it without answering — it is most likely still starting, so retry shortly)", ErrMCPEndpointUnreachable, ErrMCPTargetNotAnswering, s.endpoint, s.localPort)
	}
	return fmt.Errorf("%w: %s (127.0.0.1:%d is held but the edge never answers — a stale port-forward)", ErrMCPEndpointUnreachable, s.endpoint, s.localPort)
}

// transportError reports refused dials and mid-request drops alike as unreachable.
func (s *mcpSession) transportError(err error) error {
	return fmt.Errorf("%w: %s (%w)", ErrMCPEndpointUnreachable, s.endpoint, err)
}

// Only a request that carried a session id can mean the edge dropped the session.
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
		return fmt.Errorf("%w: %s (HTTP %d)%s", ErrMCPSessionLost, endpoint, resp.StatusCode, detail)
	}
	return fmt.Errorf("MCP endpoint %s returned HTTP %d%s", endpoint, resp.StatusCode, detail)
}

// decodeMCPReply accepts plain JSON or SSE framing and returns the raw JSON-RPC object.
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

// decodeMCPEventStream returns the first result or error event, skipping progress notifications.
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

func decodeJSONRPCResponse(reply []byte) (mcpJSONRPCResponse, bool) {
	var decoded mcpJSONRPCResponse
	err := json.Unmarshal(reply, &decoded)
	return decoded, err == nil
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
