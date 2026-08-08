package eruncommon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Stdio side of the per-environment MCP edge. An MCP client reads its server
// config once at launch and cannot refresh a header afterwards, so a bearer
// written into that config expires mid-session and every tool for the
// environment fails at once. This relay removes the credential from the config
// entirely: the client launches a command, and the bearer is minted here, per
// request, for as long as the session runs.

// jsonRPCInternalError is the code a relay failure is reported under, so a
// client that cannot reach the edge renders an actionable message instead of
// seeing its pipe go quiet.
const jsonRPCInternalError = -32603

type MCPStdioProxyParams struct {
	Endpoint      string
	MintToken     MCPTokenMinter
	ClientVersion string
	In            io.Reader
	Out           io.Writer
	// Diagnostics carries every human-facing line. Out is JSON-RPC and nothing
	// else: one stray byte there desynchronizes the client's parser.
	Diagnostics io.Writer
	// DescribeError turns a relay failure into the message the client shows. The
	// caller owns the recovery wording because it knows which command brings the
	// edge back up; nil falls back to the raw error.
	DescribeError func(error) string
}

// RunMCPStdioProxy relays newline-delimited JSON-RPC between a client on stdin
// and an environment's MCP edge until stdin closes. Messages are relayed one at
// a time: a client waits for each reply before it acts on the next, and serial
// relay keeps the edge's session bookkeeping in the order the client wrote it.
func RunMCPStdioProxy(ctx context.Context, params MCPStdioProxyParams) error {
	if params.In == nil || params.Out == nil {
		return fmt.Errorf("MCP stdio proxy requires an input and an output stream")
	}
	session, err := newMCPSession(params.Endpoint, params.MintToken, params.ClientVersion)
	if err != nil {
		return err
	}
	proxy := &mcpStdioProxy{session: session, params: params}
	session.notice = proxy.diagnose
	reader := bufio.NewReader(params.In)
	for {
		message, err := readJSONRPCLine(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read MCP client message: %w", err)
		}
		if len(message) == 0 {
			continue
		}
		if err := proxy.relay(ctx, message); err != nil {
			return err
		}
	}
}

type mcpStdioProxy struct {
	session *mcpSession
	params  MCPStdioProxyParams
}

// relay forwards one client message and writes at most one line to stdout. A
// failure is answered as a JSON-RPC error carrying the message's own id, so the
// client surfaces it against the request it is waiting on; a notification has no
// id to answer against, so it only reaches the diagnostics stream. Either way the
// relay keeps serving — a transient edge outage must not kill the session.
func (p *mcpStdioProxy) relay(ctx context.Context, message []byte) error {
	id := jsonRPCMessageID(message)
	reply, err := p.session.postRaw(ctx, message)
	switch {
	case err != nil:
		p.diagnose(err.Error())
		return p.writeError(id, p.describe(err))
	case len(reply) > 0:
		return p.writeReply(reply)
	case len(id) > 0:
		detail := fmt.Sprintf("the MCP edge at %s accepted the request but returned no reply", p.params.Endpoint)
		p.diagnose(detail)
		return p.writeError(id, detail)
	}
	return nil
}

// writeReply compacts before writing so a pretty-printed edge reply still
// reaches the client as exactly one line.
func (p *mcpStdioProxy) writeReply(reply []byte) error {
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, reply); err != nil {
		p.diagnose(fmt.Sprintf("the MCP edge at %s returned a reply that is not JSON: %v", p.params.Endpoint, err))
		return nil
	}
	return p.writeLine(compact.Bytes())
}

func (p *mcpStdioProxy) writeError(id json.RawMessage, message string) error {
	if len(id) == 0 {
		return nil
	}
	encoded, err := json.Marshal(mcpJSONRPCErrorReply{
		JSONRPC: "2.0",
		ID:      id,
		Error:   mcpJSONRPCError{Code: jsonRPCInternalError, Message: message},
	})
	if err != nil {
		return err
	}
	return p.writeLine(encoded)
}

func (p *mcpStdioProxy) writeLine(payload []byte) error {
	if _, err := p.params.Out.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write MCP reply: %w", err)
	}
	return nil
}

func (p *mcpStdioProxy) diagnose(message string) {
	if p.params.Diagnostics == nil {
		return
	}
	_, _ = fmt.Fprintln(p.params.Diagnostics, "mcp proxy: "+message)
}

func (p *mcpStdioProxy) describe(err error) string {
	if p.params.DescribeError == nil {
		return err.Error()
	}
	return p.params.DescribeError(err)
}

type mcpJSONRPCErrorReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   mcpJSONRPCError `json:"error"`
}

// readJSONRPCLine returns the next message, tolerating a final line the client
// wrote without a trailing newline before closing.
func readJSONRPCLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	trimmed := bytes.TrimSpace(line)
	if err != nil {
		if errors.Is(err, io.EOF) && len(trimmed) > 0 {
			return trimmed, nil
		}
		return nil, err
	}
	return trimmed, nil
}

// jsonRPCMessageID reports the message's id, or nil when it carries none — the
// notification case, which the protocol answers with nothing at all. A literal
// null id is a notification too.
func jsonRPCMessageID(message []byte) json.RawMessage {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		return nil
	}
	if len(bytes.TrimSpace(envelope.ID)) == 0 || bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("null")) {
		return nil
	}
	return envelope.ID
}
